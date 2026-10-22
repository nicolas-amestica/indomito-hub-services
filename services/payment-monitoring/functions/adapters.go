package functions

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	gomail "github.com/wneessen/go-mail"
)

type dynamoAPI interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}
type parameterAPI interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}
type dynamoStore struct {
	client dynamoAPI
	table  string
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}
func (d dynamoStore) Get(ctx context.Context, pk, sk string) (item, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(d.table), Key: key(pk, sk), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return item{}, errors.New("MONITOR_READ_FAILED")
	}
	var row item
	err = attributevalue.UnmarshalMap(out.Item, &row)
	return row, err
}
func (d dynamoStore) Put(ctx context.Context, row item) error {
	if row.PK != monitorPK {
		return errors.New("MONITOR_WRITE_FORBIDDEN")
	}
	values, err := attributevalue.MarshalMap(row)
	if err != nil {
		return err
	}
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(d.table), Item: values})
	if err != nil {
		return errors.New("MONITOR_WRITE_FAILED")
	}
	return nil
}
func (d dynamoStore) Query(ctx context.Context, pk string, after map[string]string) (page, error) {
	var start map[string]types.AttributeValue
	if len(after) > 0 {
		var err error
		start, err = attributevalue.MarshalMap(after)
		if err != nil {
			return page{}, err
		}
	}
	out, err := d.client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(d.table), KeyConditionExpression: aws.String("pk = :pk"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50), ExclusiveStartKey: start})
	if err != nil {
		return page{}, errors.New("MONITOR_QUERY_FAILED")
	}
	var result page
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &result.Items); err != nil {
		return page{}, errors.New("MONITOR_SOURCE_INVALID")
	}
	if len(out.LastEvaluatedKey) > 0 {
		if err := attributevalue.UnmarshalMap(out.LastEvaluatedKey, &result.Next); err != nil {
			return page{}, errors.New("MONITOR_CURSOR_INVALID")
		}
	}
	return result, nil
}
func (d dynamoStore) acquire(ctx context.Context, owner string, now time.Time) (bool, error) {
	values := key(monitorPK, "LEASE")
	values["owner"] = &types.AttributeValueMemberS{Value: owner}
	until, err := attributevalue.Marshal(now.Add(4 * time.Minute).UnixMilli())
	if err != nil {
		return false, err
	}
	values["until"] = until
	current, err := attributevalue.Marshal(now.UnixMilli())
	if err != nil {
		return false, err
	}
	_, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(d.table), Item: values, ConditionExpression: aws.String("attribute_not_exists(pk) OR #until < :now"), ExpressionAttributeNames: map[string]string{"#until": "until"}, ExpressionAttributeValues: map[string]types.AttributeValue{":now": current}})
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("MONITOR_LEASE_FAILED")
	}
	return true, nil
}
func (d dynamoStore) release(ctx context.Context, owner string) error {
	_, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(d.table), Key: key(monitorPK, "LEASE"), ConditionExpression: aws.String("#owner = :owner"), ExpressionAttributeNames: map[string]string{"#owner": "owner"}, ExpressionAttributeValues: map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: owner}}})
	if err != nil {
		return errors.New("MONITOR_RELEASE_FAILED")
	}
	return nil
}
func parameter(ctx context.Context, client parameterAPI, stage, suffix string) (string, error) {
	if stage != "dev" && stage != "prd" {
		return "", errors.New("MONITOR_ENV_INVALID")
	}
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/" + stage + "/payments/" + suffix), WithDecryption: aws.Bool(true)})
	if err != nil {
		return "", errors.New("MONITOR_CONFIG_READ_FAILED")
	}
	if out.Parameter == nil || out.Parameter.Type != ssmtypes.ParameterTypeSecureString || aws.ToString(out.Parameter.Value) == "" {
		return "", errors.New("MONITOR_SECURE_CONFIG_REQUIRED")
	}
	return aws.ToString(out.Parameter.Value), nil
}

type smtpConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	From     string `json:"from"`
}

func parseSMTP(raw string) (smtpConfig, error) {
	var c smtpConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, errors.New("MONITOR_SMTP_INVALID")
	}
	_, err := mail.ParseAddress(c.From)
	if err != nil || c.Host == "" || c.User == "" || c.Password == "" || (c.Port != 465 && c.Port != 587) || strings.ContainsAny(c.Host+c.User+c.From, "\r\n") {
		return c, errors.New("MONITOR_SMTP_INVALID")
	}
	return c, nil
}
func smtpClient(c smtpConfig) (*gomail.Client, error) {
	options := []gomail.Option{gomail.WithPort(c.Port), gomail.WithUsername(c.User), gomail.WithPassword(c.Password), gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithTLSConfig(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}), gomail.WithTimeout(20 * time.Second)}
	if c.Port == 465 {
		options = append(options, gomail.WithSSL())
	} else {
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	return gomail.NewClient(c.Host, options...)
}

type mailer struct {
	client interface {
		DialAndSendWithContext(context.Context, ...*gomail.Msg) error
	}
	from, to, stage string
}

func (m mailer) Send(ctx context.Context, subject, body string) error {
	message := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	if err := message.From(m.from); err != nil {
		return errors.New("MONITOR_SMTP_INVALID")
	}
	if err := message.To(m.to); err != nil {
		return errors.New("MONITOR_EMAIL_INVALID")
	}
	message.Subject("[" + strings.ToUpper(m.stage) + "] " + subject)
	message.SetBodyString(gomail.TypeTextPlain, body+"\n\nRevisa Tesorería > Rendiciones Khipu. Este aviso no modifica caja ni verifica cartola.")
	timeout, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := m.client.DialAndSendWithContext(timeout, message); err != nil {
		return errors.New("MONITOR_EMAIL_NOT_ACCEPTED")
	}
	return nil
}
