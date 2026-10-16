package domain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type inboundMediaS3 interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type MetaInboundMediaStore struct {
	client        *http.Client
	storage       inboundMediaS3
	token, bucket string
	maxBytes      int64
}

func NewMetaInboundMediaStore(client *http.Client, storage inboundMediaS3, token, bucket string) (*MetaInboundMediaStore, error) {
	if client == nil {
		client = &http.Client{}
	}
	if storage == nil || token == "" || bucket == "" {
		return nil, errors.New("inbound media configuration is required")
	}
	return &MetaInboundMediaStore{client: client, storage: storage, token: token, bucket: bucket, maxBytes: 32 << 20}, nil
}

func (s *MetaInboundMediaStore) Materialize(ctx context.Context, phone, messageID, mediaID, mimeType, fileName, webhookSHA string) (MessageAttachment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.facebook.com/v21.0/"+mediaID, nil)
	if err != nil {
		return MessageAttachment{}, err
	}
	req.Header.Set("authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return MessageAttachment{}, fmt.Errorf("resolve meta media: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return MessageAttachment{}, fmt.Errorf("resolve meta media: status %d", resp.StatusCode)
	}
	var metadata struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		Sha256   string `json:"sha256"`
		FileSize int64  `json:"file_size"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&metadata); err != nil || metadata.URL == "" {
		return MessageAttachment{}, errors.New("invalid meta media metadata")
	}
	if metadata.FileSize > s.maxBytes {
		return MessageAttachment{}, errors.New("inbound media exceeds size limit")
	}
	download, err := http.NewRequestWithContext(ctx, http.MethodGet, metadata.URL, nil)
	if err != nil {
		return MessageAttachment{}, err
	}
	download.Header.Set("authorization", "Bearer "+s.token)
	body, err := s.client.Do(download)
	if err != nil {
		return MessageAttachment{}, fmt.Errorf("download meta media: %w", err)
	}
	defer func() { _ = body.Body.Close() }()
	if body.StatusCode/100 != 2 {
		return MessageAttachment{}, fmt.Errorf("download meta media: status %d", body.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(body.Body, s.maxBytes+1))
	if err != nil {
		return MessageAttachment{}, err
	}
	if int64(len(content)) > s.maxBytes {
		return MessageAttachment{}, errors.New("inbound media exceeds size limit")
	}
	if mimeType == "" {
		mimeType = metadata.MimeType
	}
	if fileName == "" {
		fileName = messageID + extension(mimeType)
	}
	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	actualBase64 := base64.StdEncoding.EncodeToString(sum[:])
	expected := webhookSHA
	if expected == "" {
		expected = metadata.Sha256
	}
	if expected != "" && expected != actualBase64 && strings.ToLower(expected) != actual {
		return MessageAttachment{}, errors.New("inbound media checksum mismatch")
	}
	key := "inbound/" + phone + "/" + messageID + "/" + filepath.Base(fileName)
	_, err = s.storage.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(content), ContentType: aws.String(mimeType), Metadata: map[string]string{"sha256": actual, "meta-media-id": mediaID}})
	if err != nil {
		return MessageAttachment{}, fmt.Errorf("store inbound media: %w", err)
	}
	return MessageAttachment{FileName: fileName, MIMEType: mimeType, Size: int64(len(content)), Bucket: s.bucket, Key: key}, nil
}

func extension(mime string) string {
	switch strings.Split(mime, ";")[0] {
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4":
		return ".m4a"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "video/mp4":
		return ".mp4"
	case "application/pdf":
		return ".pdf"
	}
	return ".bin"
}
