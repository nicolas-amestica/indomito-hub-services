package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
)

// ProviderNotification es el sobre durable mínimo de una notificación ya autenticada.
// No demuestra que el pago esté confirmado: el trabajador todavía debe consultar Khipu.
type ProviderNotification struct {
	PaymentID  string    `json:"paymentId"`
	AttemptID  string    `json:"attemptId"`
	ReceivedAt time.Time `json:"receivedAt"`
}

// ReceiveProviderNotification persiste la señal autenticada y su trabajo antes de
// intentar aplicar dinero. No guarda el cuerpo, firma, correo ni secretos.
func (s Service) ReceiveProviderNotification(ctx context.Context, notification ProviderNotification) error {
	if s.DB == nil || s.Table == "" || !checkoutPaymentID.MatchString(notification.PaymentID) || !validPortalID(notification.AttemptID) || notification.ReceivedAt.IsZero() {
		return domain.ErrInvalid
	}
	digest := sha256.Sum256([]byte(notification.AttemptID + "\x00" + notification.PaymentID))
	fingerprint := hex.EncodeToString(digest[:])
	pk := "PROVIDER_NOTIFICATION#khipu#" + notification.PaymentID
	row := record{PK: pk, SK: "META", Version: 1, Status: "PENDING", Fingerprint: fingerprint, ProviderNotification: &notification}
	job := record{PK: "PROVIDER_JOB#" + notification.ReceivedAt.UTC().Format(time.DateOnly), SK: "PENDING#" + notification.PaymentID, Version: 1, Status: "PENDING", Fingerprint: fingerprint, AccountID: notification.AttemptID}
	first, err := s.put(row, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	second, err := s.put(job, "attribute_not_exists(pk)", nil)
	if err != nil {
		return err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{first, second}})
	if err == nil {
		return nil
	}
	old, readErr := s.read(ctx, pk, "META")
	if readErr == nil {
		if old.Fingerprint != fingerprint || old.ProviderNotification == nil || old.ProviderNotification.AttemptID != notification.AttemptID || old.ProviderNotification.PaymentID != notification.PaymentID {
			return ErrReplayMismatch
		}
		return nil
	}
	var cancelled *types.TransactionCanceledException
	if errors.As(err, &cancelled) {
		return domain.ErrConflict
	}
	return fmt.Errorf("persistir notificacion de proveedor: %w", err)
}

// ProcessProviderNotification verifica el pago contra el proveedor, aplica el
// ingreso idempotente y después cierra inbox/job. Ante cualquier error conserva
// PENDING para que DynamoDB Streams o recuperación manual vuelvan a intentarlo.
func (s Service) ProcessProviderNotification(ctx context.Context, verifier PaymentVerifier, paymentID string, now time.Time, zone *time.Location) (domain.Change, error) {
	if !checkoutPaymentID.MatchString(paymentID) {
		return domain.Change{}, domain.ErrInvalid
	}
	pk := "PROVIDER_NOTIFICATION#khipu#" + paymentID
	row, err := s.read(ctx, pk, "META")
	if err != nil {
		return domain.Change{}, err
	}
	if row.ProviderNotification == nil || row.ProviderNotification.PaymentID != paymentID || !validPortalID(row.ProviderNotification.AttemptID) || row.Version < 1 || (row.Status != "PENDING" && row.Status != "PROCESSED") {
		return domain.Change{}, domain.ErrInvalid
	}
	change, err := s.ConfirmProviderCheckout(ctx, verifier, row.ProviderNotification.AttemptID, paymentID, now, zone)
	if err != nil {
		return domain.Change{}, err
	}
	if row.Status == "PROCESSED" {
		return change, nil
	}
	// Otro trabajador puede haber aplicado y cerrado la misma notificación
	// mientras este verificaba contra el proveedor.
	latest, err := s.read(ctx, pk, "META")
	if err != nil {
		return domain.Change{}, err
	}
	if latest.Status == "PROCESSED" && latest.Fingerprint == row.Fingerprint {
		return change, nil
	}
	if latest.Version != row.Version || latest.Status != "PENDING" || latest.Fingerprint != row.Fingerprint {
		return domain.Change{}, domain.ErrConflict
	}
	previous := row.Version
	row.Version++
	row.Status = "PROCESSED"
	job, err := s.read(ctx, "PROVIDER_JOB#"+row.ProviderNotification.ReceivedAt.UTC().Format(time.DateOnly), "PENDING#"+paymentID)
	if err != nil {
		return domain.Change{}, err
	}
	if job.Fingerprint != row.Fingerprint || job.Status != "PENDING" || job.AccountID != row.ProviderNotification.AttemptID {
		committed, readErr := s.read(ctx, pk, "META")
		if readErr == nil && committed.Status == "PROCESSED" && committed.Fingerprint == row.Fingerprint {
			return change, nil
		}
		return domain.Change{}, domain.ErrInvalid
	}
	jobPrevious := job.Version
	job.Version++
	job.Status = "DONE"
	inboxWrite, err := s.put(row, "#version = :previous", versionValue(previous))
	if err != nil {
		return domain.Change{}, err
	}
	jobWrite, err := s.put(job, "#version = :previous", versionValue(jobPrevious))
	if err != nil {
		return domain.Change{}, err
	}
	_, err = s.DB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{inboxWrite, jobWrite}})
	if err != nil {
		committed, readErr := s.read(ctx, pk, "META")
		if readErr == nil && committed.Status == "PROCESSED" && committed.Fingerprint == row.Fingerprint {
			return change, nil
		}
		return domain.Change{}, fmt.Errorf("cerrar notificacion de proveedor: %w", err)
	}
	return change, nil
}
