package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
)

const mutationMaxAttempts = 5

type conversationMutationDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// DeleteConversationResult confirms logical deletion; repeated deletion is successful.
type DeleteConversationResult struct {
	Deleted bool `json:"deleted"`
}

// ConversationMutationService owns atomic visible mutations and their versioned projections.
type ConversationMutationService struct {
	client    conversationMutationDynamo
	tableName string
	now       func() time.Time
	newID     func(time.Time) (string, error)
}

// NewConversationMutationService creates the transactional conversation mutation service.
func NewConversationMutationService(client conversationMutationDynamo, tableName string, now func() time.Time) (*ConversationMutationService, error) {
	if client == nil || tableName == "" {
		return nil, errors.New("conversation mutation configuration is required")
	}
	if now == nil {
		now = time.Now
	}
	return &ConversationMutationService{client: client, tableName: tableName, now: now, newID: func(at time.Time) (string, error) {
		id, err := ulid.New(ulid.Timestamp(at), rand.Reader)
		return id.String(), err
	}}, nil
}

// MarkUnread sets unreadCount to one and publishes versioned list/search projections atomically.
func (s *ConversationMutationService) MarkUnread(ctx context.Context, phone string) (ConversationSummary, error) {
	return s.SetUnreadCount(ctx, phone, 1)
}

// MarkRead clears the unread counter and publishes updated projections.
func (s *ConversationMutationService) MarkRead(ctx context.Context, phone string) (ConversationSummary, error) {
	return s.SetUnreadCount(ctx, phone, 0)
}

// Derive transfers a conversation to the human team and publishes both list and detail updates.
func (s *ConversationMutationService) Derive(ctx context.Context, phone, reason, reasonLabel string) (ConversationSummary, error) {
	reason = normalizeDerivationText(reason, 500)
	reasonLabel = normalizeDerivationText(reasonLabel, 120)
	if reason == "" {
		reason = "agent_handoff"
	}
	if reasonLabel == "" {
		reasonLabel = "Requiere atencion del equipo"
	}
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		summary, listSequence, detailSequence, found, err := s.readState(ctx, phone)
		if err != nil {
			return ConversationSummary{}, err
		}
		if !found {
			return ConversationSummary{}, ErrConversationNotFound
		}
		if summary.Status == ConversationStatusDerived && summary.Reason != nil && *summary.Reason == reason {
			return summary, nil
		}
		now := s.now().UTC()
		summary.Status = ConversationStatusDerived
		summary.Reason = &reason
		summary.ReasonLabel = &reasonLabel
		if summary.UnreadCount < 1 {
			summary.UnreadCount = 1
		}
		summary.UpdatedAt = &now
		summary.Generation++
		writes, writeErr := s.visibleWrites(summary, "upsert", listSequence, detailSequence, "conversation.updated", now)
		if writeErr != nil {
			return ConversationSummary{}, writeErr
		}
		if err = s.transact(ctx, writes); err == nil {
			return summary, nil
		}
		if !isTransactionConflict(err) {
			return ConversationSummary{}, fmt.Errorf("derive conversation: %w", err)
		}
	}
	return ConversationSummary{}, errors.New("derive conversation: concurrent update limit reached")
}

// ActivateBot returns a technically failed conversation to the automated agent.
func (s *ConversationMutationService) ActivateBot(ctx context.Context, phone string) (ConversationSummary, error) {
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		summary, listSequence, detailSequence, found, err := s.readState(ctx, phone)
		if err != nil {
			return ConversationSummary{}, err
		}
		if !found {
			return ConversationSummary{}, ErrConversationNotFound
		}
		if summary.Status == ConversationStatusBot && summary.Reason == nil && summary.ReasonLabel == nil {
			return summary, nil
		}
		now := s.now().UTC()
		summary.Status = ConversationStatusBot
		summary.Reason = nil
		summary.ReasonLabel = nil
		summary.UpdatedAt = &now
		summary.Generation++
		writes, writeErr := s.visibleWrites(summary, "upsert", listSequence, detailSequence, "conversation.updated", now)
		if writeErr != nil {
			return ConversationSummary{}, writeErr
		}
		if err = s.transact(ctx, writes); err == nil {
			return summary, nil
		}
		if !isTransactionConflict(err) {
			return ConversationSummary{}, fmt.Errorf("activate conversation bot: %w", err)
		}
	}
	return ConversationSummary{}, errors.New("activate conversation bot: concurrent update limit reached")
}

