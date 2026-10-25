package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Claims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	AuthorizedParty   string `json:"azp"`

	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

type Verifier struct {
	tokenVerifier *oidc.IDTokenVerifier
	clientID      string
}

func NewVerifier(issuerURL, jwksURL, clientID string) (*Verifier, error) {
	if issuerURL == "" {
		return nil, errors.New("KEYCLOAK_ISSUER_URL is required")
	}

	if jwksURL == "" {
		return nil, errors.New("KEYCLOAK_JWKS_URL is required")
	}

	if clientID == "" {
		return nil, errors.New("KEYCLOAK_CLIENT_ID is required")
	}

	keySet := oidc.NewRemoteKeySet(context.Background(), jwksURL)

	tokenVerifier := oidc.NewVerifier(
		issuerURL,
		keySet,
		&oidc.Config{
			SkipClientIDCheck: true,
		},
	)

	return &Verifier{
		tokenVerifier: tokenVerifier,
		clientID:      clientID,
	}, nil
}

func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	token, err := v.tokenVerifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}

	var claims Claims

	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	if claims.AuthorizedParty != v.clientID {
		return nil, errors.New("token was not issued for the expected client")
	}

	if claims.Subject == "" {
		return nil, errors.New("token has no subject")
	}

	return &claims, nil
}
