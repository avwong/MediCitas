// Command agenda ejecuta el servicio de Agenda: catálogo, profesionales,
// pacientes, horarios, disponibilidad y citas de cada clínica.
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
	redisAddr := env.Optional("REDIS_ADDR", "")

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

	// Horarios, disponibilidad y citas (RF5 a RF13). La disponibilidad se
	// sirve desde Redis; sin REDIS_ADDR se calcula siempre contra la base,
	// que es más lento pero igual de correcto.
	var cache agenda.AvailabilityCache
	if redisAddr == "" {
		app.Logger.Warn("REDIS_ADDR is not set; availability is computed from the database on every request")
		cache = agenda.NewNoCache()
	} else {
		cache = agenda.NewRedisAvailabilityCache(redisAddr, app.Logger)
	}

	scheduling := agenda.NewScheduling(
		agenda.NewPostgresSchedulingStore(),
		app.Tx,
		app.DB,
		cache,
	)
	schedulingHandler := agenda.NewSchedulingHandler(scheduling, app.Logger)

	// El catálogo avisa cuando cambia algo que afecta a la disponibilidad en
	// caché: el estado de un profesional o los servicios que atiende.
	catalog.ObserveAgendas(scheduling)

	// Historial del paciente (RF16), agenda del día (RF20) y conteo de citas
	// por estado (RF21). Solo leen, así que no escriben en el outbox de
	// auditoría.
	reports := agenda.NewReports(agenda.NewPostgresReportStore(), app.Tx)
	reportHandler := agenda.NewReportHandler(reports, app.Logger)

	internal := app.Router.Group(
		"/internal",
		app.Verifier.Authenticate(),
	)

	handler.RegisterRoutes(app.API)
	patientHandler.RegisterRoutes(app.API)
	schedulingHandler.RegisterRoutes(app.API)
	reportHandler.RegisterRoutes(app.API)
	handler.RegisterInternalRoutes(internal, tenancyClientID)
	schedulingHandler.RegisterInternalRoutes(internal)


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
