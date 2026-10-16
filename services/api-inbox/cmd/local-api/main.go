package main

import (
	"context"
	"os"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
	subscription "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/crear-suscripcion-v1"
	deleteconversation "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/eliminar-conversacion-v1"
	markallread "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/iniciar-marcar-leidas-v1"
	recalculateunread "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/iniciar-recalcular-no-leidas-v1"
	markunread "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/marcar-no-leida-v1"
	detail "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/obtener-conversacion-v1"
	endpoint "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/obtener-conversaciones-v1"
	activeoperation "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/obtener-operacion-activa-v1"
	operation "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/obtener-operacion-v1"
	receivewebhook "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/recibir-webhook-whatsapp-v1"
	verifywebhook "ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions/verificar-webhook-whatsapp-v1"
)

func main() {
	log := logger.New("api-inbox-local", "startup", "local")
	if err := functions.RunLocal(context.Background(), endpoint.Register, detail.Register, markunread.Register, deleteconversation.Register, markallread.Register, recalculateunread.Register, subscription.Register, activeoperation.Register, operation.Register, verifywebhook.Register, receivewebhook.Register); err != nil {
		log.Error("localServerError", zap.Error(err))
		_ = log.Sync()
		os.Exit(1)
	}
	_ = log.Sync()
}
