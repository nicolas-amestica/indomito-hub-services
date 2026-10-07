package collection

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"ind-hub-api-gox-sls-pri-gh/bootstrap"
)

type s3TaxDocumentSigner struct {
	client *s3.PresignClient
	bucket string
}

func (s s3TaxDocumentSigner) Download(ctx context.Context, key string) (string, error) {
	result, err := s.client.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key, ResponseContentType: aws.String("application/pdf"), ResponseContentDisposition: aws.String(`attachment; filename="boleta-sii.pdf"`)}, func(options *s3.PresignOptions) { options.Expires = 2 * time.Minute })
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

// TaxDocumentsHandler gestiona boletas manuales sin permisos sobre Khipu ni SMTP.
func TaxDocumentsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.Authorizer == nil || req.RequestContext.Authorizer.Lambda["paymentAccess"] != "admin" || os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" || os.Getenv("RECEIPTS_BUCKET_NAME") == "" {
		return portalFailure(403), nil
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return portalFailure(503), nil
	}
	db := dynamodb.NewFromConfig(cfg)
	storage := s3.NewFromConfig(cfg)
	app := TaxDocumentsApp{Accounts: Service{DB: db, Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Storage: storage, Signer: s3TaxDocumentSigner{client: s3.NewPresignClient(storage), bucket: os.Getenv("RECEIPTS_BUCKET_NAME")}, Bucket: os.Getenv("RECEIPTS_BUCKET_NAME"), Now: time.Now}
	return app.Handle(ctx, req)
}
