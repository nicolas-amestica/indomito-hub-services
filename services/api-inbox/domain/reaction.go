package domain

import (
	"context"
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

var ErrReactionTargetInvalid = errors.New("reaction target is invalid")

type ReactionProvider interface {
	SendReaction(context.Context, string, string, string) error
}
type ReactionService struct {
	client    inboundDynamo
	tableName string
	provider  ReactionProvider
	now       func() time.Time
}

func NewReactionService(client inboundDynamo, tableName string, provider ReactionProvider, now func() time.Time) (*ReactionService, error) {
	if client == nil || tableName == "" || provider == nil {
		return nil, errors.New("reaction dependencies are required")
	}
	if now == nil {
		now = time.Now
	}
	return &ReactionService{client: client, tableName: tableName, provider: provider, now: now}, nil
}

func (s *ReactionService) React(ctx context.Context, phone, messageID, emoji string) error {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.tableName, Key: snapshotKey("CONV#"+phone, "MSG#"+messageID), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return err
	}
	if len(out.Item) == 0 {
		return ErrReactionTargetInvalid
	}
	var message Message
	if err = attributevalue.UnmarshalMap(out.Item, &message); err != nil || message.WhatsAppMessageID == nil {
		return ErrReactionTargetInvalid
	}
	if err = s.provider.SendReaction(ctx, phone, *message.WhatsAppMessageID, emoji); err != nil {
		return err
	}
	return persistReaction(ctx, s.client, s.tableName, phone, message.MessageSK, *message.WhatsAppMessageID, MessageTypeTeam, emoji, s.now().UTC())
}

type InboundMetaReaction struct{ Phone, MessageID, Emoji, EventID, Timestamp string }

func (s *WebhookService) ApplyReaction(ctx context.Context, reaction InboundMetaReaction) error {
	if reaction.MessageID == "" || reaction.Phone == "" {
		return nil
	}
	index, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("WAMID#"+reaction.MessageID, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return err
	}
	phone, msgSK := attributeString(index.Item, "phone"), attributeString(index.Item, "msgSk")
	if phone == "" || msgSK == "" || phone != reaction.Phone {
		return nil
	}
	return persistReaction(ctx, s.db, s.table, phone, msgSK, reaction.MessageID, MessageTypeCustomer, reaction.Emoji, inboundTime(reaction.Timestamp, s.now().UTC()))
}

func persistReaction(ctx context.Context, client inboundDynamo, table, phone, msgSK, wamid string, actor MessageType, emoji string, now time.Time) error {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: &table, Key: snapshotKey("CONV#"+phone, msgSK), ConsistentRead: aws.Bool(true)})
	if err != nil || len(out.Item) == 0 {
		return err
	}
	var message Message
	if err = attributevalue.UnmarshalMap(out.Item, &message); err != nil {
		return err
	}
	if last := message.ReactionUpdatedAt[string(actor)]; !last.IsZero() && !last.Before(now) {
		return nil
	}
	reactions := make([]MessageReaction, 0, len(message.Reactions)+1)
	for _, current := range message.Reactions {
		if current.Actor != actor {
			reactions = append(reactions, current)
		}
	}
	if strings.TrimSpace(emoji) != "" {
		reactions = append(reactions, MessageReaction{Emoji: emoji, Actor: actor, UpdatedAt: now})
	}
	encoded, _ := attributevalue.MarshalList(reactions)
	updatedAt := message.ReactionUpdatedAt
	if updatedAt == nil {
		updatedAt = map[string]time.Time{}
	}
	updatedAt[string(actor)] = now
	encodedUpdatedAt, _ := attributevalue.Marshal(updatedAt)
	sequence, err := readSequenceItem(ctx, client, table, "SEQ#DETAIL#"+phone)
	if err != nil {
		return err
	}
	next := sequence + 1
	eventID := ulid.Make().String()
	payload, _ := attributevalue.MarshalMap(map[string]any{"phone": phone, "msgSk": msgSK, "reactions": reactions})
	outbox := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("WORK_DUE#OUTBOX#%02X", conversationShard(phone))}, "sk": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano) + "#" + eventID}, "eventId": &types.AttributeValueMemberS{Value: eventID}, "eventType": &types.AttributeValueMemberS{Value: "message.reactions.updated"}, "phone": &types.AttributeValueMemberS{Value: phone}, "stream": &types.AttributeValueMemberS{Value: "detail:" + phone}, "sequence": &types.AttributeValueMemberN{Value: fmt.Sprint(next)}, "payload": &types.AttributeValueMemberM{Value: payload}, "state": &types.AttributeValueMemberS{Value: "pending"}, "nextAttemptAt": &types.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)}, "expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Add(14 * 24 * time.Hour).Unix())}}
	_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{sequenceUpdate(table, "SEQ#DETAIL#"+phone, sequence, next), {Update: &types.Update{TableName: &table, Key: snapshotKey("CONV#"+phone, msgSK), UpdateExpression: aws.String("SET reactions=:reactions, reactionUpdatedAt=:reactionUpdatedAt"), ConditionExpression: aws.String("whatsappMessageId=:wamid"), ExpressionAttributeValues: map[string]types.AttributeValue{":reactions": &types.AttributeValueMemberL{Value: encoded}, ":reactionUpdatedAt": encodedUpdatedAt, ":wamid": &types.AttributeValueMemberS{Value: wamid}}}}, {Put: &types.Put{TableName: &table, Item: outbox, ConditionExpression: aws.String("attribute_not_exists(pk)")}}}})
	return err
}
