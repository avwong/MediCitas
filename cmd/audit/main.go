package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/avwong/MediCitas/internal/audit"
	"github.com/avwong/MediCitas/internal/platform/bootstrap"
	"github.com/avwong/MediCitas/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "audit:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	agendaClientID := env.Optional(
		"AGENDA_SERVICE_CLIENT_ID",
		"agenda-service",
	)

	expedienteClientID := env.Optional(
		"EXPEDIENTE_SERVICE_CLIENT_ID",
		"expediente-service",
	)

	app, err := bootstrap.New(ctx, "audit", env)
	if err != nil {
		return err
	}

	store := audit.NewPostgresStore(app.DB)
	service := audit.NewService(store)

	handler := audit.NewHandler(
		service,
		app.Logger,
		map[string]string{
			agendaClientID:     "agenda",
			expedienteClientID: "expediente",
		},
	)

	handler.RegisterRoutes(
		app.API,
		app.Router.Group(
			"/internal",
			app.Verifier.Authenticate(),
		),
	)

	return app.Run(ctx)
}
