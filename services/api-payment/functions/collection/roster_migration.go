package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

const rosterMigrationSK = "ROSTER_MIGRATION#0001"

type RosterMigrationState struct {
	TripID   string `json:"tripId"`
	Status   string `json:"status"`
	Prepared int64  `json:"prepared"`
	Expected int64  `json:"expected"`
}

// BackfillRoster crea únicamente la proyección administrativa faltante. No
// modifica cuentas, eventos financieros, recibos, lookup ni la huella del plan.
func (s Service) BackfillRoster(ctx context.Context, tripID string, profiles []RosterProjection, audit domain.Audit) (RosterMigrationState, error) {
	profiles, fingerprint, err := canonicalRosterMigration(tripID, profiles, audit)
	if err != nil || s.DB == nil || s.Table == "" {
		return RosterMigrationState{}, domain.ErrInvalid
	}
	plan, err := s.read(ctx, "TRIP#"+tripID, "META")
	if err != nil {
		return RosterMigrationState{}, err
	}
	if plan.Status != "ACTIVE" {
		return RosterMigrationState{}, domain.ErrConflict
	}
	snapshot, err := s.read(ctx, plan.PK, "SETUP")
	if err != nil || snapshot.Startup == nil || len(snapshot.Startup.Participants) != len(profiles) {
		return RosterMigrationState{}, domain.ErrConflict
	}
	expected := map[string]bool{}
	for _, participant := range snapshot.Startup.Participants {
		expected[participant.ID] = true
	}
	for _, profile := range profiles {
		if !expected[profile.AccountID] || profile.ParticipantID != profile.AccountID {
			return RosterMigrationState{}, domain.ErrConflict
		}
		delete(expected, profile.AccountID)
	}
	if len(expected) != 0 {
		return RosterMigrationState{}, domain.ErrConflict
	}
	if plan.RosterSchemaVersion == 1 {
		if existing, readErr := s.read(ctx, plan.PK, rosterMigrationSK); readErr == nil && existing.Fingerprint != fingerprint {
			return RosterMigrationState{}, ErrReplayMismatch
		} else if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return RosterMigrationState{}, readErr
		}
		return RosterMigrationState{TripID: tripID, Status: "APPLIED", Prepared: int64(len(profiles)), Expected: int64(len(profiles))}, nil
	}
	root := record{PK: plan.PK, SK: rosterMigrationSK, TripID: tripID, Version: 1, Status: "PREPARING", Expected: int64(len(profiles)), Fingerprint: fingerprint, Event: &domain.Event{Audit: audit, SchemaVersion: 1, Type: "ROSTER_PROJECTION_MIGRATION", TripID: tripID}}
	if err = s.beginRosterMigration(ctx, root); err != nil {
		return RosterMigrationState{}, err
	}
	for _, profile := range profiles {
		if err = s.migrateRosterMember(ctx, root, profile); err != nil {
			return RosterMigrationState{}, err
		}
	}
	if err = s.publishRosterMigration(ctx, root); err != nil {
		return RosterMigrationState{}, err
	}
	return s.GetRosterMigration(ctx, tripID)
}

func canonicalRosterMigration(tripID string, profiles []RosterProjection, audit domain.Audit) ([]RosterProjection, string, error) {
	if !validPortalID(tripID) || audit.CommandID == "" || audit.Actor == "" || len(strings.TrimSpace(audit.Reason)) < 5 || audit.RecordedAt.IsZero() || len(profiles) < 1 || len(profiles) > 1000 {
		return nil, "", domain.ErrInvalid
	}
	profiles = append([]RosterProjection(nil), profiles...)
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].AccountID < profiles[j].AccountID })
	for index, profile := range profiles {
		if !validPortalID(profile.AccountID) || !validPortalID(profile.ParticipantID) || strings.TrimSpace(profile.Name) == "" || len(profile.Name) > 200 || strings.TrimSpace(profile.Document) == "" || len(profile.Document) > 64 || (index > 0 && profile.AccountID == profiles[index-1].AccountID) {
			return nil, "", domain.ErrInvalid
		}
		profiles[index].Name = strings.TrimSpace(profile.Name)
		profiles[index].Document = strings.TrimSpace(profile.Document)
		profiles[index].Active = false
		profiles[index].Free = false
		profiles[index].Version = 0
	}
	payload, err := json.Marshal(struct {
		TripID   string
		Profiles []RosterProjection
		Audit    domain.Audit
	}{tripID, profiles, audit})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return profiles, hex.EncodeToString(digest[:]), nil
}

