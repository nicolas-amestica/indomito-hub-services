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
	"github.com/labstack/echo/v4/middleware"
)

const localShutdownTimeout = 10 * time.Second

// RunLocal levanta el servidor de desarrollo con las rutas implementadas.
func RunLocal(ctx context.Context, registers ...func(*echo.Echo, *App)) error {
	app, err := GetApp(ctx)
	if err != nil {
		return err
	}
	router := echo.New()
	router.HideBanner = true
	router.Use(middleware.Recover(), middleware.RequestID(), middleware.CORS())
	for _, register := range registers {
		register(router, app)
	}
	serverErrors := make(chan error, 1)
	go func() {
		if err := router.Start(":" + app.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-serverErrors:
		return err
	case <-signals:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), localShutdownTimeout)
	defer cancel()
	return router.Shutdown(shutdownCtx)
}
