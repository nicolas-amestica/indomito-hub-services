package domain

import (
	"context"
	"testing"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type agentAssetPresignerFake struct {
	get *s3.GetObjectInput
}

func (f *agentAssetPresignerFake) PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return &v4.PresignedHTTPRequest{URL: "https://upload.test"}, nil
}

func (f *agentAssetPresignerFake) PresignGetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.get = input
	return &v4.PresignedHTTPRequest{URL: "https://download.test"}, nil
}

func TestAgentAssetDownloadOnlySignsManagedPDFs(t *testing.T) {
	presigner := &agentAssetPresignerFake{}
	service, err := NewAgentAssetUploadService(presigner, "bucket")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareDownload(context.Background(), "presupuestos/vi/bariloche-4.pdf", "Bariloche Full.pdf")
	if err != nil || result.DownloadURL != "https://download.test" || presigner.get == nil {
		t.Fatalf("valid managed PDF was not signed: result=%#v err=%v", result, err)
	}
	for _, key := range []string{"private/secret.pdf", "presupuestos/../secret.pdf", "presupuestos/file.txt"} {
		if _, err = service.PrepareDownload(context.Background(), key, "file.pdf"); err == nil {
			t.Fatalf("unsafe key %q was accepted", key)
		}
	}
}
