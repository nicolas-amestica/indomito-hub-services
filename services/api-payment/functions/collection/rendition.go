package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

// RenditionObjects conserva evidencia autenticada fuera del límite de un item DynamoDB.
type RenditionObjects interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
}

// RenditionPolicy requiere validación explícita del contrato antes de modificar caja.
// ReportEnd significa fecha de rendición del proveedor, nunca fecha verificada en cartola.
type RenditionPolicy struct {
	DevelopmentSimulation bool     `json:"developmentSimulation,omitempty"`
	Mode                  string   `json:"mode"`
	Revision              string   `json:"revision"`
	ValidatedBy           string   `json:"validatedBy"`
	Evidence              string   `json:"evidence"`
	EffectiveDateSource   string   `json:"effectiveDateSource"`
	DeductedFeeSchemes    []string `json:"deductedFeeSchemes"`
	DeferredFeeSchemes    []string `json:"deferredFeeSchemes"`
}

func (p RenditionPolicy) validate() error {
	if p.Mode == "OBSERVE" {
		return nil
	}
	if p.Mode != "APPLY" || p.Revision == "" || p.ValidatedBy == "" || p.Evidence == "" || p.EffectiveDateSource != "REPORT_END" || len(p.DeductedFeeSchemes)+len(p.DeferredFeeSchemes) == 0 {
		return providers.ErrRenditionPolicy
	}
	seen := make(map[string]bool)
	for _, schemes := range [][]string{p.DeductedFeeSchemes, p.DeferredFeeSchemes} {
		for _, scheme := range schemes {
			if strings.TrimSpace(scheme) == "" || seen[scheme] {
				return providers.ErrRenditionPolicy
			}
			seen[scheme] = true
		}
	}
	return nil
}

// RenditionState permite reanudar lotes grandes sin repetir sus asientos.
type RenditionState struct {
	Trust        string          `json:"trust,omitempty"`
	ID           string          `json:"id"`
	ProviderID   int64           `json:"providerId"`
	ReceivedAt   time.Time       `json:"receivedAt"`
	PendingSince time.Time       `json:"pendingSince"`
	ObjectKey    string          `json:"-"`
	Digest       string          `json:"-"`
	RawKey       string          `json:"-"`
	Status       string          `json:"status"`
	Cursor       int             `json:"processed"`
	Total        int             `json:"total"`
	Applied      int             `json:"applied"`
	Observed     int             `json:"observed"`
	Review       int             `json:"review"`
	Retries      int             `json:"retries"`
	LastError    string          `json:"lastError,omitempty"`
	Policy       RenditionPolicy `json:"policy"`
}

// RenditionResult expone diferencias sin publicar datos personales del reporte.
type RenditionResult struct {
	PaymentID     string `json:"paymentId"`
	TripID        string `json:"tripId,omitempty"`
	Status        string `json:"status"`
	Gross         int64  `json:"gross"`
	Fee           int64  `json:"fee"`
	FeeScheme     string `json:"feeScheme"`
	FeeTreatment  string `json:"feeTreatment"`
	EffectiveDate string `json:"renditionDate"`
}

