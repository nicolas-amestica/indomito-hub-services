package functions

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

var (
	Version   = "dev"
	Commit    = ""
	BuildTime = ""
)

// RegisterFunc defines the signature for endpoint route registration.
type RegisterFunc func(e *echo.Echo, logger *zap.Logger)

// RunLocal starts the local Echo server for development.
func RunLocal(ctx context.Context, registers ...RegisterFunc) error {
	_, err := GetApp(ctx)
	if err != nil {
		return err
	}

	log := GetLogger()

	e := echo.New()
	e.HideBanner = true
	e.Use(echomiddleware.Recover())
	e.Use(echomiddleware.RequestID())
	e.Use(echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	e.GET("/", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"message": "API funcionando correctamente!",
			"version": Version,
			"commit":  Commit,
			"built":   BuildTime,
		})
	})
	e.GET("/favicon.ico", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	for _, register := range registers {
		register(e, log)
	}

	defer log.Sync()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		if err := e.Start(":" + port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server_start_error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("server_shutting_down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Info("server_shutdown_ok")
	return nil
}
