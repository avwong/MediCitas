package expediente

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgendaClientRelaysUserTokenAndDecodesContext(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			expectedPath :=
				"/internal/appointments/" +
					testAppointmentID +
					"/clinical-note-context"

			if r.URL.Path != expectedPath {
				t.Fatalf(
					"path = %q, want %q",
					r.URL.Path,
					expectedPath,
				)
			}

			if r.Method != http.MethodGet {
				t.Fatalf(
					"method = %q, want GET",
					r.Method,
				)
			}

			if got := r.Header.Get("Authorization"); got != "Bearer raw-user-token" {
				t.Fatalf(
					"authorization = %q, want bearer token",
					got,
				)
			}

			w.Header().Set("Content-Type", "application/json")

			if err := json.NewEncoder(w).Encode(attendedAppointment()); err != nil {
				t.Fatal(err)
			}
		}),
	)
	defer server.Close()

	client := NewAgendaClient(server.URL)

	got, err := client.ClinicalNoteContext(
		context.Background(),
		"raw-user-token",
		testAppointmentID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.AppointmentID != testAppointmentID ||
		got.PatientID != testPatientID ||
		got.ClinicID != testClinicID {
		t.Fatalf("unexpected appointment context: %+v", got)
	}
}
