package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const approvedPDFContentType = "application/pdf"

type DocumentStore interface {
	PutPDF(context.Context, string, []byte) error
	VerifyPDF(context.Context, string, string, int64) error
	PresignPDF(context.Context, string, time.Duration) (string, error)
}

type SignedDocumentStore interface {
	PresignSignedPDFUpload(context.Context, string, time.Duration) (string, error)
	VerifySignedPDF(context.Context, string, string, int64) error
}

func (s *s3DocumentStore) VerifyPDF(ctx context.Context, key, expectedSHA256 string, expectedSize int64) error {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("leer PDF aprobado: %w", err)
	}
	defer result.Body.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, result.Body)
	if err != nil {
		return fmt.Errorf("verificar PDF aprobado: %w", err)
	}
	if size != expectedSize || hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return fmt.Errorf("la integridad del PDF aprobado no coincide")
	}
	return nil
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

func (s *s3DocumentStore) PresignSignedPDFUpload(ctx context.Context, key string, duration time.Duration) (string, error) {
	result, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: aws.String(approvedPDFContentType),
	}, s3.WithPresignExpires(duration))
	if err != nil {
		return "", fmt.Errorf("firmar URL de carga del contrato firmado: %w", err)
	}
	return result.URL, nil
}

func (s *s3DocumentStore) VerifySignedPDF(ctx context.Context, key, expectedSHA256 string, expectedSize int64) error {
	if expectedSize < 5 || expectedSize > maxSignedPDFSize {
		return fmt.Errorf("tamaño de PDF firmado fuera de rango")
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("leer PDF firmado: %w", err)
	}
	defer result.Body.Close()
	if result.ContentType == nil || *result.ContentType != approvedPDFContentType {
		return fmt.Errorf("el archivo cargado no declara application/pdf")
	}
	content, err := io.ReadAll(io.LimitReader(result.Body, maxSignedPDFSize+1))
	if err != nil {
		return fmt.Errorf("leer PDF firmado: %w", err)
	}
	if int64(len(content)) != expectedSize || int64(len(content)) > maxSignedPDFSize {
		return fmt.Errorf("el tamaño del PDF firmado no coincide")
	}
	if !bytes.HasPrefix(content, []byte("%PDF-")) {
		return fmt.Errorf("el archivo no contiene una cabecera PDF válida")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return fmt.Errorf("la huella del PDF firmado no coincide")
	}
	return nil
}
