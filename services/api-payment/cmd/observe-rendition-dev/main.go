// Herramienta manual DEV. No se registra como endpoint o función de producción.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/renditionobserve"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := renditionobserve.RunDEV(ctx)
	if err != nil {
		// No volcar errores de SDK ni datos de configuración a logs.
		zap.NewExample().Error("Observación DEV no completada; revisar acceso, vigencia o evidencia")
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(1)
	}
}
