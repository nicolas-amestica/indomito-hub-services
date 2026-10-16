package collection

import (
	"errors"
	"os"
)

func currentStage() (string, error) {
	stage := os.Getenv("APP_STAGE")
	if stage != "dev" && stage != "prd" {
		return "", errors.New("ambiente de pagos inválido")
	}
	return stage, nil
}

func stageParameter(stage, suffix string) string {
	return "/indomito/" + stage + "/" + suffix
}
