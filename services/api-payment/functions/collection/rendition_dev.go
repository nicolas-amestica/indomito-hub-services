package collection

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

const devRenditionARNPrefix = "arn:aws:lambda:us-east-1:382670112717:function:"

func devRenditionRole(ctx context.Context) string {
	lc, ok := lambdacontext.FromContext(ctx)
	if !ok {
		return ""
	}
	name := strings.TrimPrefix(lc.InvokedFunctionArn, devRenditionARNPrefix)
	if name == lc.InvokedFunctionArn {
		return ""
	}
	name = strings.Split(name, ":")[0]
	switch name {
	case "ind-khipu-rendition-capture-dev":
		return "receiver"
	case "ind-khipu-rendition-worker-dev":
		return "worker"
	default:
		return ""
	}
}

func newDevRenditionApp(ctx context.Context) (*RenditionApp, error) {
	if devRenditionRole(ctx) == "" {
		return nil, providers.ErrRenditionPolicy
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		return nil, err
	}
	out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/dev/payments/khipu/renditions/automation-config"), WithDecryption: aws.Bool(true)})
	if err != nil {
		return nil, err
	}
	if out.Parameter == nil || out.Parameter.Type != "SecureString" || out.Parameter.Value == nil {
		return nil, providers.ErrRenditionPolicy
	}
	c, err := decodeRenditionConfig([]byte(*out.Parameter.Value))
	if err != nil {
		return nil, err
	}
	if !c.Policy.DevelopmentSimulation {
		return nil, providers.ErrRenditionPolicy
	}
	return &RenditionApp{
		Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: "ind-dev-pagos-ddb-dev-pri-use1"},
		Objects:  renditionS3{client: s3.NewFromConfig(cfg), bucket: "ind-khipu-rendition-capture-dev-382670112717"},
		Verifier: providers.DevelopmentRenditionVerifier{RenditionVerifier: providers.RenditionVerifier{RecipientUID: c.RecipientUID, Bank: c.Bank, Account: c.Account}, Stage: "dev", AWSAccount: "382670112717"},
		Policy:   c.Policy, Stage: "dev", Now: time.Now,
	}, nil
}

// DevRenditionHandler es exclusivo de dos funciones y una cuenta DEV. No tiene
// expiración ni pin de certificado; no se usa en los entrypoints productivos.
func DevRenditionHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	role := devRenditionRole(ctx)
	if role == "" {
		return nil, errors.New("DEV rendition runtime denied")
	}
	app, err := newDevRenditionApp(ctx)
	if err != nil {
		return nil, errors.New("DEV rendition configuration unavailable")
	}
	if role == "worker" {
		var event struct {
			Source string `json:"source"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Source != "aws.events" {
			return nil, errors.New("DEV worker event denied")
		}
		err = app.Run(ctx)
		if err != nil {
			return nil, errors.New("DEV rendition worker failed; inspect pending or review batches")
		}
		return map[string]string{"status": "PROCESSED", "trust": "DEV_UNAUTHENTICATED_SIMULATION"}, nil
	}
	var request events.APIGatewayV2HTTPRequest
	if json.Unmarshal(raw, &request) != nil {
		return webhookResponse(400), nil
	}
	response := app.Handle(ctx, request)
	if response.StatusCode >= 400 {
		renditionDevMetric("RejectedReports", 1)
	}
	return response, nil
}

func renditionDevMetric(name string, value int) {
	zap.NewExample().Info("DEV rendition operational metric",
		zap.String("Stage", "dev"), zap.Int(name, value),
		zap.Any("_aws", map[string]any{"Timestamp": time.Now().UnixMilli(), "CloudWatchMetrics": []any{map[string]any{"Namespace": "Indomito/DEV/Renditions", "Dimensions": [][]string{{"Stage"}}, "Metrics": []any{map[string]string{"Name": name, "Unit": "Count"}}}}}),
	)
}
