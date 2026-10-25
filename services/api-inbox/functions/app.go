package functions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

const (
	conversationsTableEnv   = "CONVERSATIONS_TABLE_NAME"
	cursorSecretPathEnv     = "INBOX_CURSOR_SECRET_PARAM"
	runtimeConfigPathEnv    = "INBOX_RUNTIME_CONFIG_PARAM"
	whatsAppConfigPathEnv   = "INBOX_WHATSAPP_CONFIG_PARAM"
	inboxTemporaryBucketEnv = "INBOX_TEMPORARY_BUCKET_NAME"
	inboxWebSocketURLEnv    = "INBOX_WEBSOCKET_URL"
)

type parameterReader interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type cursorSecretConfig struct {
	Current   string  `json:"current"`
	Previous  *string `json:"previous"`
	RotatedAt string  `json:"rotatedAt"`
}

type runtimeConfig struct {
	MaxMaterializableConversations int `json:"maxMaterializableConversations"`
	DueQueueShards                 int `json:"dueQueueShards"`
	WorkerQueryLimit               int `json:"workerQueryLimit"`
}

type whatsAppConfig struct {
	AccessToken       string `json:"accessToken"`
	PhoneNumberID     string `json:"phoneNumberId"`
	VerifyToken       string `json:"verifyToken"`
	AppSecret         string `json:"appSecret"`
	BusinessAccountID string `json:"businessAccountId"`
}

type cursorSecrets struct {
	Current  []byte
	Previous []byte
}

// App contiene las dependencias inicializadas una vez por contenedor Lambda.
type App struct {
	List       ConversationLister
	Detail     ConversationDetailer
	Send       InboxSender
	Contact    ContactUpdater
	Attachment interface {
		AttachmentPreparer
		AttachmentSender
	}
	Mutation     ConversationMutator
	Bulk         BulkOperator
	Subscription SubscriptionCreator
	Webhook      WebhookReceiver
	Templates    TemplateLister
	TemplateSend TemplateSender
	Reaction     ReactionSender
	VerifyToken  string
	Port         string
}

type InboxSender interface {
	TextSender
	TemplateSender
	RichMessageSender
	SendManager
}

var appOnce sync.Once
var appInstance *App
var appError error

// GetApp construye el servicio de lista y carga sus parámetros SecureString.
func GetApp(ctx context.Context) (*App, error) {
	appOnce.Do(func() {
		base := bootstrap.LoadConfig()
		awsConfig, err := bootstrap.LoadAWSConfig(ctx, base)
		if err != nil {
			appError = fmt.Errorf("load inbox AWS config: %w", err)
			return
		}
		tableName := os.Getenv(conversationsTableEnv)
		cursorPath := os.Getenv(cursorSecretPathEnv)
		runtimePath := os.Getenv(runtimeConfigPathEnv)
		whatsAppPath := os.Getenv(whatsAppConfigPathEnv)
		bucketName := os.Getenv(inboxTemporaryBucketEnv)
		webSocketURL := os.Getenv(inboxWebSocketURLEnv)
		if tableName == "" || cursorPath == "" || runtimePath == "" || whatsAppPath == "" || bucketName == "" || webSocketURL == "" {
			appError = errors.New("inbox environment configuration is incomplete")
			return
		}
		parameters := ssm.NewFromConfig(awsConfig)
		secret, runtime, err := loadInboxParameters(ctx, parameters, cursorPath, runtimePath)
		if err != nil {
			appError = err
			return
		}
		client := awsddb.New(awsConfig)
		codec, err := domain.NewListCursorCodecWithPrevious(secret.Current, secret.Previous, nil)
		if err != nil {
			appError = err
			return
		}
		store, err := domain.NewDynamoListSnapshotStore(client, tableName, nil)
		if err != nil {
			appError = err
			return
		}
		service, err := domain.NewConversationListService(client, store, codec, tableName, runtime.MaxMaterializableConversations, nil)
		if err != nil {
			appError = err
			return
		}
		detail, err := domain.NewConversationDetailService(client, codec, tableName)
		if err != nil {
			appError = err
			return
		}
		var whatsApp whatsAppConfig
		if err := readSecureJSON(ctx, parameters, whatsAppPath, &whatsApp); err != nil {
			appError = err
			return
		}
		provider, err := domain.NewMetaTextProvider(nil, whatsApp.PhoneNumberID, whatsApp.AccessToken)
		if err != nil {
			appError = err
			return
		}
		repository, err := domain.NewDynamoSendRepository(client, tableName)
		if err != nil {
			appError = err
			return
		}
		sender, err := domain.NewSendService(repository, provider, nil)
		if err != nil {
			appError = err
			return
		}
		contact, err := domain.NewContactService(client, tableName, nil)
		if err != nil {
			appError = err
			return
		}
		storage := s3.NewFromConfig(awsConfig)
		attachments, err := domain.NewAttachmentService(client, s3.NewPresignClient(storage), storage, repository, provider, tableName, bucketName, nil, randomUUID)
		if err != nil {
			appError = err
			return
		}
		mutation, err := domain.NewConversationMutationService(client, tableName, nil)
		if err != nil {
			appError = err
			return
		}
		bulk, err := domain.NewBulkService(client, tableName, nil)
		if err != nil {
			appError = err
			return
		}
		subscription, err := domain.NewSubscriptionService(client, tableName, webSocketURL, nil)
		if err != nil {
			appError = err
			return
		}
		webhook, err := domain.NewWebhookService(client, tableName, whatsApp.AppSecret, nil)
		if err != nil {
			appError = err
			return
		}
		reaction, err := domain.NewReactionService(client, tableName, provider, nil)
		if err != nil {
			appError = err
			return
		}
		catalog := domain.NewMetaTemplateCatalog(nil, whatsApp.BusinessAccountID, whatsApp.AccessToken)
		templateSend, err := domain.NewTemplateService(catalog, sender)
		if err != nil {
			appError = err
			return
		}
		appInstance = &App{List: service, Detail: detail, Send: sender, Contact: contact, Attachment: attachments, Mutation: mutation, Bulk: bulk, Subscription: subscription, Webhook: webhook, Templates: catalog, TemplateSend: templateSend, Reaction: reaction, VerifyToken: whatsApp.VerifyToken, Port: base.Port}
	})
	if appError != nil {
		// El detalle se registra solo en CloudWatch; la respuesta HTTP conserva el
		// error genérico para no exponer configuración interna al cliente.
		fmt.Printf("[INBOX][INIT] application initialization failed: %v\n", appError)
	}
	return appInstance, appError
}

