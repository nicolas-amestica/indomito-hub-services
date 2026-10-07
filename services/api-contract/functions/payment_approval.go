package functions

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/libs/paymentaccess"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

// PaymentAccessConfig se obtiene de SSM; el secreto nunca se persiste en el contrato.
type PaymentAccessConfig struct {
	URL          string
	LookupSecret string
}

type parameterReader interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

func loadPaymentAccess(ctx context.Context, client parameterReader, stage string) (PaymentAccessConfig, error) {
	var config PaymentAccessConfig
	for suffix, target := range map[string]*string{"portal-url": &config.URL, "lookup-secret": &config.LookupSecret} {
		result, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/" + stage + "/payments/" + suffix), WithDecryption: aws.Bool(true)})
		if err != nil {
			return config, errors.New("no se pudo cargar la configuración de acceso a pagos")
		}
		if result.Parameter == nil || result.Parameter.Value == nil {
			return config, errors.New("configuración de acceso a pagos incompleta")
		}
		*target = *result.Parameter.Value
	}
	return config, config.validate()
}

func (c PaymentAccessConfig) validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(c.LookupSecret) < 32 {
		return errors.New("configuración de acceso a pagos inválida")
	}
	return nil
}

// Código de 30 bits: ayuda a ubicar el viaje; NO es prueba de identidad ni reemplaza rate limiting.
func newTripCode(source io.Reader) (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ" // 32 símbolos: 256 es múltiplo de 32, sin sesgo.
	var code [6]byte
	for i := 0; i < len(code); {
		var b [1]byte
		if _, err := io.ReadFull(source, b[:]); err != nil {
			return "", err
		}
		code[i] = alphabet[int(b[0])%len(alphabet)]
		i++
	}
	return string(code[:]), nil
}

func issuePaymentPortal(config PaymentAccessConfig) (*domain.PaymentPortal, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	code, err := newTripCode(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &domain.PaymentPortal{URL: strings.TrimRight(config.URL, "/"), TripCode: code}, nil
}

// paymentApprovalWrites se agrega a la misma transacción del contrato y su auditoría.
// La unicidad del código es condicional y global. No crea deudas ni acredita ingresos.
func paymentApprovalWrites(table string, item domain.Item, config PaymentAccessConfig) ([]types.TransactWriteItem, error) {
	portal := item.Content.PaymentPortal
	if table == "" || portal == nil || len(portal.TripCode) != 6 || item.Status != domain.StatusApproved || item.PDFDocument == nil {
		return nil, errors.New("aprobación de pagos incompleta")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	codeKey, err := paymentaccess.CodeKey(config.LookupSecret, portal.TripCode)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{
		{"pk": codeKey, "sk": "META", "tripId": item.ID, "codeVersion": 1, "status": "RESERVED_APPROVED"},
		{"pk": "TRIP#" + item.ID, "sk": "APPROVAL", "schemaVersion": 1, "type": "CONTRACT_APPROVED", "contractId": item.ID, "contractVersion": item.Version, "pdfSHA256": item.PDFDocument.SHA256, "policyVersion": item.Content.Payments.Conditions.RefundPolicyVersion, "codeKey": codeKey, "tripCode": portal.TripCode, "approvedAt": item.ApprovedAt, "approvedBy": item.ApprovedBy, "status": "PENDING_SETUP"},
	}
	writes := make([]types.TransactWriteItem, 0, len(rows))
	for _, row := range rows {
		raw, marshalErr := attributevalue.MarshalMap(row)
		if marshalErr != nil {
			return nil, marshalErr
		}
		writes = append(writes, types.TransactWriteItem{Put: &types.Put{TableName: &table, Item: raw, ConditionExpression: aws.String("attribute_not_exists(pk)")}})
	}
	return writes, nil
}
