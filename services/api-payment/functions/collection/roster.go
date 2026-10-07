package collection

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

type RosterPage struct {
	Items       []RosterProjection `json:"items"`
	NextCursor  string             `json:"nextCursor,omitempty"`
	Closed      bool               `json:"closed"`
	ClosedAt    string             `json:"closedAt,omitempty"`
	CloseReason string             `json:"closeReason,omitempty"`
}

// ListRoster consulta la proyección administrativa de una sola gira; no usa Scan ni GSI.
func (s Service) ListRoster(ctx context.Context, tripID, cursor string) (RosterPage, error) {
	if !validPortalID(tripID) || (cursor != "" && !validPortalID(cursor)) || s.DB == nil || s.Table == "" {
		return RosterPage{}, domain.ErrInvalid
	}
	plan, err := s.read(ctx, "TRIP#"+tripID, "META")
	if err != nil {
		return RosterPage{}, err
	}
	if plan.Status != "ACTIVE" && plan.Status != "UPDATING_ROSTER" {
		return RosterPage{}, domain.ErrConflict
	}
	if plan.RosterSchemaVersion != 1 {
		return RosterPage{}, domain.ErrConflict
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return RosterPage{}, domain.ErrInvalid
	}
	pk, prefix := "TRIP#"+tripID, "MEMBER#"
	input := &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(50)}
	if cursor != "" {
		input.ExclusiveStartKey = key(pk, prefix+cursor)
	}
	out, err := db.Query(ctx, input)
	if err != nil {
		return RosterPage{}, err
	}
	page := RosterPage{Items: []RosterProjection{}, Closed: plan.RosterClosed}
	if plan.RosterClosure != nil {
		page.ClosedAt = plan.RosterClosure.RecordedAt.UTC().Format(time.RFC3339)
		page.CloseReason = plan.RosterClosure.Reason
	}
	for _, item := range out.Items {
		var row record
		if attributevalue.UnmarshalMap(item, &row) != nil || row.Roster == nil || row.PK != pk || row.SK != prefix+row.AccountID || row.Roster.AccountID != row.AccountID || row.Roster.Version != row.Version {
			return RosterPage{}, domain.ErrInvalid
		}
		page.Items = append(page.Items, *row.Roster)
	}
	if len(out.LastEvaluatedKey) > 0 {
		pkValue, pkOK := out.LastEvaluatedKey["pk"].(*types.AttributeValueMemberS)
		skValue, skOK := out.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS)
		if !pkOK || !skOK || pkValue.Value != pk || !strings.HasPrefix(skValue.Value, prefix) || !validPortalID(strings.TrimPrefix(skValue.Value, prefix)) {
			return RosterPage{}, domain.ErrInvalid
		}
		page.NextCursor = strings.TrimPrefix(skValue.Value, prefix)
	}
	return page, nil
}
