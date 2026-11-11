package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/avwong/MediCitas/internal/expediente"
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
		fmt.Fprintln(os.Stderr, "expediente:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	agendaURL := env.Required("AGENDA_URL")

	auditURL := env.Required("AUDIT_URL")
	tokenURL := env.Required("OIDC_TOKEN_URL")
	outboxClientID := env.Required("OUTBOX_CLIENT_ID")
	outboxClientSecret := env.Required("OUTBOX_CLIENT_SECRET")

	app, err := bootstrap.New(ctx, "expediente", env)
	if err != nil {
		return err
	}

	expediente.RegisterRoutes(
		app.API,
		expediente.Dependencies{
			Logger:    app.Logger,
			AgendaURL: agendaURL,
		},
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
