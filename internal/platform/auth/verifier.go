// Package auth valida los tokens de acceso de Keycloak y autoriza las
// peticiones según la matriz de permisos.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// VerifierConfig configura la validación de tokens.
type VerifierConfig struct {
	// IssuerURL debe coincidir exactamente con el claim iss. KC_HOSTNAME lo
	// fija en Keycloak, así que los tokens traen el mismo emisor sin importar
	// desde dónde se pidieron.
	IssuerURL string
	// JWKSURL es de donde se obtienen las llaves públicas. Se alcanza por
	// nombre de servicio dentro de la red de Compose.
	JWKSURL string
	// Audience debe aparecer en el claim aud. Keycloak lo agrega con un
	// audience mapper.
	Audience string
}

// Verifier valida tokens de acceso: firma contra las llaves públicas del
// proveedor, emisor, audiencia y expiración.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewVerifier devuelve un verificador. Las llaves se piden la primera vez que
// hacen falta y quedan en caché.
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

// Verify valida rawToken y devuelve sus claims.
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
