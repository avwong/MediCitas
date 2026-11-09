// Package auth validates Keycloak access tokens and authorizes requests
// against the permission matrix.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// VerifierConfig configures token validation.
type VerifierConfig struct {
	// IssuerURL must match the iss claim exactly. Keycloak's KC_HOSTNAME fixes
	// it, so tokens carry the same issuer however they were requested.
	IssuerURL string
	// JWKSURL is where the public keys are fetched, reached by service name
	// inside the Compose network.
	JWKSURL string
	// Audience must appear in the aud claim. Keycloak adds it with an
	// audience mapper.
	Audience string
}

// Verifier validates access tokens: signature against the provider's public
// keys, issuer, audience and expiration.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewVerifier returns a verifier. Keys are fetched lazily and cached.
func NewVerifier(ctx context.Context, cfg VerifierConfig) (*Verifier, error) {
	switch {
	case cfg.IssuerURL == "":
		return nil, errors.New("issuer URL is required")
	case cfg.JWKSURL == "":
		return nil, errors.New("JWKS URL is required")
	case cfg.Audience == "":
		return nil, errors.New("audience is required")
	}

	keySet := oidc.NewRemoteKeySet(ctx, cfg.JWKSURL)

	verifier := oidc.NewVerifier(cfg.IssuerURL, keySet, &oidc.Config{
		ClientID: cfg.Audience,
	})

	return &Verifier{verifier: verifier}, nil
}

// Verify validates rawToken and returns its claims.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Claims{}, fmt.Errorf("verify token: %w", err)
	}

	var claims Claims
	if err := token.Claims(&claims); err != nil {
		return Claims{}, fmt.Errorf("decode claims: %w", err)
	}

	return claims, nil
}