func renditionDigest(b []byte) string               { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func renditionPK(id string) string                  { return "RENDITION#" + id }
func renditionJobSK(at time.Time, id string) string { return at.UTC().Format(time.RFC3339) + "#" + id }

// ReceiveRendition persiste evidencia e inbox/job antes de reconocer el aviso.
// El hash excluye iat y orden de items: reenvíos equivalentes conservan la misma identidad.
func (s Service) ReceiveRendition(ctx context.Context, objects RenditionObjects, stage string, report providers.Rendition, raw []byte, policy RenditionPolicy, now time.Time) (string, error) {
	if policy.DevelopmentSimulation && stage != "dev" {
		return "", providers.ErrRenditionPolicy
	}
	if (stage != "dev" && stage != "prd") || objects == nil || now.IsZero() || len(raw) == 0 || report.Validate() != nil || policy.validate() != nil {
		return "", domain.ErrInvalid
	}
	report.Items = append([]providers.RenditionItem(nil), report.Items...)
	for i := range report.Items {
		amount, e := providers.CLPInteger(report.Items[i].Amount)
		if e != nil {
			return "", e
		}
		fee, e := providers.CLPInteger(report.Items[i].Fee)
		if e != nil {
			return "", e
		}
		report.Items[i].Amount = json.Number(strconv.FormatInt(amount, 10))
		report.Items[i].Fee = json.Number(strconv.FormatInt(fee, 10))
		if report.Items[i].IntegratorFee != "" {
			n, e := providers.CLPInteger(report.Items[i].IntegratorFee)
			if e != nil {
				return "", e
			}
			report.Items[i].IntegratorFee = json.Number(strconv.FormatInt(n, 10))
		}
	}
	sort.Slice(report.Items, func(i, j int) bool { return report.Items[i].PaymentID < report.Items[j].PaymentID })
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	digest := renditionDigest(encoded)
	id := paymentOperationID(fmt.Sprintf("rendition:%s:%s:%s:%s:%d", stage, report.RecipientUID, report.Bank, report.Account, report.ID))
	if old, e := s.read(ctx, renditionPK(id), "META"); e == nil {
		if old.Fingerprint != digest {
			return "", ErrReplayMismatch
		}
		return id, nil
	} else if !errors.Is(e, ErrNotFound) {
		return "", e
	}
	prefix := "renditions/" + stage + "/" + id + "/" + digest
	if err = objects.Put(ctx, prefix+".json", encoded); err != nil {
		return "", err
	}
	rawKey := prefix + "-" + renditionDigest(raw) + ".jws.gz"
	if err = objects.Put(ctx, rawKey, raw); err != nil {
		return "", err
	}
	state := RenditionState{ID: id, ProviderID: report.ID, ReceivedAt: now, PendingSince: now, ObjectKey: prefix + ".json", Digest: digest, RawKey: rawKey, Status: "PENDING", Total: len(report.Items), Policy: policy}
	if policy.DevelopmentSimulation {
		state.Trust = "DEV_UNAUTHENTICATED_SIMULATION"
	}
	row := record{PK: renditionPK(id), SK: "META", Version: 1, Fingerprint: digest, Rendition: &state}
	job := record{PK: "RENDITION_JOBS", SK: renditionJobSK(now, id), AccountID: id, Status: "PENDING"}
	index := record{PK: "RENDITION_MONTH#" + now.UTC().Format("2006-01"), SK: id, Rendition: &state}
	var writes []types.TransactWriteItem
	for _, r := range []record{row, job, index} {
		w, e := s.put(r, "attribute_not_exists(pk)", nil)
		if e != nil {
			return "", e
		}
		writes = append(writes, w)
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	if err != nil {
		old, e := s.read(ctx, row.PK, row.SK)
		if e == nil && old.Fingerprint == digest {
			return id, nil
		}
		return "", err
	}
	return id, nil
}

// ProcessRendition procesa como máximo limit items. Cada avance confirma resultado
// y checkpoint juntos; un asiento aplicado antes de un corte se recupera por commandId.
func (s Service) ProcessRendition(ctx context.Context, objects RenditionObjects, id string, now time.Time, limit int) error {
	if !validPortalID(id) || limit < 1 || limit > 100 {
		return domain.ErrInvalid
	}
	row, err := s.read(ctx, renditionPK(id), "META")
	if err != nil {
		return err
	}
	if row.Rendition == nil {
		return domain.ErrInvalid
	}
	if row.Rendition.Status != "PENDING" {
		return nil
	}
	b, err := objects.Get(ctx, row.Rendition.ObjectKey)
	if err != nil {
		return err
	}
	if renditionDigest(b) != row.Rendition.Digest {
		return ErrReplayMismatch
	}
	var report providers.Rendition
	if json.Unmarshal(b, &report) != nil || report.Validate() != nil || len(report.Items) != row.Rendition.Total {
		return domain.ErrInvalid
	}
	zone, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return err
	}
	end, err := providers.RenditionDate(report.EndDate)
	if err != nil {
		return err
	}
	date := end.In(zone).Format(time.DateOnly)
	for n := 0; n < limit && row.Rendition.Cursor < len(report.Items); n++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		item := report.Items[row.Rendition.Cursor]
		result, e := s.matchRendition(ctx, id, item, row.Rendition.Policy, date, now)
		if e != nil {
			if errors.Is(e, ErrNotFound) && row.Rendition.Retries >= 11 {
				result.Status = "MISSING_PAYMENT_REVIEW"
			} else {
				return e
			}
		}
		previous := row.Version
		row.Version++
		row.Rendition.Cursor++
		switch result.Status {
		case "APPLIED":
			row.Rendition.Applied++
		case "OBSERVED":
			row.Rendition.Observed++
		default:
			row.Rendition.Review++
		}
		if row.Rendition.Cursor == len(report.Items) {
			row.Rendition.Status = "COMPLETED"
			if row.Rendition.Policy.Mode == "OBSERVE" {
				row.Rendition.Status = "OBSERVED"
			}
			if row.Rendition.Review > 0 {
				row.Rendition.Status = "REVIEW_REQUIRED"
			}
		}
		w, e := s.put(row, "#version = :previous", versionValue(previous))
		if e != nil {
			return e
		}
		resultRow := record{PK: row.PK, SK: "ITEM#" + item.PaymentID, RenditionResult: &result}
		rw, e := s.put(resultRow, "attribute_exists(pk) OR attribute_not_exists(pk)", nil)
		if e != nil {
			return e
		}
		index := record{PK: "RENDITION_MONTH#" + row.Rendition.ReceivedAt.UTC().Format("2006-01"), SK: id, Rendition: row.Rendition}
		iw, e := s.put(index, "attribute_exists(pk)", nil)
		if e != nil {
			return e
		}
		_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{w, rw, iw}})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Service) matchRendition(ctx context.Context, id string, item providers.RenditionItem, policy RenditionPolicy, date string, now time.Time) (RenditionResult, error) {
	gross, err := providers.CLPInteger(item.Amount)
	if err != nil {
		return RenditionResult{}, err
	}
	fee, err := providers.CLPInteger(item.Fee)
	if err != nil {
		return RenditionResult{}, err
	}
	result := RenditionResult{PaymentID: item.PaymentID, Gross: gross, Fee: fee, EffectiveDate: date, FeeScheme: item.FeeScheme, FeeTreatment: "UNVALIDATED"}
	if policy.DevelopmentSimulation {
		delayed := int64(0)
		if item.DelayedFee != "" {
			delayed, err = providers.CLPInteger(item.DelayedFee)
			if err != nil {
				return result, err
			}
		}
		if fee != 0 || delayed != 0 {
			result.Status = "DEV_NONZERO_FEE_REVIEW"
			return result, nil
		}
	}
	for _, scheme := range policy.DeductedFeeSchemes {
		if scheme == item.FeeScheme {
			result.FeeTreatment = "DEDUCTED"
		}
	}
	for _, scheme := range policy.DeferredFeeSchemes {
		if scheme == item.FeeScheme && fee == 0 {
			result.FeeTreatment = "DEFERRED"
		}
	}
	if !validPortalID(item.AttemptID) {
		result.Status = "UNKNOWN_ATTEMPT"
		return result, nil
	}
	attempt, err := s.read(ctx, "ATTEMPT#"+item.AttemptID, "META")
	if err != nil {
		return result, err
	}
	if attempt.Attempt == nil {
		return result, domain.ErrInvalid
	}
	account, err := s.read(ctx, "ACCOUNT#"+attempt.Attempt.AccountID, "META")
	if err != nil {
		return result, err
	}
	if account.Account == nil {
		return result, domain.ErrInvalid
	}
	result.TripID = account.Account.TripID
	confirmed, err := s.read(ctx, "ATTEMPT#"+item.AttemptID, "OUTCOME")
	if err != nil {
		return result, err
	}
	ref := "khipu:" + item.PaymentID
	if confirmed.Event == nil || confirmed.Event.Type != "PAYMENT_RECEIVED" || confirmed.Event.Reference != ref || confirmed.Event.Amount != gross || confirmed.Event.AccountID != account.Account.ID || confirmed.Event.TripID != account.Account.TripID || attempt.Attempt.Amount != gross {
		result.Status = "PAYMENT_MISMATCH"
		return result, nil
	}
	settled, err := s.read(ctx, "TRIP#"+result.TripID, "SETTLEMENT#"+ref)
	if err != nil {
		return result, err
	}
	if settled.Settlement == nil {
		return result, domain.ErrInvalid
	}
	commandID := paymentOperationID("rendition-item:" + id + ":" + item.PaymentID)
	if command, e := s.read(ctx, "COMMAND#"+commandID, "META"); e == nil {
		if command.Event == nil || command.Event.Amount != gross {
			return result, ErrReplayMismatch
		}
		result.Status = "APPLIED"
		return result, nil
	} else if !errors.Is(e, ErrNotFound) {
		return result, e
	}
	if settled.Settlement.SettledGross != 0 {
		result.Status = "ALREADY_SETTLED_REVIEW"
		return result, nil
	}
	if settled.Settlement.Amount != gross {
		result.Status = "AMOUNT_MISMATCH"
		return result, nil
	}
	if _, e := s.read(ctx, "ATTEMPT#"+item.AttemptID, "RESOLUTION"); e == nil {
		result.Status = "PAYMENT_RESOLVED_REVIEW"
		return result, nil
	} else if !errors.Is(e, ErrNotFound) {
		return result, e
	}
	if item.IntegratorFee != "" {
		n, e := providers.CLPInteger(item.IntegratorFee)
		if e != nil {
			return result, e
		}
		if n != 0 {
			result.Status = "INTEGRATOR_FEE_REVIEW"
			return result, nil
		}
	}
	if policy.Mode == "OBSERVE" {
		result.Status = "OBSERVED"
		return result, nil
	}
	if policy.validate() != nil {
		return result, providers.ErrRenditionPolicy
	}
	if result.FeeTreatment == "UNVALIDATED" {
		result.Status = "FEE_SCHEME_REVIEW"
		return result, nil
	}
	zone, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return result, err
	}
	if date > now.In(zone).Format(time.DateOnly) {
		result.Status = "DATE_REVIEW"
		return result, nil
	}
	// La identidad padre no se disfraza como referencia bancaria. Cada asignación
	// tiene una identidad única y conserva el vínculo al reporte firmado.
	audit := domain.Audit{CommandID: commandID, Actor: "provider:khipu:rendition", Reason: "Rendición " + id + "; política " + policy.Revision + "; fecha de proveedor, sin verificación de cartola", RecordedAt: now.UTC()}
	if policy.DevelopmentSimulation {
		audit.Actor = "system:dev-rendition-simulation"
		audit.Reason = "SIMULACIÓN DEV; emisor NO autenticado; certificado arbitrario; " + audit.Reason
	}
	guard := types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{TableName: &s.Table, Key: key("ATTEMPT#"+item.AttemptID, "RESOLUTION"), ConditionExpression: aws.String("attribute_not_exists(pk)")}}
	_, _, err = s.ReconcileSettlement(ctx, result.TripID, ref, settled.Settlement.Version, gross, fee, "rendition:"+id+":"+item.PaymentID, date, audit, guard)
	if err != nil {
		return result, err
	}
	result.Status = "APPLIED"
	return result, nil
}

