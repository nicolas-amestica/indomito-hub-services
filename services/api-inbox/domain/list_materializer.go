package domain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"golang.org/x/text/unicode/norm"
)

const (
	listShardCount     = 16
	listQueryWorkers   = 4
	listSequencePK     = "SEQ#LIST"
	listSequenceSK     = "META"
	listVersionPKLabel = "LIST_VERSION#"
)

var ErrListCapacityExceeded = errors.New("inbox list capacity exceeded")

// ListReader contiene sólo las operaciones de DynamoDB necesarias para materializar la bandeja.
type ListReader interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

type listSequence struct {
	Sequence uint64 `dynamodbav:"sequence"`
}

type ListVersion struct {
	Phone             string              `dynamodbav:"phone"`
	ValidFromSequence uint64              `dynamodbav:"validFromSequence"`
	Operation         string              `dynamodbav:"operation"`
	Summary           ConversationSummary `dynamodbav:"summary"`
}

// ReadListCutoff obtiene el watermark que congela la vista lógica de la lista.
func ReadListCutoff(ctx context.Context, reader ListReader, tableName string) (uint64, error) {
	output, err := reader.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      &tableName,
		ConsistentRead: boolPointer(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: listSequencePK},
			"sk": &types.AttributeValueMemberS{Value: listSequenceSK},
		},
		ProjectionExpression: stringPointer("#sequence"),
		ExpressionAttributeNames: map[string]string{
			"#sequence": "sequence",
		},
	})
	if err != nil {
		return 0, fmt.Errorf("read list cutoff: %w", err)
	}
	if len(output.Item) == 0 {
		return 0, errors.New("read list cutoff: sequence item not found")
	}

	var sequence listSequence
	if err := attributevalue.UnmarshalMap(output.Item, &sequence); err != nil {
		return 0, fmt.Errorf("decode list cutoff: %w", err)
	}
	return sequence.Sequence, nil
}

// MaterializeConversationList reconstruye la lista a un cutoff consultando las 16 particiones.
func MaterializeConversationList(ctx context.Context, reader ListReader, tableName string, cutoff uint64, search string, maxItems int) ([]ConversationSummary, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type shardResult struct {
		versions []ListVersion
		err      error
	}

	jobs := make(chan int)
	results := make(chan shardResult, listShardCount)
	var workers sync.WaitGroup
	workers.Add(listQueryWorkers)
	for range listQueryWorkers {
		go func() {
			defer workers.Done()
			for shard := range jobs {
				versions, err := queryListShard(ctx, reader, tableName, shard)
				select {
				case results <- shardResult{versions: versions, err: err}:
				case <-ctx.Done():
					return
				}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for shard := range listShardCount {
			select {
			case jobs <- shard:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	latest := make(map[string]ListVersion)
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("query list versions: %w", result.err)
		}
		for _, version := range result.versions {
			if version.ValidFromSequence > cutoff || version.Phone == "" {
				continue
			}
			current, exists := latest[version.Phone]
			if !exists || version.ValidFromSequence > current.ValidFromSequence {
				latest[version.Phone] = version
			}
		}
	}

	normalizedSearch := normalizeSearch(search)
	items := make([]ConversationSummary, 0, len(latest))
	for _, version := range latest {
		if version.Operation == "delete" || !matchesSearch(version.Summary, normalizedSearch) {
			continue
		}
		items = append(items, version.Summary)
		if maxItems > 0 && len(items) > maxItems {
			return nil, ErrListCapacityExceeded
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		left, right := conversationActivity(items[i]), conversationActivity(items[j])
		if left.Equal(right) {
			return items[i].Phone < items[j].Phone
		}
		return left.After(right)
	})
	return items, nil
}

func queryListShard(ctx context.Context, reader ListReader, tableName string, shard int) ([]ListVersion, error) {
	partition := fmt.Sprintf("%s%02X", listVersionPKLabel, shard)
	var versions []ListVersion
	var startKey map[string]types.AttributeValue
	for {
		output, err := reader.Query(ctx, &dynamodb.QueryInput{
			TableName:              &tableName,
			ConsistentRead:         boolPointer(true),
			KeyConditionExpression: stringPointer("pk = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: partition},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("shard %02X: %w", shard, err)
		}
		var page []ListVersion
		if err := attributevalue.UnmarshalListOfMaps(output.Items, &page); err != nil {
			return nil, fmt.Errorf("decode shard %02X: %w", shard, err)
		}
		versions = append(versions, page...)
		if len(output.LastEvaluatedKey) == 0 {
			return versions, nil
		}
		startKey = output.LastEvaluatedKey
	}
}

func normalizeSearch(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(value))
	withoutDiacritics := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, decomposed)
	return strings.Join(strings.Fields(withoutDiacritics), " ")
}

func matchesSearch(summary ConversationSummary, search string) bool {
	if search == "" || strings.Contains(normalizeSearch(summary.Alias), search) || strings.Contains(normalizeSearch(summary.Phone), search) {
		return true
	}
	return summary.Referral != nil && (strings.Contains(normalizeSearch(summary.Referral.Headline), search) || strings.Contains(normalizeSearch(summary.Referral.SourceID), search))
}

func conversationActivity(summary ConversationSummary) time.Time {
	if summary.LastMessageAt != nil {
		return *summary.LastMessageAt
	}
	if summary.UpdatedAt != nil {
		return *summary.UpdatedAt
	}
	if summary.CreatedAt != nil {
		return *summary.CreatedAt
	}
	return time.Time{}
}

func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }
