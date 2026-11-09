// Command expediente runs the clinical-record service. It will hold the
// clinical notes (RF15), the most sensitive data in the system, behind
// database credentials no other service has.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/avwong/MediCitas/internal/expediente"
	"github.com/avwong/MediCitas/internal/platform/bootstrap"
	"github.com/avwong/MediCitas/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "expediente:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	agendaURL := env.Required("AGENDA_URL")

	app, err := bootstrap.New(ctx, "expediente", env)
	if err != nil {
		return err
	}

	expediente.RegisterRoutes(app.API, expediente.Dependencies{
		Logger:    app.Logger,
		AgendaURL: agendaURL,
	})

	return app.Run(ctx)
}
