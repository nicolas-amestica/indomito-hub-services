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
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	table, agentPath, geminiPath, whatsAppPath := os.Getenv("CONVERSATIONS_TABLE_NAME"), os.Getenv("INBOX_AGENT_CONFIG_PARAM"), os.Getenv("INBOX_GEMINI_CONFIG_PARAM"), os.Getenv("INBOX_WHATSAPP_CONFIG_PARAM")
	assetPath, meetingPath := os.Getenv("INBOX_ASSET_CONFIG_PARAM"), os.Getenv("INBOX_MEETING_CONFIG_PARAM")
	derivationNotificationPath := os.Getenv("INBOX_DERIVATION_NOTIFICATION_CONFIG_PARAM")
	if table == "" || agentPath == "" || geminiPath == "" || whatsAppPath == "" || assetPath == "" || meetingPath == "" || derivationNotificationPath == "" {
		return events.SQSEventResponse{}, errors.New("agent environment is incomplete")
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	parameters := ssm.NewFromConfig(cfg)
	read := func(path string, target any) error {
		out, e := parameters.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(path), WithDecryption: aws.Bool(true)})
		if e != nil {
			return e
		}
		if out.Parameter == nil || out.Parameter.Value == nil {
			return errors.New("empty agent parameter")
		}
		return json.Unmarshal([]byte(*out.Parameter.Value), target)
	}
	var agent domain.AgentConfig
	var gemini domain.GeminiConfig
	var assets domain.AgentAssetConfig
	var meeting domain.AgentMeetingConfig
	var derivationNotification domain.DerivationNotificationConfig
	var whatsApp struct {
		AccessToken   string `json:"accessToken"`
		PhoneNumberID string `json:"phoneNumberId"`
	}
	if err = read(agentPath, &agent); err != nil {
		return events.SQSEventResponse{}, err
	}
	if err = read(geminiPath, &gemini); err != nil {
		return events.SQSEventResponse{}, err
	}
	if err = read(whatsAppPath, &whatsApp); err != nil {
		return events.SQSEventResponse{}, err
	}
	if err = read(assetPath, &assets); err != nil {
		return events.SQSEventResponse{}, err
	}
	if err = read(meetingPath, &meeting); err != nil {
		return events.SQSEventResponse{}, err
	}
	if err = read(derivationNotificationPath, &derivationNotification); err != nil {
		return events.SQSEventResponse{}, err
	}
	var oauth domain.GoogleOAuthConfig
	if err = read(meeting.CalendarOAuthParameterPath, &oauth); err != nil {
		return events.SQSEventResponse{}, err
	}
	db := dynamodb.NewFromConfig(cfg)
	provider, err := domain.NewMetaTextProvider(nil, whatsApp.PhoneNumberID, whatsApp.AccessToken)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	repository, err := domain.NewDynamoSendRepository(db, table)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	sender, err := domain.NewSendService(repository, provider, nil)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	storage := s3.NewFromConfig(cfg)
	toolbox, err := domain.NewAgentToolbox(storage, sender, assets, meeting, oauth)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	processor, err := domain.NewAgentService(db, storage, transcribe.NewFromConfig(cfg), sender, toolbox, table, agent, gemini)
	if err != nil {
		return events.SQSEventResponse{}, err
	}
	if derivationNotification.Enabled {
		notifier, notifierErr := domain.NewWhatsAppDerivationNotifier(sender, derivationNotification)
		if notifierErr != nil {
			return events.SQSEventResponse{}, notifierErr
		}
		processor.SetDerivationNotifier(notifier)
	}
	return functions.ProcessAgentQueue(ctx, processor, event), nil
}

func main() { lambda.Start(handle) }
