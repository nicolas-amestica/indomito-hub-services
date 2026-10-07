package collection

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// AnnexProposalSummary muestra impacto contractual, nunca ingresos bancarios ficticios.
// Stale exige preparar otro borrador; incluso false no reemplaza el control al aprobar.
type AnnexProposalSummary struct {
	RelatedAccountID string `json:"relatedAccountId,omitempty"`
	AccountID        string `json:"accountId"`
	ParticipantID    string `json:"participantId"`
	Kind             string `json:"kind"`
	ExpectedVersion  int64  `json:"expectedVersion"`
	CurrentVersion   int64  `json:"currentVersion"`
	Stale            bool   `json:"stale"`
	DebtAdded        int64  `json:"debtAdded"`
	DebtRemoved      int64  `json:"debtRemoved"`
	Free             bool   `json:"free"`
}

// AnnexProposalPage contiene hasta veinte propuestas. No es un total del grupo.
type AnnexProposalPage struct {
	Items      []AnnexProposalSummary `json:"items"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

// PreviewAnnexDraft consulta únicamente propuestas del anexo de la gira indicada.
// Lee la cabecera, hace una Query y hasta veinte GetItem de cuentas, sin Scan/GSI;
// los cursores nunca contienen
// claves arbitrarias. Solo la capa administrativa autorizada debe invocarlo.
func (s Service) PreviewAnnexDraft(ctx context.Context, tripID, id, cursor string) (AnnexProposalPage, error) {
	if !validPortalID(tripID) || !validPortalID(id) || (cursor != "" && !validPortalID(cursor)) || s.DB == nil || s.Table == "" {
		return AnnexProposalPage{}, domain.ErrInvalid
	}
	root, err := s.read(ctx, "TRIP#"+tripID, "ANNEX#"+id)
	if err != nil {
		return AnnexProposalPage{}, err
	}
	if root.Status != "DRAFT" || root.Prepared != root.Expected || root.Expected < 1 || root.Expected > 1000 {
		return AnnexProposalPage{}, domain.ErrConflict
	}
	db, ok := s.DB.(queryDatabase)
	if !ok {
		return AnnexProposalPage{}, domain.ErrInvalid
	}
	prefix := root.SK + "#PROPOSAL#"
	input := &dynamodb.QueryInput{TableName: &s.Table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: root.PK}, ":prefix": &types.AttributeValueMemberS{Value: prefix}}, ConsistentRead: aws.Bool(true), Limit: aws.Int32(20)}
	if cursor != "" {
		input.ExclusiveStartKey = key(root.PK, prefix+cursor)
	}
	result, err := db.Query(ctx, input)
	if err != nil {
		return AnnexProposalPage{}, err
	}
	page := AnnexProposalPage{Items: []AnnexProposalSummary{}}
	for _, item := range result.Items {
		var row record
		if attributevalue.UnmarshalMap(item, &row) != nil || row.PK != root.PK || row.SK != prefix+row.AccountID || row.Fingerprint != root.Fingerprint || !validPortalID(row.AccountID) || row.Change == nil {
			return AnnexProposalPage{}, domain.ErrInvalid
		}
		summary, err := s.previewAnnexProposal(ctx, tripID, id, row)
		if err != nil {
			return AnnexProposalPage{}, err
		}
		page.Items = append(page.Items, summary)
	}
	if len(result.LastEvaluatedKey) > 0 {
		pk, pkOK := result.LastEvaluatedKey["pk"].(*types.AttributeValueMemberS)
		sk, skOK := result.LastEvaluatedKey["sk"].(*types.AttributeValueMemberS)
		if !pkOK || !skOK || pk.Value != root.PK || !strings.HasPrefix(sk.Value, prefix) || !validPortalID(strings.TrimPrefix(sk.Value, prefix)) {
			return AnnexProposalPage{}, domain.ErrInvalid
		}
		page.NextCursor = strings.TrimPrefix(sk.Value, prefix)
	}
	return page, nil
}

func (s Service) previewAnnexProposal(ctx context.Context, tripID, id string, row record) (AnnexProposalSummary, error) {
	c := row.Change
	if c.Account.Validate() != nil || c.Account.ID != row.AccountID || c.Account.TripID != tripID || c.Event.AccountID != row.AccountID || c.Event.TripID != tripID || c.Event.Reference != id {
		return AnnexProposalSummary{}, domain.ErrInvalid
	}
	if row.RelatedAccountID != "" && (!validPortalID(row.RelatedAccountID) || row.RelatedAccountID == row.AccountID) {
		return AnnexProposalSummary{}, domain.ErrInvalid
	}
	summary := AnnexProposalSummary{AccountID: row.AccountID, ParticipantID: c.Account.ParticipantID, Kind: c.Event.Type, Free: c.Account.Free, RelatedAccountID: row.RelatedAccountID}
	switch c.Event.Type {
	case "PARTICIPANT_ADMITTED":
		if c.Account.Version != 1 || !c.Account.Active {
			return AnnexProposalSummary{}, domain.ErrInvalid
		}
		summary.DebtAdded = c.Account.Position().Receivable
	case "PARTICIPANT_WITHDRAWN":
		if c.Account.Version < 2 || c.Account.Active || c.Event.Amount < 0 {
			return AnnexProposalSummary{}, domain.ErrInvalid
		}
		summary.ExpectedVersion = c.Account.Version - 1
		summary.DebtRemoved = c.Event.Amount + c.Account.DepositAgreed - c.Account.DepositReceived
	default:
		return AnnexProposalSummary{}, domain.ErrInvalid
	}
	current, err := s.read(ctx, "ACCOUNT#"+row.AccountID, "META")
	if errors.Is(err, ErrNotFound) {
		summary.Stale = summary.ExpectedVersion != 0
		return summary, nil
	}
	if err != nil {
		return AnnexProposalSummary{}, err
	}
	if current.Account == nil || current.Account.Validate() != nil || current.Account.Version != current.Version {
		return AnnexProposalSummary{}, domain.ErrInvalid
	}
	summary.CurrentVersion = current.Version
	summary.Stale = summary.ExpectedVersion == 0 || current.Version != summary.ExpectedVersion || current.Account.TripID != tripID || current.Account.ParticipantID != summary.ParticipantID
	return summary, nil
}