func (s Service) beginRosterMigration(ctx context.Context, root record) error {
	if existing, err := s.read(ctx, root.PK, root.SK); err == nil {
		if existing.Fingerprint != root.Fingerprint {
			return ErrReplayMismatch
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	write, err := s.put(root, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.activePlanCheck(root.TripID), write}})
	if err != nil {
		if existing, readErr := s.read(ctx, root.PK, root.SK); readErr == nil && existing.Fingerprint == root.Fingerprint {
			return nil
		}
	}
	return err
}

func (s Service) migrateRosterMember(ctx context.Context, identity record, profile RosterProjection) error {
	memberSK := "MEMBER#" + profile.AccountID
	if existing, err := s.read(ctx, identity.PK, memberSK); err == nil {
		if existing.Roster == nil || existing.Roster.Name != profile.Name || existing.Roster.Document != profile.Document {
			return ErrReplayMismatch
		}
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	for retry := 0; retry < 6; retry++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		accountRow, err := s.read(ctx, "ACCOUNT#"+profile.AccountID, "META")
		if err != nil || accountRow.Account == nil || accountRow.Account.TripID != identity.TripID || accountRow.Account.ParticipantID != profile.ParticipantID || accountRow.Version != accountRow.Account.Version {
			return domain.ErrConflict
		}
		root, err := s.read(ctx, identity.PK, identity.SK)
		if err != nil {
			return err
		}
		if root.Fingerprint != identity.Fingerprint || root.Status != "PREPARING" || root.Prepared >= root.Expected {
			return domain.ErrConflict
		}
		previous := root.Version
		root.Version++
		root.Prepared++
		rootWrite, err := s.put(root, "#version = :previous", versionValue(previous))
		if err != nil {
			return err
		}
		roster := profile
		roster.Active, roster.Free, roster.Version = accountRow.Account.Active, accountRow.Account.Free, accountRow.Account.Version
		memberWrite, err := s.put(record{PK: identity.PK, SK: memberSK, TripID: identity.TripID, AccountID: profile.AccountID, Version: roster.Version, Roster: &roster, Fingerprint: identity.Fingerprint}, "attribute_not_exists(pk)", nil)
		if err != nil {
			return err
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{s.activePlanCheck(identity.TripID), s.accountVersionCheck(profile.AccountID, roster.Version), rootWrite, memberWrite}})
		if err == nil {
			return nil
		}
		if existing, readErr := s.read(ctx, identity.PK, memberSK); readErr == nil && existing.Roster != nil && existing.Roster.Name == profile.Name && existing.Roster.Document == profile.Document {
			return nil
		}
	}
	return domain.ErrConflict
}

func (s Service) publishRosterMigration(ctx context.Context, identity record) error {
	for retry := 0; retry < 6; retry++ {
		root, err := s.read(ctx, identity.PK, identity.SK)
		if err != nil {
			return err
		}
		plan, err := s.read(ctx, identity.PK, "META")
		if err != nil {
			return err
		}
		if root.Status == "APPLIED" && plan.RosterSchemaVersion == 1 {
			return nil
		}
		if root.Status != "PREPARING" || root.Prepared != root.Expected || plan.Status != "ACTIVE" || plan.RosterSchemaVersion != 0 {
			return domain.ErrConflict
		}
		rootPrevious, planPrevious := root.Version, plan.Version
		root.Version++
		root.Status = "APPLIED"
		plan.Version++
		plan.RosterSchemaVersion = 1
		rootWrite, err := s.put(root, "#version = :previous", versionValue(rootPrevious))
		if err != nil {
			return err
		}
		planWrite, err := s.put(plan, "#version = :previous", versionValue(planPrevious))
		if err != nil {
			return err
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{rootWrite, planWrite}})
		if err == nil {
			return nil
		}
	}
	return domain.ErrConflict
}

func (s Service) GetRosterMigration(ctx context.Context, tripID string) (RosterMigrationState, error) {
	root, err := s.read(ctx, "TRIP#"+tripID, rosterMigrationSK)
	if err != nil {
		return RosterMigrationState{}, err
	}
	return RosterMigrationState{TripID: tripID, Status: root.Status, Prepared: root.Prepared, Expected: root.Expected}, nil
}
