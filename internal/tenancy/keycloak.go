package tenancy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/auth"
	"github.com/avwong/MediCitas/internal/platform/ids"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

// ClinicAttribute es el atributo del usuario de Keycloak que guarda la
// clínica. Un protocol mapper lo copia al claim clinic_id de cada token de
// acceso.
const ClinicAttribute = "clinic_id"

const maxKeycloakResponse = 1 << 20

// KeycloakAdmin implementa IdentityProvider con la API REST de administración
// de Keycloak. Se autentica como el client tenancy-service, cuya cuenta de
// servicio solo tiene los roles de realm-management necesarios para
// administrar usuarios.
type KeycloakAdmin struct {
	baseURL string
	realm   string
	tokens  *svcclient.ClientCredentials
	http    *http.Client
}

// NewKeycloakAdmin devuelve un cliente de administración para realm en
// baseURL.
func NewKeycloakAdmin(baseURL, realm string, tokens *svcclient.ClientCredentials) *KeycloakAdmin {
	return &KeycloakAdmin{
		baseURL: strings.TrimRight(baseURL, "/"),
		realm:   realm,
		tokens:  tokens,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

type roleRepresentation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateUser crea el usuario y después le asigna el rol de realm. Si el rol no
// se puede asignar, borra el usuario para que no quede una identidad sin rol.
func (k *KeycloakAdmin) CreateUser(ctx context.Context, identity NewIdentity) (string, error) {
	representation := map[string]any{
		"username":      identity.Account.Username,
		"email":         identity.Account.Email,
		"firstName":     identity.Account.FirstName,
		"lastName":      identity.Account.LastName,
		"enabled":       true,
		"emailVerified": true,
		"credentials": []map[string]any{{
			"type":      "password",
			"value":     identity.Account.Password,
			"temporary": false,
		}},
	}

	if identity.ClinicID != "" {
		representation["attributes"] = map[string][]string{
			ClinicAttribute: {identity.ClinicID},
		}
	}

	resp, err := k.do(ctx, http.MethodPost, "/users", representation)
	if err != nil {
		return "", err
	}

	location := resp.Header.Get("Location")
	if err := expectStatus(resp, http.StatusCreated); err != nil {
		if resp.StatusCode == http.StatusConflict {
			return "", apperr.Conflict("username or email already exists")
		}

		return "", err
	}

	subject, ok := ids.Normalize(path.Base(location))
	if !ok {
		return "", fmt.Errorf("%w: keycloak returned an unexpected location %q", apperr.ErrUnavailable, location)
	}

	if err := k.addRealmRoles(ctx, subject, string(identity.Role)); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		if deleteErr := k.DeleteUser(cleanupCtx, subject); deleteErr != nil {
			return "", fmt.Errorf("assign role: %w (cleanup failed: %v)", err, deleteErr)
		}

		return "", fmt.Errorf("assign role: %w", err)
	}

	return subject, nil
}

// DeleteUser elimina un usuario. Borrar uno que no existe también cuenta como
// éxito.
func (k *KeycloakAdmin) DeleteUser(ctx context.Context, subject string) error {
	resp, err := k.do(ctx, http.MethodDelete, "/users/"+url.PathEscape(subject), nil)
	if err != nil {
		return err
	}

	return expectStatus(resp, http.StatusNoContent, http.StatusNotFound)
}

// ReplaceRole deja al usuario con exactamente un rol de dominio.
func (k *KeycloakAdmin) ReplaceRole(ctx context.Context, subject string, role auth.Role) error {
	mappingsPath := "/users/" + url.PathEscape(subject) + "/role-mappings/realm"

	var current []roleRepresentation
	if err := k.getJSON(ctx, mappingsPath, &current); err != nil {
		return err
	}

	domainRoles := auth.DomainRoleNames()
	var toRemove []roleRepresentation
	alreadyAssigned := false

	for _, assigned := range current {
		switch {
		case assigned.Name == string(role):
			alreadyAssigned = true
		case slices.Contains(domainRoles, assigned.Name):
			toRemove = append(toRemove, assigned)
		}
	}

	if !alreadyAssigned {
		if err := k.addRealmRoles(ctx, subject, string(role)); err != nil {
			return err
		}
	}

	if len(toRemove) > 0 {
		resp, err := k.do(ctx, http.MethodDelete, mappingsPath, toRemove)
		if err != nil {
			return err
		}

		if err := expectStatus(resp, http.StatusNoContent); err != nil {
			return err
		}
	}

	return nil
}

// Disable bloquea los inicios de sesión nuevos y cierra las sesiones activas
// del usuario.
func (k *KeycloakAdmin) Disable(ctx context.Context, subject string) error {
	userPath := "/users/" + url.PathEscape(subject)

	// Se devuelve la representación completa para no perder ningún atributo al
	// actualizar.
	var representation map[string]any
	if err := k.getJSON(ctx, userPath, &representation); err != nil {
		return err
	}

	representation["enabled"] = false

	resp, err := k.do(ctx, http.MethodPut, userPath, representation)
	if err != nil {
		return err
	}

	if err := expectStatus(resp, http.StatusNoContent); err != nil {
		return err
	}

	resp, err = k.do(ctx, http.MethodPost, userPath+"/logout", nil)
	if err != nil {
		return err
	}

	return expectStatus(resp, http.StatusNoContent)
}

func (k *KeycloakAdmin) addRealmRoles(ctx context.Context, subject string, names ...string) error {
	roles := make([]roleRepresentation, 0, len(names))

	for _, name := range names {
		var role roleRepresentation
		if err := k.getJSON(ctx, "/roles/"+url.PathEscape(name), &role); err != nil {
			return err
		}

		roles = append(roles, role)
	}

	resp, err := k.do(ctx, http.MethodPost, "/users/"+url.PathEscape(subject)+"/role-mappings/realm", roles)
	if err != nil {
		return err
	}

	return expectStatus(resp, http.StatusNoContent)
}

func (k *KeycloakAdmin) getJSON(ctx context.Context, resourcePath string, out any) error {
	resp, err := k.do(ctx, http.MethodGet, resourcePath, nil)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return expectStatus(resp, http.StatusOK)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxKeycloakResponse)).Decode(out); err != nil {
		return fmt.Errorf("decode keycloak response for %s: %w", resourcePath, err)
	}

	return nil
}

// do envía una petición autenticada a la API de administración del realm.
// Quien llama es responsable de cerrar el cuerpo de la respuesta.
func (k *KeycloakAdmin) do(ctx context.Context, method, resourcePath string, in any) (*http.Response, error) {
	token, err := k.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("encode keycloak request: %w", err)
		}

		body = bytes.NewReader(payload)
	}

	endpoint := k.baseURL + "/admin/realms/" + url.PathEscape(k.realm) + resourcePath

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("build keycloak request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := k.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: keycloak %s %s: %v", apperr.ErrUnavailable, method, resourcePath, err)
	}

	return resp, nil
}

// expectStatus cierra el cuerpo y convierte un código inesperado en un error.
func expectStatus(resp *http.Response, accepted ...int) error {
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxKeycloakResponse))

	if slices.Contains(accepted, resp.StatusCode) {
		return nil
	}

	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: keycloak resource not found", apperr.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%w: keycloak reported a conflict", apperr.ErrConflict)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: keycloak rejected the request", apperr.ErrInvalidInput)
	default:
		return fmt.Errorf("%w: keycloak returned %d", apperr.ErrUnavailable, resp.StatusCode)
	}
}
