package domain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var ErrUploadConflict = errors.New("upload batch conflict")
var ErrUploadInvalid = errors.New("uploaded object does not match reservation")

type AttachmentPreparationItem struct {
	ClientMessageID string `json:"clientMessageId" dynamodbav:"clientMessageId"`
	FileName        string `json:"fileName" dynamodbav:"fileName"`
	MIMEType        string `json:"mimeType" dynamodbav:"mimeType"`
	Size            int64  `json:"size" dynamodbav:"size"`
	ChecksumSHA256  string `json:"checksumSha256" dynamodbav:"checksumSha256"`
}
type PreparedAttachmentUpload struct {
	ClientMessageID string            `json:"clientMessageId"`
	UploadID        string            `json:"uploadId"`
	Key             string            `json:"key"`
	UploadURL       string            `json:"uploadUrl"`
	Method          string            `json:"method"`
	RequiredHeaders map[string]string `json:"requiredHeaders"`
}
type PrepareAttachmentsResponse struct {
	BatchID   string                     `json:"batchId"`
	ExpiresAt time.Time                  `json:"expiresAt"`
	Uploads   []PreparedAttachmentUpload `json:"uploads"`
}
type uploadRecord struct {
	PK         string    `dynamodbav:"pk"`
	SK         string    `dynamodbav:"sk"`
	OwnerID    string    `dynamodbav:"ownerUserId"`
	Phone      string    `dynamodbav:"phone"`
	BatchID    string    `dynamodbav:"batchId"`
	UploadID   string    `dynamodbav:"uploadId"`
	Key        string    `dynamodbav:"key"`
	Ordinal    int       `dynamodbav:"ordinal"`
	State      string    `dynamodbav:"state"`
	CreatedAt  time.Time `dynamodbav:"createdAt"`
	ValidUntil time.Time `dynamodbav:"validUntil"`
	ExpiresAt  int64     `dynamodbav:"expiresAt"`
	AttachmentPreparationItem
}
type attachmentDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}
type attachmentPresigner interface {
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}
type attachmentStorage interface {
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}
type MediaProvider interface {
	SendMedia(context.Context, string, string, string, string, []byte) (string, error)
}
type StickerProvider interface {
	SendSticker(context.Context, string, string, string, []byte) (string, error)
}
type AttachmentService struct {
	db            attachmentDynamo
	presigner     attachmentPresigner
	storage       attachmentStorage
	repository    SendRepository
	provider      MediaProvider
	table, bucket string
	now           func() time.Time
	newID         func() string
}

func NewAttachmentService(db attachmentDynamo, presigner attachmentPresigner, storage attachmentStorage, repository SendRepository, provider MediaProvider, table, bucket string, now func() time.Time, newID func() string) (*AttachmentService, error) {
	if db == nil || presigner == nil || storage == nil || repository == nil || provider == nil || table == "" || bucket == "" || newID == nil {
		return nil, errors.New("attachment service configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &AttachmentService{db: db, presigner: presigner, storage: storage, repository: repository, provider: provider, table: table, bucket: bucket, now: now, newID: newID}, nil
}

func (s *AttachmentService) Send(ctx context.Context, owner, phone, clientMessageID, uploadID, key, caption string) (SendState, error) {
	return s.send(ctx, owner, phone, clientMessageID, uploadID, key, caption, false)
}

func (s *AttachmentService) SendSticker(ctx context.Context, owner, phone, clientMessageID, uploadID, key string) (SendState, error) {
	return s.send(ctx, owner, phone, clientMessageID, uploadID, key, "", true)
}

func (s *AttachmentService) send(ctx context.Context, owner, phone, clientMessageID, uploadID, key, caption string, sticker bool) (SendState, error) {
	now := s.now().UTC()
	last, err := s.repository.LastCustomerMessageAt(ctx, phone)
	if err != nil {
		return SendState{}, err
	}
	if last == nil || now.Sub(last.UTC()) < 0 || now.Sub(last.UTC()) >= 24*time.Hour {
		return SendState{}, ErrServiceWindow
	}
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("UPLOAD#"+clientMessageID, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil || len(out.Item) == 0 {
		return SendState{}, ErrUploadInvalid
	}
	var record uploadRecord
	if attributevalue.UnmarshalMap(out.Item, &record) != nil || record.OwnerID != owner || record.Phone != phone || record.UploadID != uploadID || record.Key != key || record.State != "reserved" || !now.Before(record.ValidUntil) {
		return SendState{}, ErrUploadInvalid
	}
	if sticker && (record.MIMEType != "image/webp" || record.Size > 500*1024) {
		return SendState{}, ErrUploadInvalid
	}
	head, err := s.storage.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil || head.ContentLength == nil || *head.ContentLength != record.Size || head.ContentType == nil || *head.ContentType != record.MIMEType || head.Metadata["upload-id"] != uploadID {
		return SendState{}, ErrUploadInvalid
	}
	object, err := s.storage.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return SendState{}, fmt.Errorf("get attachment: %w", err)
	}
	defer func() { _ = object.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(object.Body, 7*1024*1024+1))
	if err != nil || int64(len(data)) != record.Size {
		return SendState{}, ErrUploadInvalid
	}
	digest := sha256.Sum256(data)
	if base64.StdEncoding.EncodeToString(digest[:]) != record.ChecksumSHA256 {
		return SendState{}, ErrUploadInvalid
	}
	if sticker && record.Size > stickerMaximumSize(data) {
		return SendState{}, ErrUploadInvalid
	}
	preview := caption
	if preview == "" {
		preview = record.FileName
	}
	kind := "attachment"
	var rich *MessageRichContent
	if sticker {
		kind, rich = "sticker", &MessageRichContent{Kind: "sticker"}
	}
	hash := sha256.Sum256([]byte(phone + "\x00" + kind + "\x00" + uploadID + "\x00" + record.ChecksumSHA256 + "\x00" + caption))
	attachment := &SendAttachment{FileName: record.FileName, MIMEType: record.MIMEType, Size: record.Size, Key: key}
	state, created, err := s.repository.Reserve(ctx, SendReservation{OwnerID: owner, Phone: phone, ClientMessageID: clientMessageID, RequestHash: fmt.Sprintf("%x", hash[:]), Content: preview, Attachment: attachment, Now: now, Rich: rich})
	if err != nil || !created {
		return state, err
	}
	state, err = s.repository.MarkProviderPending(ctx, owner, clientMessageID, now)
	if err != nil {
		return state, err
	}
	var wamid string
	var providerErr error
	if sticker {
		provider, ok := s.provider.(StickerProvider)
		if !ok {
			return state, errors.New("sticker provider is unavailable")
		}
		wamid, providerErr = provider.SendSticker(ctx, phone, record.FileName, record.MIMEType, data)
	} else {
		wamid, providerErr = s.provider.SendMedia(ctx, phone, record.FileName, record.MIMEType, caption, data)
	}
	if providerErr != nil {
		name, message := SendFailed, "El proveedor rechazo el adjunto"
		var classified *ProviderError
		if errors.As(providerErr, &classified) && classified.TransmissionPossible {
			name, message = SendUncertain, "No fue posible confirmar el envio del adjunto"
		}
		failed, persistErr := s.repository.Fail(ctx, owner, clientMessageID, name, message, s.now().UTC())
		if persistErr != nil {
			return state, persistErr
		}
		if name == SendUncertain {
			return failed, nil
		}
		return failed, ErrProviderRejected
	}
	state, err = s.repository.Commit(ctx, owner, clientMessageID, wamid, s.now().UTC())
	if err != nil {
		return state, err
	}
	_, _ = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &s.table, Key: snapshotKey("UPLOAD#"+clientMessageID, "META"), UpdateExpression: aws.String("SET #state=:sent, updatedAt=:now"), ConditionExpression: aws.String("#state=:reserved"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: map[string]types.AttributeValue{":sent": &types.AttributeValueMemberS{Value: "sent"}, ":reserved": &types.AttributeValueMemberS{Value: "reserved"}, ":now": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}}})
	_, _ = s.storage.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return state, nil
}

