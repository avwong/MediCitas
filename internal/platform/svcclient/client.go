// Package svcclient es el cliente HTTP con el que los servicios se llaman
// entre sí y obtienen tokens de client credentials en Keycloak.
package svcclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/avwong/MediCitas/internal/platform/apperr"
	"github.com/avwong/MediCitas/internal/platform/requestid"
)

const maxResponseBytes = 1 << 20

// Client llama a otro servicio de MediCitas. Propaga el ID de la petición y
// traduce el código de la respuesta a un error de dominio.
type Client struct {
	baseURL string
	http    *http.Client
}

// New devuelve un cliente para el servicio en baseURL con un timeout corto:
// una dependencia lenta debe fallar rápido en lugar de retener la petición de
// quien llama.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Do envía una petición JSON autenticada con bearerToken y decodifica la
// respuesta JSON en out (si out no es nil).
func (c *Client) Do(
	ctx context.Context,
	method string,
	path string,
	bearerToken string,
	in any,
	out any,
) error {
	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request to %s: %w", path, err)
		}

		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request to %s: %w", path, err)
	}

	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	if id := requestid.FromContext(ctx); id != "" {
		req.Header.Set(requestid.Header, id)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %v", apperr.ErrUnavailable, method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return statusError(method, path, resp.StatusCode)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}

	return nil
}

func statusError(method, path string, status int) error {
	var kind error

	switch status {
	case http.StatusBadRequest:
		kind = apperr.ErrInvalidInput
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = apperr.ErrForbidden
	case http.StatusNotFound:
		kind = apperr.ErrNotFound
	case http.StatusConflict:
		kind = apperr.ErrConflict
	default:
		kind = apperr.ErrUnavailable
	}

	return fmt.Errorf("%w: %s %s returned %d", kind, method, path, status)
}
