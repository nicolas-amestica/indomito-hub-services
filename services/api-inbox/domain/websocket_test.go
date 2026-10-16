package domain

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type websocketFake struct {
	items  map[string]map[string]types.AttributeValue
	writes [][]types.TransactWriteItem
}

func (f *websocketFake) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := input.Key["pk"].(*types.AttributeValueMemberS).Value
	return &dynamodb.GetItemOutput{Item: f.items[pk]}, nil
}

func (f *websocketFake) TransactWriteItems(_ context.Context, input *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.writes = append(f.writes, input.TransactItems)
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestWebSocketConnectionConsumesTicketAndCreatesLookup(t *testing.T) {
	raw := []byte("12345678901234567890123456789012")
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256(raw)
	pk := "TICKET#" + hex.EncodeToString(digest[:])
	fake := &websocketFake{items: map[string]map[string]types.AttributeValue{pk: {
		"ownerUserId": &types.AttributeValueMemberS{Value: "user"}, "scope": &types.AttributeValueMemberS{Value: "detail"}, "phone": &types.AttributeValueMemberS{Value: "56912345678"},
	}}}
	service, _ := NewWebSocketConnectionService(fake, "table", func() time.Time { return time.Unix(100, 0).UTC() })
	if err := service.Connect(context.Background(), "connection", ticket); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 1 || len(fake.writes[0]) != 3 {
		t.Fatalf("expected atomic ticket/connection/subscription transaction: %#v", fake.writes)
	}
	assertMutationPutPK(t, fake.writes[0], "WS_CONN#connection")
	assertMutationPutPK(t, fake.writes[0], "WS_SUB#DETAIL#56912345678")
}

func TestWebSocketDisconnectIsIdempotent(t *testing.T) {
	fake := &websocketFake{items: map[string]map[string]types.AttributeValue{}}
	service, _ := NewWebSocketConnectionService(fake, "table", time.Now)
	if err := service.Disconnect(context.Background(), "missing"); err != nil || len(fake.writes) != 0 {
		t.Fatalf("missing disconnect must succeed without writes: %v", err)
	}
}
