package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	demoClinicID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	northClinicID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	demoServiceID  = "d1d1d1d1-0000-4000-8000-000000000001"
	northServiceID = "d2d2d2d2-0000-4000-8000-000000000001"

	demoAdminUserID  = "22222222-aaaa-4222-8222-222222222222"
	northAdminUserID = "55555555-aaaa-4555-8555-555555555555"

	unknownNoteID = "99999999-9999-4999-8999-999999999999"
)

type checker struct {
	apiURL       string
	keycloakURL  string
	agendaSecret string
	http         *http.Client
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

type meResponse struct {
	ClinicID string `json:"clinic_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func main() {
	agendaSecret := strings.TrimSpace(os.Getenv("AGENDA_CLIENT_SECRET"))
	if agendaSecret == "" {
		fmt.Fprintln(os.Stderr, "FAIL: AGENDA_CLIENT_SECRET is required")
		os.Exit(1)
	}

	c := &checker{
		apiURL:       envOr("API_URL", "http://proxy:8080"),
		keycloakURL:  envOr("KEYCLOAK_URL", "http://keycloak:8080"),
		agendaSecret: agendaSecret,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := c.run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("All P1 API integration checks passed.")
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return strings.TrimRight(value, "/")
}

func (c *checker) run(ctx context.Context) error {
	demoAdmin, err := c.userToken(ctx, "clinic.admin", "Clinic123!")
	if err != nil {
		return err
	}

	northAdmin, err := c.userToken(ctx, "norte.admin", "Norte123!")
	if err != nil {
		return err
	}

	reception, err := c.userToken(ctx, "reception", "Reception123!")
	if err != nil {
		return err
	}

	practitioner, err := c.userToken(
		ctx,
		"practitioner",
		"Practitioner123!",
	)
	if err != nil {
		return err
	}

	agendaService, err := c.serviceToken(
		ctx,
		"agenda-service",
		c.agendaSecret,
	)
	if err != nil {
		return err
	}

	tests := []struct {
		name string
		fn   func(context.Context) error
	}{
		{
			name: "missing bearer token is rejected",
			fn: func(ctx context.Context) error {
				return c.expectStatus(ctx, http.MethodGet, "/api/v1/me", "", nil, 401)
			},
		},
		{
			name: "invalid bearer token is rejected",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/me",
					"not-a-valid-jwt",
					nil,
					401,
				)
			},
		},
		{
			name: "signed clinic claim ignores forged clinic header",
			fn: func(ctx context.Context) error {
				status, body, err := c.request(
					ctx,
					http.MethodGet,
					"/api/v1/me?clinic_id="+northClinicID,
					demoAdmin,
					map[string]string{
						"X-Clinic-Id": northClinicID,
					},
				)
				if err != nil {
					return err
				}

				if status != http.StatusOK {
					return statusError(status, body, http.StatusOK)
				}

				var me meResponse
				if err := json.Unmarshal(body, &me); err != nil {
					return fmt.Errorf("decode /me response: %w", err)
				}

				if me.ClinicID != demoClinicID {
					return fmt.Errorf(
						"clinic_id = %q, want signed clinic %q",
						me.ClinicID,
						demoClinicID,
					)
				}

				return nil
			},
		},
		{
			name: "RF2 rejects reception role",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/users",
					reception,
					nil,
					403,
				)
			},
		},
		{
			name: "RF19 rejects practitioner role",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/audit",
					practitioner,
					nil,
					403,
				)
			},
		},
		{
			name: "RF19 accepts clinic administrator",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/audit",
					demoAdmin,
					nil,
					200,
				)
			},
		},
		{
			name: "proxy hides internal routes even from service account",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodPost,
					"/internal/audit/events",
					agendaService,
					nil,
					404,
				)
			},
		},
		{
			name: "Agenda returns own-tenant service",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/services/"+demoServiceID,
					demoAdmin,
					nil,
					200,
				)
			},
		},
		{
			name: "Agenda blocks cross-tenant service despite forged clinic",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/services/"+northServiceID+
						"?clinic_id="+northClinicID,
					demoAdmin,
					map[string]string{
						"X-Clinic-Id": northClinicID,
					},
					404,
				)
			},
		},
		{
			name: "Agenda returns northern tenant service to northern admin",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/services/"+northServiceID,
					northAdmin,
					nil,
					200,
				)
			},
		},
		{
			name: "Agenda hides demo service from northern tenant",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/services/"+demoServiceID,
					northAdmin,
					nil,
					404,
				)
			},
		},
		{
			name: "Tenancy returns own-tenant user",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/users/"+demoAdminUserID,
					demoAdmin,
					nil,
					200,
				)
			},
		},
		{
			name: "Tenancy hides northern user from demo clinic",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/users/"+northAdminUserID,
					demoAdmin,
					nil,
					404,
				)
			},
		},
		{
			name: "Tenancy returns own northern user",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/users/"+northAdminUserID,
					northAdmin,
					nil,
					200,
				)
			},
		},
		{
			name: "Tenancy hides demo user from northern clinic",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/users/"+demoAdminUserID,
					northAdmin,
					nil,
					404,
				)
			},
		},
		{
			name: "Expediente rejects reception",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/clinical-notes/"+unknownNoteID,
					reception,
					nil,
					403,
				)
			},
		},
		{
			name: "Expediente rejects clinic administrator",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/clinical-notes/"+unknownNoteID,
					demoAdmin,
					nil,
					403,
				)
			},
		},
		{
			name: "Expediente lets practitioner reach owned-note lookup",
			fn: func(ctx context.Context) error {
				return c.expectStatus(
					ctx,
					http.MethodGet,
					"/api/v1/clinical-notes/"+unknownNoteID,
					practitioner,
					nil,
					404,
				)
			},
		},
	}

	for _, test := range tests {
		if err := test.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", test.name, err)
		}

		fmt.Println("PASS:", test.name)
	}

	return nil
}

func (c *checker) userToken(
	ctx context.Context,
	username string,
	password string,
) (string, error) {
	return c.token(ctx, url.Values{
		"grant_type": {"password"},
		"client_id":  {"medicitas-cli"},
		"username":   {username},
		"password":   {password},
	})
}

func (c *checker) serviceToken(
	ctx context.Context,
	clientID string,
	secret string,
) (string, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {secret},
	})
}

func (c *checker) token(
	ctx context.Context,
	form url.Values,
) (string, error) {
	var lastErr error

	for attempt := 0; attempt < 20; attempt++ {
		token, err := c.tokenOnce(ctx, form)
		if err == nil {
			return token, nil
		}

		lastErr = err

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}

	return "", fmt.Errorf("obtain Keycloak token: %w", lastErr)
}

func (c *checker) tokenOnce(
	ctx context.Context,
	form url.Values,
) (string, error) {
	endpoint := c.keycloakURL +
		"/realms/medicitas/protocol/openid-connect/token"

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"token endpoint returned %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return "", err
	}

	if strings.TrimSpace(token.AccessToken) == "" {
		return "", fmt.Errorf("token endpoint returned no access_token")
	}

	return token.AccessToken, nil
}

func (c *checker) expectStatus(
	ctx context.Context,
	method string,
	path string,
	token string,
	headers map[string]string,
	expected int,
) error {
	status, body, err := c.request(
		ctx,
		method,
		path,
		token,
		headers,
	)
	if err != nil {
		return err
	}

	if status != expected {
		return statusError(status, body, expected)
	}

	return nil
}

func (c *checker) request(
	ctx context.Context,
	method string,
	path string,
	token string,
	headers map[string]string,
) (int, []byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		method,
		c.apiURL+path,
		nil,
	)
	if err != nil {
		return 0, nil, err
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}

	return resp.StatusCode, body, nil
}

func statusError(
	status int,
	body []byte,
	expected int,
) error {
	return fmt.Errorf(
		"status = %d, want %d, body = %s",
		status,
		expected,
		strings.TrimSpace(string(body)),
	)
}