func randomUUID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func loadInboxParameters(ctx context.Context, reader parameterReader, cursorPath, runtimePath string) (cursorSecrets, runtimeConfig, error) {
	var cursor cursorSecretConfig
	if err := readSecureJSON(ctx, reader, cursorPath, &cursor); err != nil {
		return cursorSecrets{}, runtimeConfig{}, err
	}
	secret, err := base64.StdEncoding.DecodeString(cursor.Current)
	if err != nil || len(secret) < 32 {
		return cursorSecrets{}, runtimeConfig{}, errors.New("invalid inbox cursor secret")
	}
	if cursor.RotatedAt == "" {
		return cursorSecrets{}, runtimeConfig{}, errors.New("invalid inbox cursor rotation timestamp")
	}
	if _, err := time.Parse(time.RFC3339, cursor.RotatedAt); err != nil {
		return cursorSecrets{}, runtimeConfig{}, errors.New("invalid inbox cursor rotation timestamp")
	}
	var previous []byte
	if cursor.Previous != nil {
		previous, err = base64.StdEncoding.DecodeString(*cursor.Previous)
		if err != nil || len(previous) < 32 {
			return cursorSecrets{}, runtimeConfig{}, errors.New("invalid previous inbox cursor secret")
		}
	}
	var runtime runtimeConfig
	if err := readSecureJSON(ctx, reader, runtimePath, &runtime); err != nil {
		return cursorSecrets{}, runtimeConfig{}, err
	}
	if runtime.MaxMaterializableConversations < 20_000 || runtime.DueQueueShards != 16 || runtime.WorkerQueryLimit != 100 {
		return cursorSecrets{}, runtimeConfig{}, errors.New("invalid inbox runtime config")
	}
	return cursorSecrets{Current: secret, Previous: previous}, runtime, nil
}

func readSecureJSON(ctx context.Context, reader parameterReader, name string, target any) error {
	output, err := reader.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return fmt.Errorf("read inbox parameter %q: %w", name, err)
	}
	if output == nil || output.Parameter == nil || output.Parameter.Value == nil || output.Parameter.Type != ssmtypes.ParameterTypeSecureString {
		return fmt.Errorf("read inbox parameter %q: empty value", name)
	}
	decoder := json.NewDecoder(strings.NewReader(*output.Parameter.Value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode inbox parameter %q: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode inbox parameter %q: trailing JSON content", name)
	}
	return nil
}
