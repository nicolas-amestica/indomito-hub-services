package functions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/aws/aws-lambda-go/events"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"go.uber.org/zap"
)

// App contiene clientes y metadatos de ejecución; no conserva secretos en logs.
type App struct {
	db                   dynamoStore
	ssm                  parameterAPI
	stage, topic, region string
	log                  *zap.Logger
}

// New inicializa clientes sin realizar envíos ni modificar recursos.
func New(ctx context.Context) (*App, error) {
	stage, table, topic := os.Getenv("APP_STAGE"), os.Getenv("PAYMENTS_TABLE_NAME"), os.Getenv("ALARM_TOPIC_ARN")
	if (stage != "dev" && stage != "prd") || table == "" || topic == "" {
		return nil, errors.New("MONITOR_ENV_INVALID")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, errors.New("MONITOR_AWS_CONFIG_FAILED")
	}
	return &App{db: dynamoStore{client: dynamodb.NewFromConfig(cfg), table: table}, ssm: ssm.NewFromConfig(cfg), stage: stage, topic: topic, region: cfg.Region, log: zap.NewExample()}, nil
}
func (a *App) dependencies(ctx context.Context, now time.Time) (config, mailer, error) {
	raw, err := parameter(ctx, a.ssm, a.stage, "khipu/renditions/monitoring-config")
	if err != nil {
		return config{}, mailer{}, err
	}
	c, err := parseConfig(raw, now)
	if err != nil {
		return c, mailer{}, err
	}
	raw, err = parameter(ctx, a.ssm, a.stage, "receipt-smtp")
	if err != nil {
		return c, mailer{}, err
	}
	smtp, err := parseSMTP(raw)
	if err != nil {
		return c, mailer{}, err
	}
	client, err := smtpClient(smtp)
	if err != nil {
		return c, mailer{}, errors.New("MONITOR_SMTP_INVALID")
	}
	return c, mailer{client: client, from: smtp.From, to: c.Email, stage: a.stage}, nil
}

// Monitor mantiene el contrato de la Lambda programada y sus cursores existentes.
func (a *App) Monitor(ctx context.Context) (resultErr error) {
	now := time.Now().UTC()
	c, mail, err := a.dependencies(ctx, now)
	if err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("MONITOR_OWNER_FAILED")
	}
	owner := hex.EncodeToString(random[:])
	acquired, err := a.db.acquire(ctx, owner, now)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	defer func() {
		// Aun si el trabajo agota su contexto, intentar liberar con un límite pequeño.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := a.db.release(cleanup, owner); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if err := runMonitor(ctx, a.db, mail, c, now); err != nil {
		return errors.New("MONITOR_RUN_FAILED")
	}
	a.log.Info("monitor heartbeat", zap.Any("_aws", map[string]any{"Timestamp": time.Now().UnixMilli(), "CloudWatchMetrics": []any{map[string]any{"Namespace": "Indomito/Renditions", "Dimensions": [][]string{{"Stage"}}, "Metrics": []any{map[string]any{"Name": "MonitorHeartbeat", "Unit": "Count"}}}}}), zap.String("Stage", a.stage), zap.Int("MonitorHeartbeat", 1))
	return nil
}

func alarmMessage(record events.SNSEventRecord, stage, topic, region string) (string, string, error) {
	if topic == "" || record.EventSource != "aws:sns" || record.SNS.TopicArn != topic {
		return "", "", errors.New("MONITOR_ALARM_SOURCE_INVALID")
	}
	var alarm struct {
		AlarmName     string
		NewStateValue string
	}
	if err := json.Unmarshal([]byte(record.SNS.Message), &alarm); err != nil {
		return "", "", errors.New("MONITOR_ALARM_INVALID")
	}
	if (alarm.NewStateValue != "ALARM" && alarm.NewStateValue != "OK") || !regexp.MustCompile(`^khipu-renditions-`+regexp.QuoteMeta(stage)+`-[A-Za-z0-9-]+$`).MatchString(alarm.AlarmName) {
		return "", "", errors.New("MONITOR_ALARM_INVALID")
	}
	return "Alarma Khipu: " + alarm.NewStateValue, alarm.AlarmName + "\nEstado: " + alarm.NewStateValue + "\nRevisa la alarma en CloudWatch (" + region + ").", nil
}

// Alarm recibe exclusivamente notificaciones del topic SNS propio del ambiente.
func (a *App) Alarm(ctx context.Context, event events.SNSEvent) error {
	// Validar todo el lote antes de cargar secretos o enviar el primer correo.
	for _, record := range event.Records {
		if _, _, err := alarmMessage(record, a.stage, a.topic, a.region); err != nil {
			return err
		}
	}
	_, mail, err := a.dependencies(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, record := range event.Records {
		subject, body, err := alarmMessage(record, a.stage, a.topic, a.region)
		if err != nil {
			return err
		}
		if err := mail.Send(ctx, subject, body); err != nil {
			return err
		}
	}
	return nil
}