func renditionDeleteJob(table, sk string) types.TransactWriteItem {
	return types.TransactWriteItem{Delete: &types.Delete{TableName: &table, Key: key("RENDITION_JOBS", sk), ConditionExpression: aws.String("#status = :pending"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: "PENDING"}}}}
}

// RescheduleRendition rota el trabajo por fecha para evitar que un lote fallido
// bloquee otros. La misma transacción protege reintentos concurrentes.
func (s Service) RescheduleRendition(ctx context.Context, id, oldSK string, now time.Time, failed bool) error {
	row, err := s.read(ctx, renditionPK(id), "META")
	if err != nil {
		return err
	}
	if row.Rendition == nil {
		return domain.ErrInvalid
	}
	previous := row.Version
	row.Version++
	if failed {
		row.Rendition.Retries++
		row.Rendition.LastError = "PROCESSING_FAILED"
	}
	if row.Rendition.Retries >= 12 && row.Rendition.Status == "PENDING" {
		row.Rendition.Status = "REVIEW_REQUIRED"
	}
	w, err := s.put(row, "#version = :previous", versionValue(previous))
	if err != nil {
		return err
	}
	writes := []types.TransactWriteItem{w, renditionDeleteJob(s.Table, oldSK)}
	if row.Rendition.Status == "PENDING" {
		delay := time.Minute
		if failed {
			delay = 5 * time.Minute
		}
		job := record{PK: "RENDITION_JOBS", SK: renditionJobSK(now.Add(delay), id), AccountID: id, Status: "PENDING"}
		jw, e := s.put(job, "attribute_not_exists(pk)", nil)
		if e != nil {
			return e
		}
		writes = append(writes, jw)
	}
	index := record{PK: "RENDITION_MONTH#" + row.Rendition.ReceivedAt.UTC().Format("2006-01"), SK: id, Rendition: row.Rendition}
	iw, err := s.put(index, "attribute_exists(pk)", nil)
	if err != nil {
		return err
	}
	writes = append(writes, iw)
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: writes})
	return err
}

