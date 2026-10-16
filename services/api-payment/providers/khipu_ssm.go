package providers

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ParameterReader permite resolver secretos sin acoplar el dominio a AWS.
type ParameterReader interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

func readSecureParameter(ctx context.Context, reader ParameterReader, name string) (string, error) {
	response, err := reader.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil || response == nil || response.Parameter == nil || response.Parameter.Type != types.ParameterTypeSecureString || response.Parameter.Value == nil {
		return "", ErrVerification
	}
	value := strings.TrimSpace(*response.Parameter.Value)
	if value == "" || strings.HasPrefix(value, "COMPLETAR_") || value == "placeholder-pending-configuration" {
		return "", ErrVerification
	}
	return value, nil
}

// KhipuCredentials contiene secretos solo en memoria del backend. Nunca serializar.
type KhipuCredentials struct {
	Client        *Khipu `json:"-"`
	WebhookSecret string `json:"-"`
}

// LoadKhipu carga credenciales aisladas por ambiente y exige SecureString.
// En DEV, la validación adicional de DemoBank se mantiene en el flujo de laboratorio.
func LoadKhipu(ctx context.Context, reader ParameterReader, stage string) (KhipuCredentials, error) {
	var result KhipuCredentials
	errConfig := errors.New("configuración Khipu incompleta o inválida en SSM")
	if (stage != "dev" && stage != "prd") || reader == nil {
		return result, errConfig
	}
	values := make([]string, 0, 3)
	for _, suffix := range []string{"api-key", "webhook-secret", "receiver-id"} {
		name := "/indomito/" + stage + "/payments/khipu/" + suffix
		value, err := readSecureParameter(ctx, reader, name)
		if err != nil {
			return result, errConfig
		}
		values = append(values, value)
	}
	receiverID, err := strconv.ParseInt(values[2], 10, 64)
	if err != nil || receiverID <= 0 {
		return result, errConfig
	}
	client, err := NewKhipu(values[0], receiverID)
	if err != nil {
		return result, errConfig
	}
	return KhipuCredentials{Client: client, WebhookSecret: values[1]}, nil
}

// LoadKhipuDevelopment conserva el contrato del laboratorio y nunca admite PRD.
func LoadKhipuDevelopment(ctx context.Context, reader ParameterReader, stage string) (KhipuCredentials, error) {
	if stage != "dev" {
		return KhipuCredentials{}, errors.New("configuración Khipu DEV incompleta o inválida en SSM")
	}
	return LoadKhipu(ctx, reader, stage)
}
