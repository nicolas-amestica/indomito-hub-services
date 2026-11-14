package domain

import (
	"context"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// derivationHistory consulta hasta diez mensajes del cliente/agente, incluso si la IA usa menos turnos.
func (s *AgentService) derivationHistory(ctx context.Context, phone string, latest Message) ([]DerivationTranscriptMessage, error) {
	result := make([]DerivationTranscriptMessage, 0, 10)
	var start map[string]types.AttributeValue
	condition := "pk = :pk AND begins_with(sk, :message)"
	values := map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONV#" + phone}, ":message": &types.AttributeValueMemberS{Value: "MSG#"}}
	if latest.MessageSK != "" {
		condition = "pk = :pk AND sk BETWEEN :message AND :latest"
		values[":latest"] = &types.AttributeValueMemberS{Value: latest.MessageSK}
	}
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{TableName: &s.table, KeyConditionExpression: &condition, ExpressionAttributeValues: values, ExclusiveStartKey: start, ScanIndexForward: aws.Bool(false), ConsistentRead: aws.Bool(true), Limit: aws.Int32(25)})
		if err != nil {
			return nil, err
		}
		var messages []Message
		if err = attributevalue.UnmarshalListOfMaps(out.Items, &messages); err != nil {
			return nil, err
		}
		for _, m := range messages {
			if m.Type != MessageTypeCustomer && m.Type != MessageTypeAIAgent {
				continue
			}
			// La transcripción del audio actual puede existir solo en memoria.
			if m.ID == latest.ID && latest.Content != nil {
				m.Content = latest.Content
			}
			result = append(result, derivationTranscript(m))
			if len(result) == 10 {
				break
			}
		}
		if len(result) == 10 || len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	slices.Reverse(result)
	return result, nil
}
