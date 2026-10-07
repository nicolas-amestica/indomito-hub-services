package collection

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// CloseRoster impide nuevos borradores/anexos sin cerrar la cobranza. Pagos,
// comprobantes y devoluciones continúan usando el plan ACTIVE.
func (s Service) CloseRoster(ctx context.Context, tripID string, audit domain.Audit) error {
	if !validPortalID(tripID) || !validPortalID(audit.CommandID) || audit.Actor == "" || len(audit.Reason) < 5 || audit.RecordedAt.IsZero() || s.DB == nil || s.Table == "" {
		return domain.ErrInvalid
	}
	for retry := 0; retry < 6; retry++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		plan, err := s.read(ctx, "TRIP#"+tripID, "META")
		if err != nil {
			return err
		}
		if plan.RosterClosed {
			if plan.RosterClosure != nil && *plan.RosterClosure == audit {
				return nil
			}
			return ErrReplayMismatch
		}
		if plan.Status != "ACTIVE" || plan.PendingAnnexID != "" || plan.RosterSchemaVersion != 1 {
			return domain.ErrConflict
		}
		previous := plan.Version
		plan.Version++
		plan.RosterClosed = true
		plan.RosterClosure = &audit
		planWrite, err := s.put(plan, "#version = :previous", versionValue(previous))
		if err != nil {
			return err
		}
		event := domain.Event{Audit: audit, SchemaVersion: 1, Type: "ROSTER_CLOSED", TripID: tripID}
		eventWrite, err := s.put(record{PK: plan.PK, SK: "ROSTER_CLOSURE#" + audit.CommandID, TripID: tripID, Event: &event}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{planWrite, eventWrite}})
		if err == nil {
			return nil
		}
		latest, readErr := s.read(ctx, plan.PK, plan.SK)
		if readErr == nil && latest.RosterClosed && latest.RosterClosure != nil {
			if *latest.RosterClosure == audit {
				return nil
			}
			return ErrReplayMismatch
		}
		var cancelled *types.TransactionCanceledException
		if !errors.As(err, &cancelled) {
			return err
		}
	}
	return domain.ErrConflict
}
