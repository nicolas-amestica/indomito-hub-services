package functions

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const approvedPDFContentType = "application/pdf"

type DocumentStore interface {
	PutPDF(context.Context, string, []byte) error
	PresignPDF(context.Context, string, time.Duration) (string, error)
}

type s3DocumentStore struct {
	bucket    string
	client    *s3.Client
	presigner *s3.PresignClient
}

func newS3DocumentStore(cfg aws.Config, bucket string) DocumentStore {
	client := s3.NewFromConfig(cfg)
	return &s3DocumentStore{bucket: bucket, client: client, presigner: s3.NewPresignClient(client)}
}

func (s *s3DocumentStore) PutPDF(ctx context.Context, key string, content []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(s.bucket),
		Key:                aws.String(key),
		Body:               bytes.NewReader(content),
		ContentType:        aws.String(approvedPDFContentType),
		ContentDisposition: aws.String(`inline; filename="contrato-prestacion-servicios.pdf"`),
	})
	if err != nil {
		return fmt.Errorf("guardar PDF aprobado: %w", err)
	}
	return nil
}

func (s *s3DocumentStore) PresignPDF(ctx context.Context, key string, duration time.Duration) (string, error) {
	result, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentType:        aws.String(approvedPDFContentType),
		ResponseContentDisposition: aws.String(`inline; filename="contrato-prestacion-servicios.pdf"`),
	}, s3.WithPresignExpires(duration))
	if err != nil {
		return "", fmt.Errorf("firmar URL del PDF aprobado: %w", err)
	}
	return result.URL, nil
}
