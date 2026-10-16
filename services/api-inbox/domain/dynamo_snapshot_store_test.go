package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeSnapshotDynamo struct {
	documents      map[string]map[string]types.AttributeValue
	batchSizes     []int
	transactionTry int
	failSlots      int
	updates        int
	deletes        int
}

func (fake *fakeSnapshotDynamo) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	fake.updates++
	return &dynamodb.UpdateItemOutput{}, nil
}

func (fake *fakeSnapshotDynamo) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	fake.deletes++
	return &dynamodb.DeleteItemOutput{}, nil
}

func (fake *fakeSnapshotDynamo) BatchWriteItem(_ context.Context, input *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	requests := input.RequestItems["table"]
	fake.batchSizes = append(fake.batchSizes, len(requests))
	for _, request := range requests {
		item := request.PutRequest.Item
		fake.documents[documentKey(item)] = item
	}
	return &dynamodb.BatchWriteItemOutput{}, nil
}

func (fake *fakeSnapshotDynamo) TransactWriteItems(_ context.Context, input *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	fake.transactionTry++
	if fake.transactionTry <= fake.failSlots {
		code := "ConditionalCheckFailed"
		return nil, &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: &code}}}
	}
	for _, operation := range input.TransactItems {
		if operation.Put != nil {
			fake.documents[documentKey(operation.Put.Item)] = operation.Put.Item
		}
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func (fake *fakeSnapshotDynamo) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: fake.documents[documentKey(input.Key)]}, nil
}

func TestDynamoListSnapshotStoreCreateClaimsNextAvailableSlot(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	fake := newFakeSnapshotDynamo()
	fake.failSlots = 1
	store, _ := NewDynamoListSnapshotStore(fake, "table", func() time.Time { return now })
	store.newSessionID = func() (string, error) { return "session-1", nil }
	items := make([]ConversationSummary, 201)
	for index := range items {
		items[index] = ConversationSummary{Phone: string(rune('a' + index%20)), UnreadCount: 1}
	}

	page, err := store.Create(context.Background(), ListSnapshotCreate{OwnerID: "owner", QueryHash: "query", Cutoff: 9, Items: items})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if page.Slot != 1 || page.Page != 1 || len(page.Items) != 200 || !page.HasMore || page.UnreadTotal != 201 {
		t.Fatalf("page = %#v", page)
	}
	if len(fake.batchSizes) != 1 || fake.batchSizes[0] != 3 || fake.transactionTry != 2 {
		t.Fatalf("batches = %v, transactions = %d", fake.batchSizes, fake.transactionTry)
	}
}

func TestDynamoListSnapshotStoreCreateEnforcesThreeSlots(t *testing.T) {
	fake := newFakeSnapshotDynamo()
	fake.failSlots = 3
	store, _ := NewDynamoListSnapshotStore(fake, "table", time.Now)
	store.newSessionID = func() (string, error) { return "session", nil }
	_, err := store.Create(context.Background(), ListSnapshotCreate{OwnerID: "owner", QueryHash: "query"})
	if !errors.Is(err, ErrSnapshotLimitReached) || fake.transactionTry != 3 {
		t.Fatalf("error = %v, transactions = %d", err, fake.transactionTry)
	}
}

func TestDynamoListSnapshotStoreReadPageUsesFrozenChunks(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	fake := newFakeSnapshotDynamo()
	store, _ := NewDynamoListSnapshotStore(fake, "table", func() time.Time { return now })
	items := make([]ConversationSummary, 401)
	for index := range items {
		items[index] = ConversationSummary{Phone: string(rune(index + 1))}
	}
	documents := buildListSnapshotDocuments(ListSnapshotCreate{OwnerID: "owner", QueryHash: "query", Cutoff: 10, Items: items}, "session", 2, now)
	meta, _ := attributevalue.MarshalMap(documents.meta)
	fake.documents[documentKey(meta)] = meta
	for _, chunk := range documents.chunks {
		item, _ := attributevalue.MarshalMap(chunk)
		fake.documents[documentKey(item)] = item
	}

	page, err := store.ReadPage(context.Background(), ListSnapshotPageRequest{OwnerID: "owner", QueryHash: "query", SessionID: "session", Slot: 2, Page: 2, PageSize: 200})
	if err != nil {
		t.Fatalf("ReadPage() error = %v", err)
	}
	if len(page.Items) != 200 || page.Items[0].Phone != items[200].Phone || page.Items[199].Phone != items[399].Phone || !page.HasMore {
		t.Fatalf("page = %#v", page)
	}
	if fake.updates != documents.meta.ChunkCount {
		t.Fatalf("renewed chunks = %d, want %d", fake.updates, documents.meta.ChunkCount)
	}
}

func TestDynamoListSnapshotStoreReleasesSlotAfterLastPage(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	fake := newFakeSnapshotDynamo()
	store, _ := NewDynamoListSnapshotStore(fake, "table", func() time.Time { return now })
	items := make([]ConversationSummary, 201)
	documents := buildListSnapshotDocuments(ListSnapshotCreate{OwnerID: "owner", QueryHash: "query", Items: items}, "session", 1, now)
	meta, _ := attributevalue.MarshalMap(documents.meta)
	fake.documents[documentKey(meta)] = meta
	for _, chunk := range documents.chunks {
		item, _ := attributevalue.MarshalMap(chunk)
		fake.documents[documentKey(item)] = item
	}

	page, err := store.ReadPage(context.Background(), ListSnapshotPageRequest{OwnerID: "owner", QueryHash: "query", SessionID: "session", Slot: 1, Page: 2, PageSize: 200})
	if err != nil || len(page.Items) != 1 || page.HasMore || fake.deletes != 1 {
		t.Fatalf("page = %#v, deletes = %d, error = %v", page, fake.deletes, err)
	}
}

func newFakeSnapshotDynamo() *fakeSnapshotDynamo {
	return &fakeSnapshotDynamo{documents: make(map[string]map[string]types.AttributeValue)}
}

func documentKey(item map[string]types.AttributeValue) string {
	pk := item["pk"].(*types.AttributeValueMemberS).Value
	sk := item["sk"].(*types.AttributeValueMemberS).Value
	return pk + "|" + sk
}
