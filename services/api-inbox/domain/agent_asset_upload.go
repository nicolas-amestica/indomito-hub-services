package domain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var managedAssetNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type agentAssetPresigner interface {
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type AgentAssetUploadService struct {
	presigner agentAssetPresigner
	bucket    string
	now       func() time.Time
}
type AgentAssetUpload struct {
	Key       string    `json:"key"`
	UploadURL string    `json:"uploadUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func NewAgentAssetUploadService(presigner agentAssetPresigner, bucket string) (*AgentAssetUploadService, error) {
	if presigner == nil || bucket == "" {
		return nil, errors.New("agent asset upload configuration is required")
	}
	return &AgentAssetUploadService{presigner: presigner, bucket: bucket, now: time.Now}, nil
}

func (s *AgentAssetUploadService) Prepare(ctx context.Context, fileName string, size int64) (AgentAssetUpload, error) {
	fileName = strings.TrimSpace(fileName)
	if !strings.HasSuffix(strings.ToLower(fileName), ".pdf") || size < 1 || size > 32<<20 {
		return AgentAssetUpload{}, errors.New("only PDF files up to 32 MiB are allowed")
	}
	safe := strings.Trim(managedAssetNamePattern.ReplaceAllString(fileName, "-"), "-.")
	if safe == "" {
		return AgentAssetUpload{}, errors.New("invalid file name")
	}
	now := s.now().UTC()
	key := fmt.Sprintf("programas/gestionados/%s/%s", now.Format("20060102T150405.000000000Z"), safe)
	out, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, ContentType: aws.String("application/pdf"), ContentLength: aws.Int64(size)}, func(options *s3.PresignOptions) { options.Expires = 10 * time.Minute })
	if err != nil {
		return AgentAssetUpload{}, fmt.Errorf("presign agent asset: %w", err)
	}
	return AgentAssetUpload{Key: key, UploadURL: out.URL, ExpiresAt: now.Add(10 * time.Minute)}, nil
}