// SetAgentProcessing durably changes the AI processing indicator and publishes
// a sequenced detail event. A stale worker cannot clear a newer worker's token.
func (s *ConversationMutationService) SetAgentProcessing(ctx context.Context, phone, messageID string, active bool) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("agent processing message id is required")
	}
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		summary, _, detailSequence, found, err := s.readState(ctx, phone)
		if err != nil {
			return err
		}
		if !found {
			return ErrConversationNotFound
		}
		now := s.now().UTC()
		if active {
			if summary.AgentProcessing && summary.AgentProcessingID == messageID && summary.AgentProcessingUntil != nil && summary.AgentProcessingUntil.After(now) {
				return nil
			}
			summary.AgentProcessing = true
			summary.AgentProcessingID = messageID
			expiresAt := now.Add(4 * time.Minute)
			summary.AgentProcessingUntil = &expiresAt
		} else {
			if !summary.AgentProcessing || (summary.AgentProcessingID != "" && summary.AgentProcessingID != messageID) {
				return nil
			}
			summary.AgentProcessing = false
			summary.AgentProcessingID = ""
			summary.AgentProcessingUntil = nil
		}
		writes, writeErr := s.agentProcessingWrites(summary, detailSequence, active, now)
		if writeErr != nil {
			return writeErr
		}
		if err = s.transact(ctx, writes); err == nil {
			return nil
		}
		if !isTransactionConflict(err) {
			return fmt.Errorf("set agent processing: %w", err)
		}
	}
	return errors.New("set agent processing: concurrent update limit reached")
}

func (s *ConversationMutationService) agentProcessingWrites(summary ConversationSummary, detailSequence uint64, active bool, now time.Time) ([]types.TransactWriteItem, error) {
	nextDetail := detailSequence + 1
	metaItem, err := attributevalue.MarshalMap(summary)
	if err != nil {
		return nil, fmt.Errorf("encode agent processing metadata: %w", err)
	}
	metaItem["pk"] = &types.AttributeValueMemberS{Value: "CONV#" + summary.Phone}
	metaItem["sk"] = &types.AttributeValueMemberS{Value: "META"}
	metaItem["expiresAt"] = &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}
	eventID, err := s.newID(now)
	if err != nil {
		return nil, fmt.Errorf("create agent processing event id: %w", err)
	}
	payload, err := attributevalue.MarshalMap(map[string]any{"phone": summary.Phone, "active": active, "expiresAt": summary.AgentProcessingUntil})
	if err != nil {
		return nil, fmt.Errorf("encode agent processing payload: %w", err)
	}
	shard := conversationShard(summary.Phone)
	eventItem := map[string]types.AttributeValue{
		"pk":            &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", shard)},
		"sk":            &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID},
		"eventId":       &types.AttributeValueMemberS{Value: eventID},
		"eventType":     &types.AttributeValueMemberS{Value: "agent_processing"},
		"phone":         &types.AttributeValueMemberS{Value: summary.Phone},
		"stream":        &types.AttributeValueMemberS{Value: "detail:" + summary.Phone},
		"sequence":      &types.AttributeValueMemberN{Value: fmt.Sprint(nextDetail)},
		"payload":       &types.AttributeValueMemberM{Value: payload},
		"state":         &types.AttributeValueMemberS{Value: "pending"},
		"nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		"expiresAt":     &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())},
	}
	return []types.TransactWriteItem{
		sequenceUpdate(s.tableName, "SEQ#DETAIL#"+summary.Phone, detailSequence, nextDetail),
		{Put: &types.Put{TableName: &s.tableName, Item: metaItem, ConditionExpression: aws.String("generation = :generation"), ExpressionAttributeValues: map[string]types.AttributeValue{":generation": &types.AttributeValueMemberN{Value: fmt.Sprint(summary.Generation)}}}},
		{Put: &types.Put{TableName: &s.tableName, Item: eventItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}, nil
}

func normalizeDerivationText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// SetUnreadCount updates the unread projection while preserving transactional versions and events.
func (s *ConversationMutationService) SetUnreadCount(ctx context.Context, phone string, unread int) (ConversationSummary, error) {
	if unread < 0 {
		return ConversationSummary{}, errors.New("unread count cannot be negative")
	}
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		summary, listSequence, detailSequence, found, err := s.readState(ctx, phone)
		if err != nil {
			return ConversationSummary{}, err
		}
		if !found {
			return ConversationSummary{}, ErrConversationNotFound
		}
		now := s.now().UTC()
		if summary.UnreadCount == unread {
			return summary, nil
		}
		summary.UnreadCount = unread
		summary.UpdatedAt = &now
		summary.Generation++
		writes, err := s.visibleWrites(summary, "upsert", listSequence, detailSequence, "conversation.updated", now)
		if err != nil {
			return ConversationSummary{}, err
		}
		if err = s.transact(ctx, writes); err == nil {
			return summary, nil
		}
		if !isTransactionConflict(err) {
			return ConversationSummary{}, fmt.Errorf("set conversation unread count: %w", err)
		}
	}
	return ConversationSummary{}, errors.New("set conversation unread count: concurrent update limit reached")
}

