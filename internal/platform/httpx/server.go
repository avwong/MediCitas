package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Checker indica si una dependencia está lista para atender tráfico.
type Checker func(ctx context.Context) error

// RegisterHealth agrega /health (el proceso está vivo) y /ready (sus
// dependencias responden). Ninguna exige autenticación.
func RegisterHealth(router *gin.Engine, ready Checker) {
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	router.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := ready(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
}

// Serve corre el servidor HTTP hasta que ctx se cancela y luego lo apaga de
// forma ordenada, dejando terminar las peticiones en curso.
func Serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)

	go func() {
		logger.Info("http server listening", "addr", addr)

		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}

		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down http server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}
