// Command agenda runs the agenda service: catalog, practitioners and, as they
// are implemented, schedules, patients, availability and appointments.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/avwong/MediCitas/internal/agenda"
	"github.com/avwong/MediCitas/internal/platform/bootstrap"
	"github.com/avwong/MediCitas/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "agenda:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env := &config.Loader{}

	tenancyURL := env.Required("TENANCY_URL")
	tenancyClientID := env.Optional("TENANCY_SERVICE_CLIENT_ID", "tenancy-service")

	app, err := bootstrap.New(ctx, "agenda", env)
	if err != nil {
		return err
	}

	store := agenda.NewPostgresStore(app.DB, app.Tx)
	catalog := agenda.NewCatalog(store, agenda.NewTenancyDirectory(tenancyURL))
	handler := agenda.NewHandler(catalog, app.Logger)

	handler.RegisterRoutes(app.API)
	handler.RegisterInternalRoutes(
		app.Router.Group("/internal", app.Verifier.Authenticate()),
		tenancyClientID,
	)

	return app.Run(ctx)
}
