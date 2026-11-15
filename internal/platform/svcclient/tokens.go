package svcclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
)

// ClientCredentials obtiene y guarda en caché tokens de acceso con el flujo
// client credentials de OAuth 2.0. Los servicios lo usan cuando actúan por
// cuenta propia (aprovisionar, entregar eventos de auditoría) y no en nombre
// de un usuario.
type ClientCredentials struct {
	tokenURL     string
	clientID     string
	clientSecret string
	http         *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewClientCredentials devuelve una fuente de tokens para ese client de
// Keycloak.
func NewClientCredentials(tokenURL, clientID, clientSecret string) *ClientCredentials {
	return &ClientCredentials{
		tokenURL:     tokenURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 5 * time.Second},
	}
}

// Token devuelve un token de acceso válido y pide uno nuevo poco antes de que
// venza el que tiene en caché.
func (s *ClientCredentials) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && time.Now().Before(s.expiry) {
		return s.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.clientID},
		"client_secret": {s.clientSecret},
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.tokenURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: request token: %v", apperr.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return "", fmt.Errorf("%w: token endpoint returned %d", apperr.ErrUnavailable, resp.StatusCode)
	}

	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if body.AccessToken == "" {
		return "", errors.New("token response has no access token")
	}

	lifetime := time.Duration(body.ExpiresIn) * time.Second
	margin := 30 * time.Second
	if lifetime <= 2*margin {
		margin = lifetime / 2
	}

	s.token = body.AccessToken
	s.expiry = time.Now().Add(lifetime - margin)

	return s.token, nil
}