func stickerMaximumSize(content []byte) int64 {
	if bytes.Contains(content, []byte("ANIM")) {
		return 500 * 1024
	}
	return 100 * 1024
}
func (s *AttachmentService) Prepare(ctx context.Context, owner, phone, batchID string, files []AttachmentPreparationItem) (PrepareAttachmentsResponse, error) {
	now := s.now().UTC()
	validUntil := now.Add(5 * time.Minute)
	terminalTTL := now.Add(24 * time.Hour).Unix()
	writes := make([]types.TransactWriteItem, 0, len(files)+1)
	batch := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "UPLOAD_BATCH#" + owner + "#" + batchID}, "sk": &types.AttributeValueMemberS{Value: "META"}, "ownerUserId": &types.AttributeValueMemberS{Value: owner}, "phone": &types.AttributeValueMemberS{Value: phone}, "batchId": &types.AttributeValueMemberS{Value: batchID}, "state": &types.AttributeValueMemberS{Value: "reserved"}, "createdAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(terminalTTL)}}
	writes = append(writes, types.TransactWriteItem{Put: &types.Put{TableName: &s.table, Item: batch, ConditionExpression: aws.String("attribute_not_exists(pk)")}})
	uploads := make([]PreparedAttachmentUpload, 0, len(files))
	for ordinal, file := range files {
		uploadID := s.newID()
		key := path.Join("temporary", owner, phone, batchID, fmt.Sprintf("%02d-%s", ordinal, uploadID))
		record := uploadRecord{PK: "UPLOAD#" + file.ClientMessageID, SK: "META", OwnerID: owner, Phone: phone, BatchID: batchID, UploadID: uploadID, Key: key, Ordinal: ordinal, State: "reserved", CreatedAt: now, ValidUntil: validUntil, ExpiresAt: terminalTTL, AttachmentPreparationItem: file}
		item, err := attributevalue.MarshalMap(record)
		if err != nil {
			return PrepareAttachmentsResponse{}, fmt.Errorf("encode upload reservation: %w", err)
		}
		writes = append(writes, types.TransactWriteItem{Put: &types.Put{TableName: &s.table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}})
		signed, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, ContentType: &file.MIMEType, ChecksumSHA256: &file.ChecksumSHA256, Metadata: map[string]string{"upload-id": uploadID}}, func(options *s3.PresignOptions) { options.Expires = 5 * time.Minute })
		if err != nil {
			return PrepareAttachmentsResponse{}, fmt.Errorf("presign attachment: %w", err)
		}
		uploads = append(uploads, PreparedAttachmentUpload{ClientMessageID: file.ClientMessageID, UploadID: uploadID, Key: key, UploadURL: signed.URL, Method: "PUT", RequiredHeaders: map[string]string{"Content-Type": file.MIMEType, "x-amz-checksum-sha256": file.ChecksumSHA256, "x-amz-meta-upload-id": uploadID}})
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			return PrepareAttachmentsResponse{}, ErrUploadConflict
		}
		return PrepareAttachmentsResponse{}, fmt.Errorf("reserve attachment batch: %w", err)
	}
	return PrepareAttachmentsResponse{BatchID: batchID, ExpiresAt: validUntil, Uploads: uploads}, nil
}
