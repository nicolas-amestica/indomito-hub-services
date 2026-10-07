package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// PreparePlan prepara cuentas por transacciones pequeñas y solo publica al completar todas.
// El handler debe construir Startup desde el contrato aprobado y una puesta en marcha autorizada.
// Es reanudable: repetir la misma entrada no crea cuentas ni incrementa contadores dos veces.
func (s Service) PreparePlan(ctx context.Context, input collection.Startup, actor string) error {
	if actor == "" {
		return collection.ErrInvalid
	}
	accounts, err := collection.Start(input)
	if err != nil {
		return err
	}
	if s.DB == nil || s.Table == "" {
		return collection.ErrInvalid
	}
	seenLookup := map[string]bool{}
	for _, account := range accounts {
		if lookup, exists := input.LookupKeys[account.ID]; exists {
			if len(lookup) != 68 || !strings.HasPrefix(lookup, "RUT#") || seenLookup[lookup] {
				return collection.ErrInvalid
			}
			if _, err := hex.DecodeString(lookup[4:]); err != nil {
				return collection.ErrInvalid
			}
			seenLookup[lookup] = true
		}
	}
	if len(seenLookup) != len(input.LookupKeys) {
		return collection.ErrInvalid
	}
	// La huella utiliza cuentas ordenadas y condiciones, no el orden accidental de la nómina.
	canonical := input
	canonical.Participants = nil
	b, err := json.Marshal(struct {
		Conditions collection.Startup
		Accounts   []collection.Account
	}{canonical, accounts})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(b)
	hash := hex.EncodeToString(digest[:])
	plan := record{PK: "TRIP#" + input.TripID, SK: "META", Version: 1, Status: "PREPARING", Expected: int64(len(accounts)), Fingerprint: hash, RosterSchemaVersion: 1}
	plan.Event = &collection.Event{Audit: collection.Audit{CommandID: ulid.Make().String(), Actor: actor, Reason: "Puesta en marcha desde contrato aprobado", RecordedAt: time.Now().UTC()}, SchemaVersion: 1, Type: "PAYMENT_PLAN_PREPARING", TripID: input.TripID}
	if existing, readErr := s.read(ctx, plan.PK, plan.SK); readErr == nil {
		if existing.Fingerprint != hash {
			return ErrReplayMismatch
		}
		if existing.Status == "ACTIVE" {
			return nil
		}
	} else if !errors.Is(readErr, ErrNotFound) {
		return readErr
	} else {
		write, buildErr := s.put(plan, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return buildErr
		}
		snapshot, buildErr := s.put(record{PK: plan.PK, SK: "SETUP", Startup: &input, Fingerprint: hash}, "attribute_not_exists(pk)", nil)
		if buildErr != nil {
			return buildErr
		}
		if _, writeErr := s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{write, snapshot}}); writeErr != nil {
			existing, checkErr := s.read(ctx, plan.PK, plan.SK)
			if checkErr != nil {
				return writeErr
			}
			if existing.Fingerprint != hash {
				return ErrReplayMismatch
			}
		}
	}
	for _, account := range accounts {
		if err = ctx.Err(); err != nil {
			return err
		}
		var participant collection.Participant
		for _, candidate := range input.Participants {
			if candidate.ID == account.ParticipantID {
				participant = candidate
				break
			}
		}
		roster := RosterProjection{AccountID: account.ID, ParticipantID: account.ParticipantID, Name: participant.Name, Document: participant.Document, Active: account.Active, Free: account.Free, Version: account.Version}
		if err = s.prepareAccount(ctx, account, roster, hash, input.LookupKeys[account.ID]); err != nil {
			return err
		}
	}
	return s.publishPlan(ctx, plan.PK, hash)
}

func (s Service) prepareAccount(ctx context.Context, account collection.Account, roster RosterProjection, hash, lookup string) error {
	for retry := 0; retry < 4; retry++ {
		if existing, err := s.read(ctx, "ACCOUNT#"+account.ID, "META"); err == nil {
			if existing.Fingerprint != hash {
				return ErrReplayMismatch
			}
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		plan, err := s.read(ctx, "TRIP#"+account.TripID, "META")
		if err != nil {
			return err
		}
		if plan.Fingerprint != hash || plan.Status != "PREPARING" || plan.Prepared >= plan.Expected {
			return collection.ErrConflict
		}
		old := plan.Version
		plan.Version++
		plan.Prepared++
		meta, err := s.put(plan, "#version = :previous", versionValue(old))
		if err != nil {
			return err
		}
		row, err := s.put(record{PK: "ACCOUNT#" + account.ID, SK: "META", Version: account.Version, Account: &account, Fingerprint: hash}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		member, err := s.put(record{PK: plan.PK, SK: "MEMBER#" + account.ID, AccountID: account.ID, TripID: account.TripID, Version: account.Version, Roster: &roster, Fingerprint: hash}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		writes := []types.TransactWriteItem{meta, row, member}
		if lookup != "" {
			link, buildErr := s.put(record{PK: plan.PK, SK: lookup, AccountID: account.ID, TripID: account.TripID, Fingerprint: hash}, "attribute_not_exists(pk)", nil)
			if buildErr != nil {
				return buildErr
			}
			writes = append(writes, link)
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
		if err == nil {
			return nil
		}
		var cancelled *types.TransactionCanceledException
		if !errors.As(err, &cancelled) {
			// Un timeout puede suceder después del commit; comprobar la cuenta antes de informar error.
			if existing, checkErr := s.read(ctx, "ACCOUNT#"+account.ID, "META"); checkErr == nil && existing.Fingerprint == hash {
				return nil
			}
			return err
		}
	}
	return collection.ErrConflict
}

func (s Service) publishPlan(ctx context.Context, pk, hash string) error {
	plan, err := s.read(ctx, pk, "META")
	if err != nil {
		return err
	}
	if plan.Fingerprint != hash {
		return ErrReplayMismatch
	}
	if plan.Status == "ACTIVE" {
		return nil
	}
	if plan.Status != "PREPARING" || plan.Expected < 1 || plan.Prepared != plan.Expected {
		return collection.ErrConflict
	}
	old := plan.Version
	plan.Version++
	plan.Status = "ACTIVE"
	write, err := s.put(plan, "#version = :previous", versionValue(old))
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{write}})
	if err != nil {
		if existing, readErr := s.read(ctx, pk, "META"); readErr == nil && existing.Fingerprint == hash && existing.Status == "ACTIVE" {
			return nil
		}
	}
	return err
}

func versionValue(version int64) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{":previous": &types.AttributeValueMemberN{Value: strconv.FormatInt(version, 10)}}
}
