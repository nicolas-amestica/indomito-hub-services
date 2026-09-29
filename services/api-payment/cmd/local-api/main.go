// Servidor de simulación local; no incluye rutas de producción ni acceso a bancos.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions"
)

func main() {
	directory := flag.String("data-dir", "tmp/payment-demo", "Directorio exclusivo para datos ficticios")
	flag.Parse()
	if err := os.MkdirAll(*directory, 0700); err != nil {
		slog.Error("No se pudo preparar el directorio")
		os.Exit(1)
	}
	keyPath := filepath.Join(*directory, "admin.key")
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			slog.Error("No se pudo generar credencial")
			os.Exit(1)
		}
		key = []byte(hex.EncodeToString(raw))
		err = os.WriteFile(keyPath, key, 0600)
	}
	if err != nil {
		slog.Error("No se pudo preparar credencial")
		os.Exit(1)
	}
	app, err := functions.Open(*directory, string(key))
	if err != nil {
		slog.Error("No se pudo cargar el entorno local")
		os.Exit(1)
	}
	server := http.Server{Addr: "127.0.0.1:8095", Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	slog.Info("Pagos locales disponibles", "address", server.Addr, "adminKeyFile", keyPath)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Servidor detenido")
		os.Exit(1)
	}
}
