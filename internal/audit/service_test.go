package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/outbox"
)

type fakeStore struct {
	inserted []outbox.Event

	listClinic string
	listFilter Filter
	listCalls  int
}

func (f *fakeStore) Insert(
	_ context.Context,
	event outbox.Event,
) error {
	f.inserted = append(f.inserted, event)
	return nil
}

func (f *fakeStore) List(
	_ context.Context,
	clinicID string,
	filter Filter,
) ([]Entry, error) {
	f.listCalls++
	f.listClinic = clinicID
	f.listFilter = filter
	return []Entry{}, nil
}

func testAuditEvent() outbox.Event {
	return outbox.Event{
		EventID:       "11111111-1111-4111-8111-111111111111",
		ClinicID:      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		ActorSubject:  "user-subject",
		ActorUsername: "doctor",
		Action:        outbox.ActionPatientContactRead,
		PatientID:     "22222222-2222-4222-8222-222222222222",
		ResourceID:    "22222222-2222-4222-8222-222222222222",
		OccurredAt:    time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC),
	}
}

func TestIngestDerivesTrustedSource(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	event := testAuditEvent()
	event.SourceService = "forged"

	if err := service.Ingest(
		context.Background(),
		event,
		"agenda",
	); err != nil {
		t.Fatal(err)
	}

	if len(store.inserted) != 1 {
		t.Fatalf("inserted %d events, want 1", len(store.inserted))
	}

	if store.inserted[0].SourceService != "agenda" {
		t.Fatalf(
			"source = %q, want agenda",
			store.inserted[0].SourceService,
		)
	}
}

func TestIngestRejectsActionFromWrongProducer(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	event := testAuditEvent()
	event.Action = outbox.ActionClinicalNoteRead

	err := service.Ingest(
		context.Background(),
		event,
		"agenda",
	)

	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}
}

func TestListScopesToActorClinic(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	actor := auth.Actor{
		Subject:  "admin-subject",
		ClinicID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Role:     auth.RoleClinicAdmin,
	}

	if _, err := service.List(
		context.Background(),
		actor,
		Filter{},
	); err != nil {
		t.Fatal(err)
	}

	if store.listClinic != actor.ClinicID {
		t.Fatalf(
			"clinic = %q, want %q",
			store.listClinic,
			actor.ClinicID,
		)
	}

	if store.listFilter.Limit != 100 {
		t.Fatalf(
			"default limit = %d, want 100",
			store.listFilter.Limit,
		)
	}
}

func TestListRejectsReception(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	actor := auth.Actor{
		Subject:  "reception-subject",
		ClinicID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Role:     auth.RoleReception,
	}

	_, err := service.List(
		context.Background(),
		actor,
		Filter{},
	)

	if !errors.Is(err, apperr.ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}

	if store.listCalls != 0 {
		t.Fatal("store must not be queried for reception")
	}
}

func TestListRejectsInvalidPatientFilter(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	actor := auth.Actor{
		ClinicID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Role:     auth.RoleClinicAdmin,
	}

	_, err := service.List(
		context.Background(),
		actor,
		Filter{PatientID: "bad-id"},
	)

	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}
}

func TestListRejectsInvertedDateRange(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	actor := auth.Actor{
		ClinicID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Role:     auth.RoleClinicAdmin,
	}

	from := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

	_, err := service.List(
		context.Background(),
		actor,
		Filter{
			From: &from,
			To:   &to,
		},
	)

	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}
}

func TestListRejectsInvalidLimit(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)

	actor := auth.Actor{
		ClinicID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Role:     auth.RoleClinicAdmin,
	}

	_, err := service.List(
		context.Background(),
		actor,
		Filter{Limit: 501},
	)

	if !errors.Is(err, apperr.ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}
}
