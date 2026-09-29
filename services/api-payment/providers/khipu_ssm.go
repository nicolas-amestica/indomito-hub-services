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

// KhipuDevelopment contiene secretos solo en memoria del backend. Nunca serializar.
type KhipuDevelopment struct {
	Client        *Khipu `json:"-"`
	WebhookSecret string `json:"-"`
}

// LoadKhipuDevelopment rechaza otro stage y parámetros sin cifrado. Esto no
// verifica que la cuenta Khipu sea desarrollador: se debe confirmar en su panel.
func LoadKhipuDevelopment(ctx context.Context, reader ParameterReader, stage string) (KhipuDevelopment, error) {
	var result KhipuDevelopment
	errConfig := errors.New("configuración Khipu DEV incompleta o inválida en SSM")
	if stage != "dev" || reader == nil {
		return result, errConfig
	}
	values := make([]string, 0, 3)
	for _, suffix := range []string{"api-key", "webhook-secret", "receiver-id"} {
		name := "/indomito/dev/payments/khipu/" + suffix
		response, err := reader.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
		if err != nil || response == nil || response.Parameter == nil || response.Parameter.Type != types.ParameterTypeSecureString || response.Parameter.Value == nil {
			return result, errConfig
		}
		value := strings.TrimSpace(*response.Parameter.Value)
		if value == "" || strings.HasPrefix(value, "COMPLETAR_") || value == "placeholder-pending-configuration" {
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
	return KhipuDevelopment{Client: client, WebhookSecret: values[1]}, nil
}
