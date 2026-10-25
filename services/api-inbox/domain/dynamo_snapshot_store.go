package domain

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const snapshotWriteBatchSize = 25

type snapshotDynamoClient interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// DynamoListSnapshotStore implementa snapshots inmutables y cuota de tres sesiones.
type DynamoListSnapshotStore struct {
	client       snapshotDynamoClient
	tableName    string
	now          func() time.Time
	newSessionID func() (string, error)
}

// NewDynamoListSnapshotStore construye el repositorio DynamoDB de snapshots.
func NewDynamoListSnapshotStore(client snapshotDynamoClient, tableName string, now func() time.Time) (*DynamoListSnapshotStore, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("configuracion invalida del snapshot store")
	}
	if now == nil {
		now = time.Now
	}
	return &DynamoListSnapshotStore{client: client, tableName: tableName, now: now, newSessionID: randomSessionID}, nil
}

// Create persiste los chunks antes de publicar META y reclamar un slot.
func (store *DynamoListSnapshotStore) Create(ctx context.Context, input ListSnapshotCreate) (ListSnapshotPage, error) {
	sessionID, err := store.newSessionID()
	if err != nil {
		return ListSnapshotPage{}, fmt.Errorf("generate snapshot session: %w", err)
	}
	now := store.now().UTC()
	documents := buildListSnapshotDocuments(input, sessionID, 0, now)
	if err := store.writeChunks(ctx, documents.chunks); err != nil {
		return ListSnapshotPage{}, err
	}

	for slot := 0; slot < 3; slot++ {
		documents.meta.Slot = slot
		claimed, err := store.claimSlot(ctx, sessionID, documents.meta)
		if err != nil {
			return ListSnapshotPage{}, err
		}
		if claimed {
			end := min(ConversationListPageSize, len(input.Items))
			page := snapshotPage(sessionID, documents.meta, 1, input.Items[:end])
			if !page.HasMore {
				if err := store.releaseSlot(ctx, sessionID, documents.meta); err != nil {
					return ListSnapshotPage{}, err
				}
			}
			return page, nil
		}
	}
	return ListSnapshotPage{}, ErrSnapshotLimitReached
}

// ReadPage carga META y los chunks requeridos usando lecturas consistentes.
func (store *DynamoListSnapshotStore) ReadPage(ctx context.Context, request ListSnapshotPageRequest) (ListSnapshotPage, error) {
	meta, err := store.readMeta(ctx, request.SessionID)
	if err != nil {
		return ListSnapshotPage{}, err
	}
	now := store.now().UTC()
	if meta.OwnerID != request.OwnerID || meta.QueryHash != request.QueryHash || meta.Slot != request.Slot {
		return ListSnapshotPage{}, ErrInvalidCursor
	}
	if now.Unix() >= meta.ExpiresAt || !now.Before(meta.AbsoluteExpiresAt) {
		return ListSnapshotPage{}, ErrExpiredCursor
	}
	start := (request.Page - 1) * request.PageSize
	if request.Page < 2 || request.PageSize != ConversationListPageSize || start >= meta.TotalItems {
		return ListSnapshotPage{}, ErrInvalidCursor
	}
	end := min(start+request.PageSize, meta.TotalItems)
	items := make([]ConversationSummary, 0, end-start)
	for chunkIndex := start / listSnapshotChunkSize; chunkIndex <= (end-1)/listSnapshotChunkSize; chunkIndex++ {
		chunk, err := store.readChunk(ctx, request.SessionID, chunkIndex)
		if err != nil {
			return ListSnapshotPage{}, err
		}
		chunkStart := max(start-chunkIndex*listSnapshotChunkSize, 0)
		chunkEnd := min(end-chunkIndex*listSnapshotChunkSize, len(chunk.Items))
		if chunkStart > chunkEnd {
			return ListSnapshotPage{}, ErrInvalidCursor
		}
		items = append(items, chunk.Items[chunkStart:chunkEnd]...)
	}
	page := snapshotPage(request.SessionID, meta, request.Page, items)
	if page.HasMore {
		if err := store.renew(ctx, request.SessionID, meta, now); err != nil {
			return ListSnapshotPage{}, err
		}
	} else if err := store.releaseSlot(ctx, request.SessionID, meta); err != nil {
		return ListSnapshotPage{}, err
	}
	return page, nil
}

