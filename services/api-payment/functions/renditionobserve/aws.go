package renditionobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

const evidenceBucket = "ind-khipu-rendition-capture-dev-382670112717"

type readOnlyDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

type awsReader struct{ db readOnlyDynamo }
type row struct {
	Attempt    *domain.Attempt    `dynamodbav:"attempt"`
	Account    *domain.Account    `dynamodbav:"account"`
	Event      *domain.Event      `dynamodbav:"event"`
	Settlement *domain.Settlement `dynamodbav:"settlement"`
}

func (r awsReader) get(ctx context.Context, pk, sk string) (row, bool, error) {
	var item row
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String("ind-dev-pagos-ddb-dev-pri-use1"), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}})
	if err != nil {
		return item, false, err
	}
	if out == nil || len(out.Item) == 0 {
		return item, false, nil
	}
	err = attributevalue.UnmarshalMap(out.Item, &item)
	return item, true, err
}

func (r awsReader) Read(ctx context.Context, attemptID, paymentID string) (Snapshot, error) {
	var s Snapshot
	a, _, err := r.get(ctx, "ATTEMPT#"+attemptID, "META")
	if err != nil {
		return s, err
	}
	s.Attempt = a.Attempt
	if s.Attempt == nil {
		return s, nil
	}
	o, _, err := r.get(ctx, "ATTEMPT#"+attemptID, "OUTCOME")
	if err != nil {
		return s, err
	}
	s.Event = o.Event
	a, _, err = r.get(ctx, "ACCOUNT#"+s.Attempt.AccountID, "META")
	if err != nil {
		return s, err
	}
	s.Account = a.Account
	if s.Account == nil {
		return s, nil
	}
	a, _, err = r.get(ctx, "TRIP#"+s.Account.TripID, "SETTLEMENT#khipu:"+paymentID)
	if err != nil {
		return s, err
	}
	s.Settlement = a.Settlement
	_, s.Resolved, err = r.get(ctx, "ATTEMPT#"+attemptID, "RESOLUTION")
	return s, err
}

type sampleObjects interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}
type awsWriter struct{ client sampleObjects }

func readObject(ctx context.Context, client sampleObjects, key string) ([]byte, error) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(evidenceBucket), Key: &key})
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(out.Body, 400000))
	closeErr := out.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(b) >= 400000 {
		return nil, errors.New("objeto excede límite")
	}
	return b, nil
}

func (w awsWriter) SaveOnce(ctx context.Context, hash string, body []byte) (bool, error) {
	if !hashPattern.MatchString(hash) {
		return false, errors.New("hash inválido")
	}
	key := "unverified/observations/" + hash + ".json"
	_, err := w.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(evidenceBucket), Key: &key, Body: bytes.NewReader(body), IfNoneMatch: aws.String("*"), ContentType: aws.String("application/json"), Metadata: map[string]string{"trust": "dev-exception-untrusted", "mode": "observe"}})
	if err == nil {
		return false, nil
	}
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "PreconditionFailed" {
		return false, err
	}
	previous, err := readObject(ctx, w.client, key)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(previous, body) {
		return false, errors.New("la observación existente difiere; revisar sin sobrescribir")
	}
	return true, nil
}

// RunDEV es una herramienta explícita, no un webhook ni worker automático.
// STS verifica la cuenta antes de acceder a SSM/S3/DynamoDB; DynamoDB solo GetItem.
func RunDEV(ctx context.Context) (Result, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile("pa-dev"), config.WithRegion("us-east-1"))
	if err != nil {
		return Result{}, err
	}
	identity, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return Result{}, err
	}
	if identity.Account == nil || *identity.Account != DevAccount {
		return Result{}, errors.New("cuenta AWS no autorizada para excepción DEV")
	}
	parameter, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/dev/payments/khipu/renditions/dev-observation-config"), WithDecryption: aws.Bool(true)})
	if err != nil {
		return Result{}, err
	}
	if parameter.Parameter == nil || parameter.Parameter.Type != ssmtypes.ParameterTypeSecureString || parameter.Parameter.Value == nil {
		return Result{}, errors.New("se requiere SecureString")
	}
	var p Policy
	if err = json.Unmarshal([]byte(*parameter.Parameter.Value), &p); err != nil {
		return Result{}, errors.New("configuración de excepción inválida")
	}
	if err = p.validate("dev", *identity.Account, time.Now()); err != nil {
		return Result{}, err
	}
	client := s3.NewFromConfig(cfg)
	b, err := readObject(ctx, client, "unverified/slot-01.json")
	if err != nil {
		return Result{}, err
	}
	var captured struct {
		Body []byte `json:"bodyBase64"`
		Hash string `json:"sha256"`
	}
	if json.Unmarshal(b, &captured) != nil || captured.Hash != p.ReportSHA256 {
		return Result{}, errors.New("evidencia no autorizada")
	}
	return Observe(ctx, "dev", *identity.Account, p, captured.Body, time.Now(), awsReader{db: dynamodb.NewFromConfig(cfg)}, awsWriter{client: client})
}
