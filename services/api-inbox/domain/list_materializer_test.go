package domain

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeListReader struct {
	mu          sync.Mutex
	items       map[string][]ListVersion
	queries     map[string]int
	active      int
	maxActive   int
	delay       time.Duration
	getInput    *dynamodb.GetItemInput
	queryInputs []*dynamodb.QueryInput
}

func (fake *fakeListReader) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	fake.getInput = input
	item, err := attributevalue.MarshalMap(listSequence{Sequence: 42})
	return &dynamodb.GetItemOutput{Item: item}, err
}

func (fake *fakeListReader) Query(ctx context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	partition := input.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value
	fake.mu.Lock()
	fake.active++
	if fake.active > fake.maxActive {
		fake.maxActive = fake.active
	}
	fake.queries[partition]++
	page := fake.queries[partition]
	fake.queryInputs = append(fake.queryInputs, input)
	fake.mu.Unlock()

	select {
	case <-time.After(fake.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	fake.mu.Lock()
	fake.active--
	fake.mu.Unlock()
	versions := fake.items[partition]
	if page > 1 || len(versions) == 0 {
		return &dynamodb.QueryOutput{}, nil
	}
	maps := make([]map[string]types.AttributeValue, 0, len(versions))
	for _, version := range versions {
		item, err := attributevalue.MarshalMap(version)
		if err != nil {
			return nil, err
		}
		maps = append(maps, item)
	}
	output := &dynamodb.QueryOutput{Items: maps}
	if partition == "LIST_VERSION#00" && input.ExclusiveStartKey == nil {
		output.LastEvaluatedKey = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: partition}}
	}
	return output, nil
}

func TestReadListCutoffUsesConsistentRead(t *testing.T) {
	fake := newFakeListReader()
	cutoff, err := ReadListCutoff(context.Background(), fake, "inbox-table")
	if err != nil {
		t.Fatalf("ReadListCutoff() error = %v", err)
	}
	if cutoff != 42 {
		t.Fatalf("cutoff = %d, want 42", cutoff)
	}
	if fake.getInput.ConsistentRead == nil || !*fake.getInput.ConsistentRead {
		t.Fatal("GetItem must use a consistent read")
	}
	if got := fake.getInput.Key["pk"].(*types.AttributeValueMemberS).Value; got != listSequencePK {
		t.Fatalf("pk = %q, want %q", got, listSequencePK)
	}
}

func TestMaterializeConversationListSelectsFiltersAndSorts(t *testing.T) {
	fake := newFakeListReader()
	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	fake.items["LIST_VERSION#00"] = []ListVersion{
		{Phone: "+56911111111", ValidFromSequence: 1, Operation: "upsert", Summary: ConversationSummary{Phone: "+56911111111", Alias: "Álvaro", LastMessageAt: &older}},
		{Phone: "+56911111111", ValidFromSequence: 3, Operation: "upsert", Summary: ConversationSummary{Phone: "+56911111111", Alias: "Álvaro", LastMessageAt: &newer}},
		{Phone: "+56911111111", ValidFromSequence: 9, Operation: "delete"},
		{Phone: "+56922222222", ValidFromSequence: 2, Operation: "delete", Summary: ConversationSummary{Phone: "+56922222222", Alias: "Alvaro borrado"}},
	}
	fake.items["LIST_VERSION#01"] = []ListVersion{
		{Phone: "+56933333333", ValidFromSequence: 2, Operation: "upsert", Summary: ConversationSummary{Phone: "+56933333333", Alias: "Álvaro dos", LastMessageAt: &older}},
	}

	items, err := MaterializeConversationList(context.Background(), fake, "inbox-table", 5, "alvaro", 100)
	if err != nil {
		t.Fatalf("MaterializeConversationList() error = %v", err)
	}
	if len(items) != 2 || items[0].Phone != "+56911111111" || items[1].Phone != "+56933333333" {
		t.Fatalf("items = %#v", items)
	}
	assertShardQueries(t, fake)
}

func TestMaterializeConversationListSearchesMetaAdAttribution(t *testing.T) {
	fake := newFakeListReader()
	fake.items["LIST_VERSION#00"] = []ListVersion{{Phone: "+56911111111", ValidFromSequence: 1, Operation: "upsert", Summary: ConversationSummary{Phone: "+56911111111", Alias: "Ana", Referral: &MetaReferral{SourceID: "1200001", Headline: "Gira a Bariloche"}}}}

	items, err := MaterializeConversationList(context.Background(), fake, "inbox-table", 1, "bariloche", 100)
	if err != nil {
		t.Fatalf("MaterializeConversationList() error = %v", err)
	}
	if len(items) != 1 || items[0].Referral == nil || items[0].Referral.SourceID != "1200001" {
		t.Fatalf("items = %#v", items)
	}
}

func TestMaterializeConversationListLimitsConcurrencyAndCapacity(t *testing.T) {
	fake := newFakeListReader()
	fake.delay = 5 * time.Millisecond
	for shard := range listShardCount {
		phone := fmt.Sprintf("+569%08d", shard)
		fake.items[fmt.Sprintf("LIST_VERSION#%02X", shard)] = []ListVersion{{Phone: phone, ValidFromSequence: 1, Operation: "upsert", Summary: ConversationSummary{Phone: phone}}}
	}

	_, err := MaterializeConversationList(context.Background(), fake, "inbox-table", 1, "", 15)
	if err != ErrListCapacityExceeded {
		t.Fatalf("error = %v, want ErrListCapacityExceeded", err)
	}
	if fake.maxActive > listQueryWorkers {
		t.Fatalf("max concurrent queries = %d, want <= %d", fake.maxActive, listQueryWorkers)
	}
	if fake.maxActive != listQueryWorkers {
		t.Fatalf("max concurrent queries = %d, want %d", fake.maxActive, listQueryWorkers)
	}
}

func newFakeListReader() *fakeListReader {
	return &fakeListReader{items: make(map[string][]ListVersion), queries: make(map[string]int)}
}

func assertShardQueries(t *testing.T, fake *fakeListReader) {
	t.Helper()
	for shard := range listShardCount {
		partition := fmt.Sprintf("LIST_VERSION#%02X", shard)
		if fake.queries[partition] == 0 {
			t.Errorf("partition %s was not queried", partition)
		}
	}
	for _, input := range fake.queryInputs {
		if input.ConsistentRead == nil || !*input.ConsistentRead {
			t.Error("every Query must use a consistent read")
		}
	}
}
