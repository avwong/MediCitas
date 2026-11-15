package agenda

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"
)

// fakeRedis es un Redis en memoria que se puede "caer".
type fakeRedis struct {
	values map[string]string
	down   bool
}

var errRedisDown = errors.New("redis is down")

func newFakeRedis() *fakeRedis {
	return &fakeRedis{values: map[string]string{}}
}

func (f *fakeRedis) Get(_ context.Context, key string) (string, bool, error) {
	if f.down {
		return "", false, errRedisDown
	}

	value, found := f.values[key]

	return value, found, nil
}

func (f *fakeRedis) Set(_ context.Context, key, value string, _ time.Duration) error {
	if f.down {
		return errRedisDown
	}

	f.values[key] = value

	return nil
}

func (f *fakeRedis) Incr(_ context.Context, key string) (int64, error) {
	if f.down {
		return 0, errRedisDown
	}

	current, _ := strconv.ParseInt(f.values[key], 10, 64)
	current++
	f.values[key] = strconv.FormatInt(current, 10)

	return current, nil
}

func newTestCache() (*RedisAvailabilityCache, *fakeRedis) {
	redis := newFakeRedis()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return newRedisAvailabilityCache(redis, logger), redis
}

const (
	cacheClinic       = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	cachePractitioner = "c1c1c1c1-0000-4000-8000-000000000001"
	cacheService      = "d1d1d1d1-0000-4000-8000-000000000001"
)

var cachedSlots = []Slot{{StartsAt: at(8, 0), EndsAt: at(8, 30)}}

// Cubre I8 (una invalidación deja sin efecto las entradas anteriores)
func TestInvalidateOrphansPreviousEntries(t *testing.T) {
	ctx := context.Background()
	cache, _ := newTestCache()

	version, ok := cache.Version(ctx, cacheClinic, cachePractitioner)
	if !ok || version != 0 {
		t.Fatalf("a new agenda starts at version 0: got %d, %v", version, ok)
	}

	key := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, version)
	cache.Set(ctx, key, cachedSlots)

	if slots, hit := cache.Get(ctx, key); !hit || len(slots) != 1 || !slots[0].StartsAt.Equal(at(8, 0)) {
		t.Fatalf("the stored entry was not read back: %v, %v", slots, hit)
	}

	cache.Invalidate(ctx, cacheClinic, cachePractitioner)

	version, ok = cache.Version(ctx, cacheClinic, cachePractitioner)
	if !ok || version != 1 {
		t.Fatalf("after one invalidation the version must be 1: got %d, %v", version, ok)
	}

	newKey := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, version)
	if newKey == key {
		t.Fatal("the key must change with the version")
	}

	if _, hit := cache.Get(ctx, newKey); hit {
		t.Fatal("an entry stored before the invalidation must not be served after it")
	}
}

// Cubre I8 (una lectura vieja no puede pisar una invalidación)
//
// El lector toma la versión, un cambio hace commit e invalida, y solo después
// el lector guarda lo que había calculado. Su resultado queda bajo la versión
// anterior y la siguiente consulta no lo encuentra.
func TestStaleReadCannotOverwriteAnInvalidation(t *testing.T) {
	ctx := context.Background()
	cache, _ := newTestCache()

	readerVersion, _ := cache.Version(ctx, cacheClinic, cachePractitioner)

	cache.Invalidate(ctx, cacheClinic, cachePractitioner)

	staleKey := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, readerVersion)
	cache.Set(ctx, staleKey, cachedSlots)

	current, _ := cache.Version(ctx, cacheClinic, cachePractitioner)
	currentKey := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, current)

	if _, hit := cache.Get(ctx, currentKey); hit {
		t.Fatal("the stale result was served under the current version")
	}
}

// Cubre I8 (Redis caído durante la invalidación)
//
// Si la invalidación falla, Redis conserva una entrada anterior a un cambio
// que ya hizo commit. La agenda deja de servirse desde la caché hasta que la
// invalidación se logra, y entonces la versión sube.
func TestFailedInvalidationDisablesTheCacheForThatAgenda(t *testing.T) {
	ctx := context.Background()
	cache, redis := newTestCache()

	version, _ := cache.Version(ctx, cacheClinic, cachePractitioner)
	key := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, version)
	cache.Set(ctx, key, cachedSlots)

	redis.down = true
	cache.Invalidate(ctx, cacheClinic, cachePractitioner)

	if _, ok := cache.Version(ctx, cacheClinic, cachePractitioner); ok {
		t.Fatal("with Redis down the cache must not be used")
	}

	// Redis vuelve con la entrada vieja todavía guardada.
	redis.down = false

	version, ok := cache.Version(ctx, cacheClinic, cachePractitioner)
	if !ok {
		t.Fatal("once Redis is back the pending invalidation must succeed")
	}

	if version != 1 {
		t.Fatalf("the pending invalidation must bump the version: got %d", version)
	}

	current := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, version)
	if _, hit := cache.Get(ctx, current); hit {
		t.Fatal("the entry from before the failed invalidation must not be served")
	}

	// Otra agenda nunca dejó de ser confiable.
	if _, ok := cache.Version(ctx, cacheClinic, "c1c1c1c1-0000-4000-8000-000000000002"); !ok {
		t.Fatal("a failed invalidation must only affect its own agenda")
	}
}

// Si Redis no responde al leer, la consulta va a la base sin fallar.
func TestCacheIsBypassedWhenRedisIsDown(t *testing.T) {
	ctx := context.Background()
	cache, redis := newTestCache()

	redis.down = true

	if _, ok := cache.Version(ctx, cacheClinic, cachePractitioner); ok {
		t.Fatal("with Redis down the cache must not be used")
	}

	if _, hit := cache.Get(ctx, "any"); hit {
		t.Fatal("with Redis down there is nothing to read")
	}

	// Guardar tampoco debe fallar ni entrar en pánico.
	cache.Set(ctx, "any", cachedSlots)
}

// La clave separa clínicas, profesionales, fechas y servicios.
func TestAvailabilityKeyIsScoped(t *testing.T) {
	base := availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, 3)

	others := []string{
		availabilityKey("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", cachePractitioner, "2030-03-04", cacheService, 3),
		availabilityKey(cacheClinic, "c1c1c1c1-0000-4000-8000-000000000002", "2030-03-04", cacheService, 3),
		availabilityKey(cacheClinic, cachePractitioner, "2030-03-05", cacheService, 3),
		availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", "d1d1d1d1-0000-4000-8000-000000000002", 3),
		availabilityKey(cacheClinic, cachePractitioner, "2030-03-04", cacheService, 4),
	}

	for _, other := range others {
		if other == base {
			t.Errorf("two different entries share the key %s", base)
		}
	}
}
