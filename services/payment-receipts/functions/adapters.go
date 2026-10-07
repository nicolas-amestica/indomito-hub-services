package functions

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	gomail "github.com/wneessen/go-mail"
)

type dynamoAPI interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

type documentAPI interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type store struct {
	client          dynamoAPI
	table           string
	parentReceiptID string
	now             func() time.Time
}

func (s store) key(id string) map[string]types.AttributeValue {
	if s.parentReceiptID != "" {
		return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "RECEIPT#" + s.parentReceiptID}, "sk": &types.AttributeValueMemberS{Value: "DELIVERY#" + id}}
	}
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "RECEIPT#" + id}, "sk": &types.AttributeValueMemberS{Value: "META"}}
}

func (s store) get(ctx context.Context, id string) (receiptRow, error) {
	output, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.table), Key: s.key(id), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return receiptRow{}, fmt.Errorf("leer comprobante: %w", err)
	}
	if len(output.Item) == 0 {
		return receiptRow{}, errors.New("RECEIPT_NOT_FOUND")
	}
	var row receiptRow
	if err = attributevalue.UnmarshalMap(output.Item, &row); err != nil {
		return receiptRow{}, fmt.Errorf("decodificar comprobante: %w", err)
	}
	return row, nil
}

func (s store) jobs(ctx context.Context, date string, limit int32, cursor string) ([]receiptRow, string, error) {
	partition := "JOB#" + date
	input := &dynamodb.QueryInput{TableName: aws.String(s.table), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: partition}, ":prefix": &types.AttributeValueMemberS{Value: "PENDING#"}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(limit)}
	if cursor != "" {
		input.ExclusiveStartKey = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: partition}, "sk": &types.AttributeValueMemberS{Value: "PENDING#" + cursor}}
	}
	output, err := s.client.Query(ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("consultar trabajos: %w", err)
	}
	items := make([]receiptRow, 0, len(output.Items))
	for _, item := range output.Items {
		var row receiptRow
		if err = attributevalue.UnmarshalMap(item, &row); err != nil {
			return nil, "", fmt.Errorf("decodificar trabajo: %w", err)
		}
		items = append(items, row)
	}
	next := ""
	if value, ok := output.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS); ok {
		next = strings.TrimPrefix(value.Value, "PENDING#")
	}
	return items, next, nil
}

func (s store) claim(ctx context.Context, id, owner string) (bool, error) {
	now := s.now().Unix()
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.table), Key: s.key(id),
		ConditionExpression:      aws.String("attribute_exists(pk) AND (attribute_not_exists(leaseUntil) OR leaseUntil < :now) AND (attribute_not_exists(deliveryAttempts) OR deliveryAttempts < :max) AND #status <> :sent AND #status <> :failed"),
		UpdateExpression:         aws.String("SET leaseOwner = :owner, leaseUntil = :until ADD deliveryAttempts :one"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":owner": &types.AttributeValueMemberS{Value: owner}, ":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)}, ":until": &types.AttributeValueMemberN{Value: strconv.FormatInt(now+120, 10)}, ":max": &types.AttributeValueMemberN{Value: "5"}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":sent": &types.AttributeValueMemberS{Value: statusSent}, ":failed": &types.AttributeValueMemberS{Value: statusDeliveryFailed},
		}})
	if err == nil {
		return true, nil
	}
	var conditional *types.ConditionalCheckFailedException
	if errors.As(err, &conditional) {
		return false, nil
	}
	return false, fmt.Errorf("reclamar comprobante: %w", err)
}

func (s store) documentReady(ctx context.Context, id, owner, key, digest string) error {
	return s.ownedUpdate(ctx, id, owner, "SET documentKey = :key, documentSha256 = :sha, #status = :status", map[string]string{"#status": "status"}, map[string]types.AttributeValue{":key": &types.AttributeValueMemberS{Value: key}, ":sha": &types.AttributeValueMemberS{Value: digest}, ":status": &types.AttributeValueMemberS{Value: statusDocumentReady}})
}

func (s store) sent(ctx context.Context, id, owner string) error {
	return s.ownedUpdate(ctx, id, owner, "SET #status = :status, sentAt = :at REMOVE leaseOwner, leaseUntil", map[string]string{"#status": "status"}, map[string]types.AttributeValue{":status": &types.AttributeValueMemberS{Value: statusSent}, ":at": &types.AttributeValueMemberS{Value: s.now().UTC().Format(time.RFC3339Nano)}})
}

func (s store) failed(ctx context.Context, id, owner, code string) error {
	if !allowedFailure(code) {
		code = "DELIVERY_DEPENDENCY_FAILURE"
	}
	row, err := s.get(ctx, id)
	if err != nil || row.LeaseOwner != owner {
		return err
	}
	status := "DELIVERY_RETRY"
	if row.DeliveryAttempts >= 5 {
		status = statusDeliveryFailed
	}
	return s.ownedUpdate(ctx, id, owner, "SET #status = :status, lastFailureCode = :code REMOVE leaseOwner, leaseUntil", map[string]string{"#status": "status"}, map[string]types.AttributeValue{":status": &types.AttributeValueMemberS{Value: status}, ":code": &types.AttributeValueMemberS{Value: code}})
}

func (s store) finishJob(ctx context.Context, date, id, status, receiptID string) error {
	if status != statusSent && status != statusDocumentReady && status != statusDeliveryFailed {
		return errors.New("INVALID_JOB_RESULT")
	}
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.table), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "JOB#" + date}, "sk": &types.AttributeValueMemberS{Value: "PENDING#" + id}}, ConditionExpression: aws.String("attribute_exists(pk) AND receiptId = :id AND (#status = :pending OR #status = :done)"), UpdateExpression: aws.String("SET #status = :done"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: receiptID}, ":pending": &types.AttributeValueMemberS{Value: "PENDING"}, ":done": &types.AttributeValueMemberS{Value: status}}})
	if err != nil {
		return fmt.Errorf("cerrar trabajo: %w", err)
	}
	return nil
}

