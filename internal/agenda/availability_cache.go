package agenda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// AvailabilityCache guarda en Redis las respuestas de disponibilidad (RF7).
// La fuente de verdad sigue siendo PostgreSQL: la caché solo ahorra recalcular
// una agenda que no ha cambiado.
//
// Cada agenda (un profesional de una clínica) tiene un número de versión, y la
// versión forma parte de la clave de cada entrada. Invalidar es subir la
// versión: las entradas anteriores quedan bajo claves que nadie vuelve a
// pedir y vencen solas.
type AvailabilityCache interface {
	// Version devuelve la versión vigente de la agenda. Con ok = false la
	// caché no es confiable en este momento y no se debe leer ni escribir.
	Version(ctx context.Context, clinicID, practitionerID string) (version int64, ok bool)
	Get(ctx context.Context, key string) ([]Slot, bool)
	Set(ctx context.Context, key string, slots []Slot)
	// Invalidate sube la versión de la agenda.
	Invalidate(ctx context.Context, clinicID, practitionerID string)
}

// availabilityKey es la clave de una entrada: una agenda, una fecha, un
// servicio y la versión de la agenda con la que se calculó.
func availabilityKey(clinicID, practitionerID, date, serviceID string, version int64) string {
	return fmt.Sprintf("avail:%s:%s:%s:%s:v%d", clinicID, practitionerID, date, serviceID, version)
}

func versionKey(clinicID, practitionerID string) string {
	return fmt.Sprintf("avail-version:%s:%s", clinicID, practitionerID)
}

// noCache es la caché desactivada: toda consulta se calcula contra la base.
type noCache struct{}

// NewNoCache devuelve una caché que nunca guarda nada. Se usa cuando Redis no
// está configurado y para medir la disponibilidad sin caché.
func NewNoCache() AvailabilityCache { return noCache{} }

func (noCache) Version(context.Context, string, string) (int64, bool) { return 0, false }
func (noCache) Get(context.Context, string) ([]Slot, bool)            { return nil, false }
func (noCache) Set(context.Context, string, []Slot)                   {}
func (noCache) Invalidate(context.Context, string, string)            {}

// redisCommands es lo que la caché usa de Redis. Existe para poder probar la
// caché con un Redis que falla.
type redisCommands interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Incr(ctx context.Context, key string) (int64, error)
}

// RedisAvailabilityCache implementa AvailabilityCache sobre Redis.
type RedisAvailabilityCache struct {
	redis  redisCommands
	logger *slog.Logger
	ttl    time.Duration

	// stale son las agendas cuya invalidación falló. Mientras una agenda
	// esté aquí no se sirve desde la caché: Redis podría tener una entrada
	// anterior a un cambio que ya hizo commit en la base (I8).
	mu    sync.Mutex
	stale map[string]struct{}
}

// NewRedisAvailabilityCache devuelve la caché de disponibilidad sobre el Redis
// en addr. Los timeouts son cortos: si Redis tarda, la disponibilidad se
// calcula contra la base en lugar de esperar.
func NewRedisAvailabilityCache(addr string, logger *slog.Logger) *RedisAvailabilityCache {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond,
		MaxRetries:   1,
	})

	return newRedisAvailabilityCache(goRedis{client: client}, logger)
}

func newRedisAvailabilityCache(commands redisCommands, logger *slog.Logger) *RedisAvailabilityCache {
	return &RedisAvailabilityCache{
		redis:  commands,
		logger: logger,
		// La vida de las entradas no es la política de invalidación: solo
		// limpia las versiones viejas y acota el daño si este proceso muere
		// justo después de un commit y antes de invalidar.
		ttl:   10 * time.Minute,
		stale: make(map[string]struct{}),
	}
}

// Version devuelve la versión de la agenda. Si una invalidación anterior de
// esta agenda falló, primero la reintenta; mientras no lo logre responde
// ok = false para que la consulta vaya a la base.
func (c *RedisAvailabilityCache) Version(ctx context.Context, clinicID, practitionerID string) (int64, bool) {
	key := versionKey(clinicID, practitionerID)

	if c.isStale(key) {
		version, err := c.redis.Incr(ctx, key)
		if err != nil {
			return 0, false
		}

		c.setStale(key, false)

		return version, true
	}

	value, found, err := c.redis.Get(ctx, key)
	if err != nil {
		c.logger.WarnContext(ctx, "availability cache unavailable; reading from the database", "error", err)
		return 0, false
	}

	if !found {
		return 0, true
	}

	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}

	return version, true
}

// Get devuelve los espacios guardados bajo key, si existen.
func (c *RedisAvailabilityCache) Get(ctx context.Context, key string) ([]Slot, bool) {
	value, found, err := c.redis.Get(ctx, key)
	if err != nil || !found {
		return nil, false
	}

	var slots []Slot
	if err := json.Unmarshal([]byte(value), &slots); err != nil {
		return nil, false
	}

	return slots, true
}

// Set guarda los espacios bajo key. Si falla no pasa nada: la próxima consulta
// vuelve a calcular.
func (c *RedisAvailabilityCache) Set(ctx context.Context, key string, slots []Slot) {
	value, err := json.Marshal(slots)
	if err != nil {
		return
	}

	if err := c.redis.Set(ctx, key, string(value), c.ttl); err != nil {
		c.logger.WarnContext(ctx, "could not store availability in the cache", "error", err)
	}
}

// Invalidate sube la versión de la agenda. Si Redis no responde, la agenda se
// marca como no confiable y deja de servirse desde la caché hasta que la
// invalidación se logre.
func (c *RedisAvailabilityCache) Invalidate(ctx context.Context, clinicID, practitionerID string) {
	key := versionKey(clinicID, practitionerID)

	if _, err := c.redis.Incr(ctx, key); err != nil {
		c.setStale(key, true)
		c.logger.ErrorContext(
			ctx,
			"could not invalidate the availability cache; the agenda is served from the database until it succeeds",
			"practitioner_id", practitionerID,
			"error", err,
		)

		return
	}

	c.setStale(key, false)
}

func (c *RedisAvailabilityCache) isStale(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, stale := c.stale[key]

	return stale
}

func (c *RedisAvailabilityCache) setStale(key string, stale bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if stale {
		c.stale[key] = struct{}{}
		return
	}

	delete(c.stale, key)
}

// goRedis adapta el cliente de go-redis a redisCommands.
type goRedis struct {
	client *redis.Client
}

func (g goRedis) Get(ctx context.Context, key string) (string, bool, error) {
	value, err := g.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}

	if err != nil {
		return "", false, err
	}

	return value, true, nil
}

func (g goRedis) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return g.client.Set(ctx, key, value, ttl).Err()
}

func (g goRedis) Incr(ctx context.Context, key string) (int64, error) {
	return g.client.Incr(ctx, key).Result()
}