// DeleteConversation performs an idempotent logical delete and leaves a generation tombstone.
func (s *ConversationMutationService) DeleteConversation(ctx context.Context, phone string) (DeleteConversationResult, error) {
	for attempt := 0; attempt < mutationMaxAttempts; attempt++ {
		summary, listSequence, detailSequence, found, err := s.readState(ctx, phone)
		if err != nil {
			return DeleteConversationResult{}, err
		}
		if !found {
			return DeleteConversationResult{Deleted: true}, nil
		}
		now := s.now().UTC()
		summary.Generation++
		summary.UpdatedAt = &now
		writes, err := s.visibleWrites(summary, "delete", listSequence, detailSequence, "conversation.deleted", now)
		if err != nil {
			return DeleteConversationResult{}, err
		}
		writes = append(writes, types.TransactWriteItem{Delete: &types.Delete{TableName: &s.tableName, Key: snapshotKey("CONV#"+phone, "META"), ConditionExpression: aws.String("generation = :generation"), ExpressionAttributeValues: map[string]types.AttributeValue{":generation": &types.AttributeValueMemberN{Value: fmt.Sprint(summary.Generation - 1)}}}}, types.TransactWriteItem{Update: &types.Update{TableName: &s.tableName, Key: snapshotKey("CONV_COUNT", "META"), UpdateExpression: aws.String("ADD activeCount :decrement"), ConditionExpression: aws.String("activeCount > :zero"), ExpressionAttributeValues: map[string]types.AttributeValue{":decrement": &types.AttributeValueMemberN{Value: "-1"}, ":zero": &types.AttributeValueMemberN{Value: "0"}}}}, types.TransactWriteItem{Put: &types.Put{TableName: &s.tableName, Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "TOMBSTONE#" + phone}, "sk": &types.AttributeValueMemberS{Value: fmt.Sprintf("GEN#%020d", summary.Generation)}, "phone": &types.AttributeValueMemberS{Value: phone}, "generation": &types.AttributeValueMemberN{Value: fmt.Sprint(summary.Generation)}, "deletedAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}}, ConditionExpression: aws.String("attribute_not_exists(pk)")}})
		// visibleWrites includes a META Put for upserts; deletion must not recreate it.
		writes = removeConversationMetaPut(writes, phone)
		if err = s.transact(ctx, writes); err == nil {
			return DeleteConversationResult{Deleted: true}, nil
		}
		if !isTransactionConflict(err) {
			return DeleteConversationResult{}, fmt.Errorf("delete conversation: %w", err)
		}
	}
	return DeleteConversationResult{}, errors.New("delete conversation: concurrent update limit reached")
}

func (s *ConversationMutationService) readState(ctx context.Context, phone string) (ConversationSummary, uint64, uint64, bool, error) {
	conversation, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: snapshotKey("CONV#"+phone, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return ConversationSummary{}, 0, 0, false, fmt.Errorf("read conversation mutation target: %w", err)
	}
	var summary ConversationSummary
	found := len(conversation.Item) > 0
	if found {
		err = attributevalue.UnmarshalMap(conversation.Item, &summary)
	}
	if err != nil {
		return ConversationSummary{}, 0, 0, false, fmt.Errorf("decode conversation mutation target: %w", err)
	}
	listSeq, err := readSequenceItem(ctx, s.client, s.tableName, listSequencePK)
	if err != nil {
		return ConversationSummary{}, 0, 0, false, err
	}
	detailSeq, err := readSequenceItem(ctx, s.client, s.tableName, "SEQ#DETAIL#"+phone)
	if err != nil {
		return ConversationSummary{}, 0, 0, false, err
	}
	return summary, listSeq, detailSeq, found, nil
}
func readSequenceItem(ctx context.Context, client conversationMutationDynamo, table, pk string) (uint64, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &table, Key: snapshotKey(pk, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return 0, fmt.Errorf("read mutation sequence: %w", err)
	}
	if len(out.Item) == 0 {
		return 0, nil
	}
	var seq listSequence
	if err = attributevalue.UnmarshalMap(out.Item, &seq); err != nil {
		return 0, fmt.Errorf("decode mutation sequence: %w", err)
	}
	return seq.Sequence, nil
}

func (s *ConversationMutationService) visibleWrites(summary ConversationSummary, operation string, listSeq, detailSeq uint64, eventType string, now time.Time) ([]types.TransactWriteItem, error) {
	nextList, nextDetail := listSeq+1, detailSeq+1
	shard := conversationShard(summary.Phone)
	versionSK := fmt.Sprintf("PHONE#%s#SEQ#%020d", summary.Phone, nextList)
	version := ListVersion{Phone: summary.Phone, ValidFromSequence: nextList, Operation: operation, Summary: summary}
	versionItem, err := attributevalue.MarshalMap(version)
	if err != nil {
		return nil, fmt.Errorf("encode conversation version: %w", err)
	}
	versionItem["sk"] = &types.AttributeValueMemberS{Value: versionSK}
	versionItem["expiresAt"] = &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}
	listItem := cloneAttributeMap(versionItem)
	listItem["pk"] = &types.AttributeValueMemberS{Value: fmt.Sprintf("LIST_VERSION#%02X", shard)}
	searchItem := cloneAttributeMap(versionItem)
	searchItem["pk"] = &types.AttributeValueMemberS{Value: fmt.Sprintf("SEARCH_VERSION#%02X", shard)}
	searchItem["normalizedAlias"] = &types.AttributeValueMemberS{Value: normalizeSearch(summary.Alias)}
	searchItem["normalizedPhone"] = &types.AttributeValueMemberS{Value: normalizeSearch(summary.Phone)}
	metaItem, err := attributevalue.MarshalMap(summary)
	if err != nil {
		return nil, fmt.Errorf("encode conversation metadata: %w", err)
	}
	metaItem["pk"] = &types.AttributeValueMemberS{Value: "CONV#" + summary.Phone}
	metaItem["sk"] = &types.AttributeValueMemberS{Value: "META"}
	metaItem["expiresAt"] = &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(90 * 24 * time.Hour).Unix())}
	eventID, err := s.newID(now)
	if err != nil {
		return nil, fmt.Errorf("create outbox event id: %w", err)
	}
	eventItem := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", shard)}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID}, "eventId": &types.AttributeValueMemberS{Value: eventID}, "eventType": &types.AttributeValueMemberS{Value: eventType}, "phone": &types.AttributeValueMemberS{Value: summary.Phone}, "generation": &types.AttributeValueMemberN{Value: fmt.Sprint(summary.Generation)}, "stream": &types.AttributeValueMemberS{Value: "detail:" + summary.Phone}, "sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(nextDetail)}, "state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())}}
	listEventID := alternateEventID(eventID)
	listEventType := "conversation.updated"
	if operation == "delete" {
		listEventType = "conversation.deleted"
	}
	listEventItem := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", shard)}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + listEventID}, "eventId": &types.AttributeValueMemberS{Value: listEventID}, "eventType": &types.AttributeValueMemberS{Value: listEventType}, "phone": &types.AttributeValueMemberS{Value: summary.Phone}, "generation": &types.AttributeValueMemberN{Value: fmt.Sprint(summary.Generation)}, "stream": &types.AttributeValueMemberS{Value: "list"}, "sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(nextList)}, "state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())}}
	payload, err := attributevalue.MarshalMap(map[string]any{"phone": summary.Phone, "generation": summary.Generation, "conversation": withoutMetaClickID(summary)})
	if err != nil {
		return nil, fmt.Errorf("encode conversation event payload: %w", err)
	}
	eventItem["payload"] = &types.AttributeValueMemberM{Value: payload}
	listEventItem["payload"] = &types.AttributeValueMemberM{Value: payload}
	writes := []types.TransactWriteItem{sequenceUpdate(s.tableName, listSequencePK, listSeq, nextList), sequenceUpdate(s.tableName, "SEQ#DETAIL#"+summary.Phone, detailSeq, nextDetail), {Put: &types.Put{TableName: &s.tableName, Item: metaItem, ConditionExpression: aws.String("generation = :previousGeneration"), ExpressionAttributeValues: map[string]types.AttributeValue{":previousGeneration": &types.AttributeValueMemberN{Value: fmt.Sprint(summary.Generation - 1)}}}}, {Put: &types.Put{TableName: &s.tableName, Item: listItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: &s.tableName, Item: searchItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: &s.tableName, Item: eventItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}, {Put: &types.Put{TableName: &s.tableName, Item: listEventItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}
	return writes, nil
}

func alternateEventID(eventID string) string {
	if len(eventID) == 0 {
		return eventID
	}
	replacement := byte('0')
	if eventID[len(eventID)-1] == '0' {
		replacement = '1'
	}
	return eventID[:len(eventID)-1] + string(replacement)
}

func sequenceUpdate(table, pk string, current, next uint64) types.TransactWriteItem {
	condition := "attribute_not_exists(#sequence) OR #sequence = :current"
	values := map[string]types.AttributeValue{":next": &types.AttributeValueMemberN{Value: fmt.Sprint(next)}, ":current": &types.AttributeValueMemberN{Value: fmt.Sprint(current)}}
	return types.TransactWriteItem{Update: &types.Update{TableName: &table, Key: snapshotKey(pk, "META"), UpdateExpression: aws.String("SET #sequence = :next"), ConditionExpression: &condition, ExpressionAttributeNames: map[string]string{"#sequence": "sequence"}, ExpressionAttributeValues: values}}
}
func (s *ConversationMutationService) transact(ctx context.Context, writes []types.TransactWriteItem) error {
	_, err := s.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	return err
}
func conversationShard(phone string) byte {
	sum := sha256.Sum256([]byte(phone))
	return sum[len(sum)-1] & 0x0f
}
func cloneAttributeMap(source map[string]types.AttributeValue) map[string]types.AttributeValue {
	target := make(map[string]types.AttributeValue, len(source)+2)
	for key, value := range source {
		target[key] = value
	}
	return target
}
func removeConversationMetaPut(writes []types.TransactWriteItem, phone string) []types.TransactWriteItem {
	for index, item := range writes {
		if item.Put != nil {
			if pk, ok := item.Put.Item["pk"].(*types.AttributeValueMemberS); ok && pk.Value == "CONV#"+phone {
				return append(writes[:index], writes[index+1:]...)
			}
		}
	}
	return writes
}
func isTransactionConflict(err error) bool {
	var canceled *types.TransactionCanceledException
	return errors.As(err, &canceled)
}
