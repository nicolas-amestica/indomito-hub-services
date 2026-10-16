package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	table := os.Getenv("CONVERSATIONS_TABLE_NAME")
	if table == "" {
		return events.SQSEventResponse{}, errors.New("conversations table is required")
	}
	config, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	whatsAppPath, bucket := os.Getenv("INBOX_WHATSAPP_CONFIG_PARAM"), os.Getenv("INBOX_TEMPORARY_BUCKET_NAME")
	if whatsAppPath == "" || bucket == "" {
		return events.SQSEventResponse{}, errors.New("inbound media environment is required")
	}
	parameter, err := ssm.NewFromConfig(config).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(whatsAppPath), WithDecryption: aws.Bool(true)})
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	var whatsApp struct {
		AccessToken string `json:"accessToken"`
	}
	if parameter.Parameter == nil || parameter.Parameter.Value == nil {
		return events.SQSEventResponse{}, errors.New("whatsapp parameter is empty")
	}
	if err = json.Unmarshal([]byte(*parameter.Parameter.Value), &whatsApp); err != nil {
		return events.SQSEventResponse{}, err
	}
	media, err := domain.NewMetaInboundMediaStore(nil, s3.NewFromConfig(config), whatsApp.AccessToken, bucket)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	processor, err := domain.NewInboundProcessorWithMedia(dynamodb.NewFromConfig(config), table, media, nil)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	return functions.ProcessInboundQueue(ctx, processor, event), nil
}

func main() { lambda.Start(handle) }
