package auth

import (
	"errors"
	"testing"
)

const clinicA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func claimsWith(subject, clinicID string, roles ...string) Claims {
	var claims Claims
	claims.Subject = subject
	claims.ClinicID = clinicID
	claims.AuthorizedParty = "medicitas-cli"
	claims.RealmAccess.Roles = roles

	return claims
}

func TestNewActor(t *testing.T) {
	tests := []struct {
		name       string
		claims     Claims
		wantErr    error
		wantRole   Role
		wantClinic string
	}{
		{
			name:       "clinic role with clinic",
			claims:     claimsWith("user-1", clinicA, "reception", "offline_access"),
			wantRole:   RoleReception,
			wantClinic: clinicA,
		},
		{
			name:       "clinic id is normalized",
			claims:     claimsWith("user-1", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "practitioner"),
			wantRole:   RolePractitioner,
			wantClinic: clinicA,
		},
		{
			name:       "platform admin never carries a clinic",
			claims:     claimsWith("admin", clinicA, "platform_admin"),
			wantRole:   RolePlatformAdmin,
			wantClinic: "",
		},
		{
			name:     "service account",
			claims:   claimsWith("svc", "", "service", "default-roles-medicitas"),
			wantRole: RoleService,
		},
		{
			name:    "clinic role without clinic",
			claims:  claimsWith("user-1", "", "clinic_admin"),
			wantErr: ErrNoClinic,
		},
		{
			name:    "clinic role with malformed clinic",
			claims:  claimsWith("user-1", "clinic-demo", "clinic_admin"),
			wantErr: ErrNoClinic,
		},
		{
			name:    "no domain role",
			claims:  claimsWith("user-1", clinicA, "offline_access"),
			wantErr: ErrNoDomainRole,
		},
		{
			name:    "two domain roles",
			claims:  claimsWith("user-1", clinicA, "reception", "clinic_admin"),
			wantErr: ErrAmbiguousRole,
		},
		{
			name:    "no subject",
			claims:  claimsWith("", clinicA, "reception"),
			wantErr: ErrNoSubject,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actor, err := NewActor(tt.claims, "raw-token")

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if actor.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", actor.Role, tt.wantRole)
			}

			if actor.ClinicID != tt.wantClinic {
				t.Errorf("clinic = %q, want %q", actor.ClinicID, tt.wantClinic)
			}

			if actor.Token != "raw-token" {
				t.Errorf("token was not kept for relaying")
			}
		})
	}
}

func TestIsService(t *testing.T) {
	actor := Actor{Role: RoleService, ClientID: "tenancy-service"}

	if !actor.IsService("tenancy-service") {
		t.Error("tenancy service account should be recognized")
	}

	if actor.IsService("agenda-service") {
		t.Error("a service account must not pass as another client")
	}

	human := Actor{Role: RoleClinicAdmin, ClientID: "tenancy-service"}
	if human.IsService("tenancy-service") {
		t.Error("a human user must never pass as a service")
	}
}
