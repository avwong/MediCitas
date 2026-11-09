// Command audit runs the audit service: the append-only log of who read or
// changed clinical notes and patient contact data (RF18, RF19).
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "audit:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	app, err := bootstrap.New(ctx, "audit", env)
	if err != nil {
		return err
	}

	audit.RegisterRoutes(app.API, app.Router.Group("/internal", app.Verifier.Authenticate()))

	return app.Run(ctx)
}