func (s store) ownedUpdate(ctx context.Context, id, owner, expression string, names map[string]string, values map[string]types.AttributeValue) error {
	values[":owner"] = &types.AttributeValueMemberS{Value: owner}
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(s.table), Key: s.key(id), ConditionExpression: aws.String("leaseOwner = :owner"), UpdateExpression: aws.String(expression), ExpressionAttributeNames: names, ExpressionAttributeValues: values})
	if err != nil {
		return fmt.Errorf("actualizar entrega: %w", err)
	}
	return nil
}

type documents struct {
	client documentAPI
	bucket string
}

func (d documents) putImmutable(ctx context.Context, document renderedDocument) error {
	_, err := d.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(d.bucket), Key: aws.String(document.Key), Body: bytes.NewReader(document.Bytes), ContentType: aws.String("application/pdf"), ContentDisposition: aws.String(`attachment; filename="comprobante.pdf"`), ServerSideEncryption: "AES256", IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": document.SHA256}})
	if err == nil {
		return nil
	}
	existing, headErr := d.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(d.bucket), Key: aws.String(document.Key)})
	if headErr == nil && existing.Metadata["sha256"] == document.SHA256 {
		return nil
	}
	if headErr == nil {
		return errors.New("IMMUTABLE_RECEIPT_CONFLICT")
	}
	return fmt.Errorf("guardar comprobante: %w", err)
}

type smtpConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	From     string `json:"from"`
}
type mailer struct{ config smtpConfig }

func loadSMTP(ctx context.Context, client *ssm.Client) (smtpConfig, error) {
	output, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/dev/payments/receipt-smtp"), WithDecryption: aws.Bool(true)})
	if err != nil || output.Parameter == nil || output.Parameter.Value == nil {
		return smtpConfig{}, fmt.Errorf("leer SMTP: %w", err)
	}
	return parseSMTPConfig([]byte(*output.Parameter.Value))
}

func parseSMTPConfig(raw []byte) (smtpConfig, error) {
	var config smtpConfig
	if json.Unmarshal(raw, &config) != nil || config.Host == "" || (config.Port != 465 && config.Port != 587) || config.User == "" || config.Password == "" || config.From == "" || strings.ContainsAny(config.Host+config.User+config.From, "\r\n") {
		return smtpConfig{}, errors.New("SMTP_CONFIGURATION_REQUIRED")
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil || from.Address != "no-reply-dev@girasindomito.cl" {
		return smtpConfig{}, errors.New("SMTP_CONFIGURATION_REQUIRED")
	}
	return config, nil
}

func (m mailer) send(ctx context.Context, to, receiptID, deliveryID string, document []byte, review bool) error {
	message, err := buildMailMessage(m.config.From, to, receiptID, deliveryID, document, review)
	if err != nil {
		return err
	}
	options := []gomail.Option{
		gomail.WithPort(m.config.Port),
		gomail.WithUsername(m.config.User),
		gomail.WithPassword(m.config.Password),
		gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
		gomail.WithTLSConfig(&tls.Config{ServerName: m.config.Host, MinVersion: tls.VersionTLS12}),
		gomail.WithTimeout(20 * time.Second),
	}
	if m.config.Port == 465 {
		options = append(options, gomail.WithSSL())
	} else {
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	client, err := gomail.NewClient(m.config.Host, options...)
	if err != nil {
		return fmt.Errorf("configurar SMTP: %w", err)
	}
	if err = client.DialAndSendWithContext(ctx, message); err != nil {
		return fmt.Errorf("enviar SMTP: %w", err)
	}
	return nil
}

func buildMailMessage(from, to, receiptID, deliveryID string, document []byte, review bool) (*gomail.Msg, error) {
	toAddress, err := mail.ParseAddress(to)
	if err != nil || toAddress.Address != to || strings.ContainsAny(to, "\r\n") {
		return nil, errors.New("RECEIPT_EMAIL_MISSING")
	}
	fromAddress, err := mail.ParseAddress(from)
	if err != nil || fromAddress.Address != "no-reply-dev@girasindomito.cl" {
		return nil, errors.New("SMTP_CONFIGURATION_REQUIRED")
	}
	text := "Consulta el portal para revisar el saldo actualizado."
	if review {
		text = "El dinero recibido está pendiente de revisión; no repitas el pago."
	}
	message := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	message.FromMailAddress(fromAddress)
	message.ToMailAddress(toAddress)
	message.Subject("Giras Indomito: comprobante de registro de pago (DEV)")
	message.SetMessageIDWithValue("receipt-" + receiptID + "-" + deliveryID + "@girasindomito.cl")
	message.SetBodyString(gomail.TypeTextPlain, fmt.Sprintf("Adjuntamos el comprobante %s. %s Este documento no es una boleta ni factura del SII. Ambiente de desarrollo.", receiptID, text))
	if err = message.AttachReader("comprobante-"+receiptID+".pdf", bytes.NewReader(document), gomail.WithFileContentType(gomail.ContentType("application/pdf"))); err != nil {
		return nil, fmt.Errorf("adjuntar comprobante: %w", err)
	}
	return message, nil
}

func randomOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("crear lease: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
func allowedFailure(code string) bool {
	return code == "RECEIPT_EMAIL_MISSING" || code == "IMMUTABLE_RECEIPT_CONFLICT" || code == "SMTP_NOT_ACCEPTED" || code == "DELIVERY_DEPENDENCY_FAILURE"
}