// ReplayRendition vuelve a comprobar un lote observado/fallido con la política vigente.
// Se conservan commandId deterministas, evidencia y asientos ya aplicados.
func (s Service) ReplayRendition(ctx context.Context, id string, policy RenditionPolicy, actor string, now time.Time) error {
	if !validPortalID(id) || policy.validate() != nil || actor == "" {
		return domain.ErrInvalid
	}
	row, err := s.read(ctx, renditionPK(id), "META")
	if err != nil {
		return err
	}
	if row.Rendition == nil || row.Rendition.Status == "PENDING" {
		return domain.ErrConflict
	}
	previous := row.Version
	priorState := *row.Rendition
	prior := record{PK: row.PK, SK: fmt.Sprintf("RUN#%020d", previous), Rendition: &priorState, RequestedBy: row.RequestedBy}
	row.Version++
	row.Rendition.Status = "PENDING"
	row.Rendition.PendingSince = now
	row.Rendition.Cursor = 0
	row.Rendition.Applied = 0
	row.Rendition.Observed = 0
	row.Rendition.Review = 0
	row.Rendition.Retries = 0
	row.Rendition.LastError = ""
	row.Rendition.Policy = policy
	row.RequestedBy = actor
	w, err := s.put(row, "#version = :previous", versionValue(previous))
	if err != nil {
		return err
	}
	j, err := s.put(record{PK: "RENDITION_JOBS", SK: renditionJobSK(now, id), AccountID: id, Status: "PENDING"}, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	i, err := s.put(record{PK: "RENDITION_MONTH#" + row.Rendition.ReceivedAt.UTC().Format("2006-01"), SK: id, Rendition: row.Rendition}, "attribute_exists(pk)", nil)
	if err != nil {
		return err
	}
	history, err := s.put(prior, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{w, j, i, history}})
	return err
}

func renditionQuery(pk, prefix string) *dynamodb.QueryInput {
	input := &dynamodb.QueryInput{
		KeyConditionExpression:    aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}},
		ConsistentRead:            aws.Bool(true),
		Limit:                     aws.Int32(50),
	}
	// DynamoDB rechaza valores vacíos para atributos de clave, incluso en begins_with.
	if prefix != "" {
		input.KeyConditionExpression = aws.String("pk = :pk AND begins_with(sk, :prefix)")
		input.ExpressionAttributeValues[":prefix"] = &types.AttributeValueMemberS{Value: prefix}
	}
	return input
}
