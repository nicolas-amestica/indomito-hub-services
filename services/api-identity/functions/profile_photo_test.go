package functions

import (
	"context"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type profilePhotoStoreFake struct {
	body        []byte
	contentType string
	key         string
	deleted     bool
}

func (fake *profilePhotoStoreFake) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	fake.key = aws.ToString(input.Key)
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(string(fake.body))), ContentType: aws.String(fake.contentType)}, nil
}

func (fake *profilePhotoStoreFake) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	fake.key = aws.ToString(input.Key)
	fake.contentType = aws.ToString(input.ContentType)
	fake.body, _ = io.ReadAll(input.Body)
	return &s3.PutObjectOutput{}, nil
}

func (fake *profilePhotoStoreFake) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	fake.key = aws.ToString(input.Key)
	fake.deleted = true
	return &s3.DeleteObjectOutput{}, nil
}

func TestProfilePhotoCRUD(t *testing.T) {
	store := &profilePhotoStoreFake{}
	app := &App{ProfilePhotos: store, PhotosBucket: "photos"}
	request := authenticatedPhotoRequest()
	request.Headers = map[string]string{"content-type": "image/png"}
	request.Body = base64.StdEncoding.EncodeToString([]byte("photo"))
	request.IsBase64Encoded = true

	putResponse, _ := putProfilePhoto(context.Background(), app, request)
	if putResponse.StatusCode != 200 || string(store.body) != "photo" || store.key != "profile-photos/user-1" {
		t.Fatalf("guardado inesperado: status=%d key=%q body=%q", putResponse.StatusCode, store.key, store.body)
	}

	getResponse, _ := getProfilePhoto(context.Background(), app, authenticatedPhotoRequest())
	if getResponse.StatusCode != 200 || !getResponse.IsBase64Encoded || getResponse.Body != base64.StdEncoding.EncodeToString([]byte("photo")) {
		t.Fatalf("lectura inesperada: %+v", getResponse)
	}

	deleteResponse, _ := deleteProfilePhoto(context.Background(), app, authenticatedPhotoRequest())
	if deleteResponse.StatusCode != 200 || !store.deleted {
		t.Fatalf("eliminación inesperada: status=%d deleted=%v", deleteResponse.StatusCode, store.deleted)
	}
}

func TestPutProfilePhotoRejectsInvalidRequests(t *testing.T) {
	app := &App{ProfilePhotos: &profilePhotoStoreFake{}, PhotosBucket: "photos"}

	unauthorized, _ := putProfilePhoto(context.Background(), app, events.APIGatewayV2HTTPRequest{})
	if unauthorized.StatusCode != 401 {
		t.Fatalf("status sin identidad = %d, esperado 401", unauthorized.StatusCode)
	}

	request := authenticatedPhotoRequest()
	request.Headers = map[string]string{"content-type": "image/gif"}
	request.Body = "photo"
	unsupported, _ := putProfilePhoto(context.Background(), app, request)
	if unsupported.StatusCode != 415 {
		t.Fatalf("status de tipo no permitido = %d, esperado 415", unsupported.StatusCode)
	}
}

func authenticatedPhotoRequest() events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "user-1"}}}}
}
