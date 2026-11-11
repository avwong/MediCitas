package audit

import (
	"testing"

	"github.com/avwong/MediCitas/internal/platform/auth"
)

func TestSourceForAcceptsAgendaService(t *testing.T) {
	handler := &Handler{
		producers: map[string]string{
			"agenda-service":     "agenda",
			"expediente-service": "expediente",
		},
	}

	source, ok := handler.sourceFor(auth.Actor{
		Role:     auth.RoleService,
		ClientID: "agenda-service",
	})

	if !ok || source != "agenda" {
		t.Fatalf(
			"source = %q, ok = %v, want agenda,true",
			source,
			ok,
		)
	}
}

func TestSourceForAcceptsExpedienteService(t *testing.T) {
	handler := &Handler{
		producers: map[string]string{
			"agenda-service":     "agenda",
			"expediente-service": "expediente",
		},
	}

	source, ok := handler.sourceFor(auth.Actor{
		Role:     auth.RoleService,
		ClientID: "expediente-service",
	})

	if !ok || source != "expediente" {
		t.Fatalf(
			"source = %q, ok = %v, want expediente,true",
			source,
			ok,
		)
	}
}

func TestSourceForRejectsHumanActor(t *testing.T) {
	handler := &Handler{
		producers: map[string]string{
			"agenda-service": "agenda",
		},
	}

	if _, ok := handler.sourceFor(auth.Actor{
		Role:     auth.RoleClinicAdmin,
		ClientID: "agenda-service",
	}); ok {
		t.Fatal("human actor must not ingest audit events")
	}
}

func TestSourceForRejectsUnknownService(t *testing.T) {
	handler := &Handler{
		producers: map[string]string{
			"agenda-service": "agenda",
		},
	}

	if _, ok := handler.sourceFor(auth.Actor{
		Role:     auth.RoleService,
		ClientID: "unknown-service",
	}); ok {
		t.Fatal("unknown service account must not ingest audit events")
	}
}
