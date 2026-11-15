// Command agenda ejecuta el servicio de Agenda: catálogo, profesionales y
// pacientes de cada clínica.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/avwong/MediCitas/internal/agenda"
	"github.com/avwong/MediCitas/internal/platform/bootstrap"
	"github.com/avwong/MediCitas/internal/platform/config"
	"github.com/avwong/MediCitas/internal/platform/outbox"
	"github.com/avwong/MediCitas/internal/platform/svcclient"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "agenda:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	tenancyURL := env.Required("TENANCY_URL")
	tenancyClientID := env.Optional(
		"TENANCY_SERVICE_CLIENT_ID",
		"tenancy-service",
	)

	auditURL := env.Required("AUDIT_URL")
	tokenURL := env.Required("OIDC_TOKEN_URL")
	outboxClientID := env.Required("OUTBOX_CLIENT_ID")
	outboxClientSecret := env.Required("OUTBOX_CLIENT_SECRET")

	app, err := bootstrap.New(ctx, "agenda", env)
	if err != nil {
		return err
	}

	store := agenda.NewPostgresStore(app.DB, app.Tx)
	catalog := agenda.NewCatalog(
		store,
		agenda.NewTenancyDirectory(tenancyURL),
	)
	handler := agenda.NewHandler(catalog, app.Logger)

	// Pacientes (RF14). El Writer deja los eventos de auditoría de contacto
	// (RF18) en el outbox de Agenda, dentro de la misma transacción que la
	// operación sobre el paciente.
	patients := agenda.NewPatients(
		agenda.NewPostgresPatientStore(),
		app.Tx,
		outbox.NewWriter("agenda"),
	)
	patientHandler := agenda.NewPatientHandler(patients, app.Logger)

	handler.RegisterRoutes(app.API)
	patientHandler.RegisterRoutes(app.API)
	handler.RegisterInternalRoutes(
		app.Router.Group(
			"/internal",
			app.Verifier.Authenticate(),
		),
		tenancyClientID,
	)

	tokenSource := svcclient.NewClientCredentials(
		tokenURL,
		outboxClientID,
		outboxClientSecret,
	)

	relay := outbox.NewRelay(
		outbox.NewPostgresStore(app.DB),
		outbox.NewHTTPSink(auditURL, tokenSource),
		app.Logger,
		2*time.Second,
		100,
	)

	go relay.Run(ctx)

	return app.Run(ctx)
}