func (store *DynamoListSnapshotStore) renew(ctx context.Context, sessionID string, meta listSnapshotMeta, now time.Time) error {
	expiresAt := renewedSnapshotExpiry(now, meta.AbsoluteExpiresAt)
	lastAccessAt, err := attributevalue.Marshal(now)
	if err != nil {
		return fmt.Errorf("encode snapshot access time: %w", err)
	}
	_, err = store.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: &types.Update{TableName: &store.tableName, Key: snapshotKey("LIST_RESULT#"+sessionID, "META"), UpdateExpression: stringPointer("SET expiresAt = :expiresAt, lastAccessAt = :accessedAt"), ConditionExpression: stringPointer("ownerId = :ownerId"), ExpressionAttributeValues: map[string]types.AttributeValue{
			":expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(expiresAt)}, ":accessedAt": lastAccessAt, ":ownerId": &types.AttributeValueMemberS{Value: meta.OwnerID},
		}}},
		{Update: &types.Update{TableName: &store.tableName, Key: snapshotKey("LIST_SLOT#"+meta.OwnerID, fmt.Sprintf("SLOT#%d", meta.Slot)), UpdateExpression: stringPointer("SET expiresAt = :expiresAt"), ConditionExpression: stringPointer("sessionId = :sessionId"), ExpressionAttributeValues: map[string]types.AttributeValue{
			":expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(expiresAt)}, ":sessionId": &types.AttributeValueMemberS{Value: sessionID},
		}}},
	}})
	if err != nil {
		return fmt.Errorf("renew snapshot metadata: %w", err)
	}
	for index := 0; index < meta.ChunkCount; index++ {
		_, err := store.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &store.tableName, Key: snapshotKey("LIST_RESULT#"+sessionID, fmt.Sprintf("CHUNK#%06d", index)), UpdateExpression: stringPointer("SET expiresAt = :expiresAt"), ExpressionAttributeValues: map[string]types.AttributeValue{":expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(expiresAt)}}})
		if err != nil {
			return fmt.Errorf("renew snapshot chunk %d: %w", index, err)
		}
	}
	return nil
}

func (store *DynamoListSnapshotStore) releaseSlot(ctx context.Context, sessionID string, meta listSnapshotMeta) error {
	_, err := store.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:                 &store.tableName,
		Key:                       snapshotKey("LIST_SLOT#"+meta.OwnerID, fmt.Sprintf("SLOT#%d", meta.Slot)),
		ConditionExpression:       stringPointer("sessionId = :sessionId"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":sessionId": &types.AttributeValueMemberS{Value: sessionID}},
	})
	if err != nil {
		return fmt.Errorf("release snapshot slot: %w", err)
	}
	return nil
}

func (store *DynamoListSnapshotStore) writeChunks(ctx context.Context, chunks []listSnapshotChunk) error {
	requests := make([]types.WriteRequest, 0, len(chunks))
	for _, chunk := range chunks {
		item, err := attributevalue.MarshalMap(chunk)
		if err != nil {
			return fmt.Errorf("encode snapshot chunk: %w", err)
		}
		requests = append(requests, types.WriteRequest{PutRequest: &types.PutRequest{Item: item}})
	}
	for start := 0; start < len(requests); start += snapshotWriteBatchSize {
		end := min(start+snapshotWriteBatchSize, len(requests))
		pending := append([]types.WriteRequest(nil), requests[start:end]...)
		for attempt := 0; len(pending) > 0 && attempt < 5; attempt++ {
			output, err := store.client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{store.tableName: pending}})
			if err != nil {
				return fmt.Errorf("write snapshot chunks: %w", err)
			}
			pending = output.UnprocessedItems[store.tableName]
		}
		if len(pending) > 0 {
			return errors.New("write snapshot chunks: unprocessed items exhausted retries")
		}
	}
	return nil
}

