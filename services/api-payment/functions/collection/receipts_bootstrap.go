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

type s3ReceiptSigner struct {
	client *s3.PresignClient
	bucket string
}

func (s s3ReceiptSigner) Download(ctx context.Context, key string) (string, error) {
	result, err := s.client.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key, ResponseContentType: aws.String("application/pdf"), ResponseContentDisposition: aws.String(`attachment; filename="comprobante.pdf"`)}, func(o *s3.PresignOptions) { o.Expires = 2 * time.Minute })
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

// ReceiptsHandler no lee claves Khipu ni SMTP y no tiene permisos de escritura financiera.
func ReceiptsHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.Authorizer == nil {
		return portalFailure(403), nil
	}
	access := req.RequestContext.Authorizer.Lambda["paymentAccess"]
	if access != "passenger" && access != "admin" {
		return portalFailure(403), nil
	}
	if os.Getenv("APP_STAGE") != "dev" || os.Getenv("PAYMENTS_TABLE_NAME") == "" || os.Getenv("RECEIPTS_BUCKET_NAME") == "" {
		return portalFailure(503), nil
	}
	cfg, err := bootstrap.LoadAWSConfig(ctx, bootstrap.LoadConfig())
	if err != nil {
		return portalFailure(503), nil
	}
	a := ReceiptsApp{Accounts: Service{DB: dynamodb.NewFromConfig(cfg), Table: os.Getenv("PAYMENTS_TABLE_NAME")}, Signer: s3ReceiptSigner{client: s3.NewPresignClient(s3.NewFromConfig(cfg)), bucket: os.Getenv("RECEIPTS_BUCKET_NAME")}, Now: time.Now}
	if access == "passenger" {
		if req.RequestContext.HTTP.Method != "POST" {
			return portalFailure(403), nil
		}
		return a.HandlePortalResend(ctx, req), nil
	}
	if req.PathParameters["tripId"] != "" {
		if req.PathParameters["id"] != "" {
			return a.HandleGroupDownload(ctx, req), nil
		}
		return a.HandleGroupList(ctx, req), nil
	}
	if req.RequestContext.HTTP.Method == "POST" {
		return a.HandleAdminResend(ctx, req), nil
	}
	if req.PathParameters["id"] != "" {
		return a.HandleDownload(ctx, req), nil
	}
	return a.HandleList(ctx, req), nil
}
