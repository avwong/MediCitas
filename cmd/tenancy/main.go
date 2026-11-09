// Command tenancy runs the tenancy service: clinics and their users.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/avwong/MediCitas/internal/platform/bootstrap"
	"github.com/avwong/MediCitas/internal/platform/config"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
	"github.com/avwong/MediCitas/internal/tenancy"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "tenancy:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	keycloakURL := strings.TrimRight(env.Required("KEYCLOAK_INTERNAL_URL"), "/")
	realm := env.Optional("KEYCLOAK_REALM", "medicitas")
	clientID := env.Required("TENANCY_CLIENT_ID")
	clientSecret := env.Required("TENANCY_CLIENT_SECRET")
	agendaURL := env.Required("AGENDA_URL")

	app, err := bootstrap.New(ctx, "tenancy", env)
	if err != nil {
		return err
	}

	tokens := svcclient.NewClientCredentials(
		keycloakURL+"/realms/"+realm+"/protocol/openid-connect/token",
		clientID,
		clientSecret,
	)

	service := tenancy.NewService(
		tenancy.NewPostgresStore(app.DB, app.Tx),
		tenancy.NewKeycloakAdmin(keycloakURL, realm, tokens),
		tenancy.NewAgendaClient(agendaURL, tokens),
		app.Logger,
	)

	tenancy.NewHandler(service, app.Logger).RegisterRoutes(app.API)

	return app.Run(ctx)
}