func (store *DynamoListSnapshotStore) claimSlot(ctx context.Context, sessionID string, meta listSnapshotMeta) (bool, error) {
	metaItem, err := attributevalue.MarshalMap(meta)
	if err != nil {
		return false, fmt.Errorf("encode snapshot meta: %w", err)
	}
	slotItem, err := attributevalue.MarshalMap(map[string]any{
		"pk": "LIST_SLOT#" + meta.OwnerID, "sk": fmt.Sprintf("SLOT#%d", meta.Slot),
		"sessionId": sessionID, "expiresAt": meta.ExpiresAt,
	})
	if err != nil {
		return false, fmt.Errorf("encode snapshot slot: %w", err)
	}
	_, err = store.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &store.tableName, Item: slotItem, ConditionExpression: stringPointer("attribute_not_exists(sessionId) OR expiresAt < :now"), ExpressionAttributeValues: map[string]types.AttributeValue{":now": &types.AttributeValueMemberN{Value: fmt.Sprint(store.now().Unix())}}}},
		{Put: &types.Put{TableName: &store.tableName, Item: metaItem, ConditionExpression: stringPointer("attribute_not_exists(pk)")}},
	}})
	if err == nil {
		return true, nil
	}
	var canceled *types.TransactionCanceledException
	if errors.As(err, &canceled) {
		for _, reason := range canceled.CancellationReasons {
			if reason.Code != nil && *reason.Code == "ConditionalCheckFailed" {
				return false, nil
			}
		}
	}
	return false, fmt.Errorf("claim snapshot slot: %w", err)
}

func (store *DynamoListSnapshotStore) readMeta(ctx context.Context, sessionID string) (listSnapshotMeta, error) {
	var meta listSnapshotMeta
	err := store.getDocument(ctx, "LIST_RESULT#"+sessionID, "META", &meta)
	return meta, err
}

func (store *DynamoListSnapshotStore) readChunk(ctx context.Context, sessionID string, index int) (listSnapshotChunk, error) {
	var chunk listSnapshotChunk
	err := store.getDocument(ctx, "LIST_RESULT#"+sessionID, fmt.Sprintf("CHUNK#%06d", index), &chunk)
	return chunk, err
}

func (store *DynamoListSnapshotStore) getDocument(ctx context.Context, pk, sk string, target any) error {
	output, err := store.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &store.tableName, ConsistentRead: boolPointer(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk},
	}})
	if err != nil {
		return fmt.Errorf("read snapshot document: %w", err)
	}
	if len(output.Item) == 0 {
		return ErrExpiredCursor
	}
	if err := attributevalue.UnmarshalMap(output.Item, target); err != nil {
		return fmt.Errorf("decode snapshot document: %w", err)
	}
	return nil
}

func snapshotPage(sessionID string, meta listSnapshotMeta, page int, items []ConversationSummary) ListSnapshotPage {
	return ListSnapshotPage{SessionID: sessionID, Slot: meta.Slot, Page: page, Items: append([]ConversationSummary(nil), items...), TotalItems: meta.TotalItems, UnreadTotal: meta.UnreadTotal, Cutoff: meta.Cutoff, CreatedAt: meta.CreatedAt, AbsoluteExpiresAt: meta.AbsoluteExpiresAt, HasMore: page*ConversationListPageSize < meta.TotalItems}
}

func randomSessionID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func snapshotKey(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}

var _ ListSnapshotStore = (*DynamoListSnapshotStore)(nil)
