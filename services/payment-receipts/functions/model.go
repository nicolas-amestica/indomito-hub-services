// Package functions procesa de forma idempotente los comprobantes pendientes.
package functions

import "time"

type financialEvent struct {
	Type          string    `dynamodbav:"Type"`
	AccountID     string    `dynamodbav:"AccountID"`
	TripID        string    `dynamodbav:"TripID"`
	Amount        int64     `dynamodbav:"Amount"`
	EffectiveDate string    `dynamodbav:"EffectiveDate"`
	RecordedAt    time.Time `dynamodbav:"RecordedAt"`
}

type receiptRow struct {
	PK                string          `dynamodbav:"pk"`
	SK                string          `dynamodbav:"sk"`
	ReceiptID         string          `dynamodbav:"receiptId"`
	DeliveryID        string          `dynamodbav:"deliveryId"`
	ReceiptEmail      string          `dynamodbav:"receiptEmail"`
	Status            string          `dynamodbav:"status"`
	DeliveryMode      string          `dynamodbav:"deliveryMode"`
	DeliveryAttempts  int64           `dynamodbav:"deliveryAttempts"`
	LeaseOwner        string          `dynamodbav:"leaseOwner"`
	DocumentKey       string          `dynamodbav:"documentKey"`
	DocumentSHA256    string          `dynamodbav:"documentSha256"`
	DocumentVersion   int64           `dynamodbav:"documentVersion"`
	PassengerName     string          `dynamodbav:"passengerName"`
	PassengerDocument string          `dynamodbav:"passengerDocument"`
	Event             *financialEvent `dynamodbav:"event"`
}

type receiptModel struct {
	ID, AccountID, TripID, EffectiveDate, PassengerName, PassengerDocument string
	VerificationURL                                                        string
	Amount                                                                 int64
	RecordedAt                                                             time.Time
	Review, Deposit, GroupDeposit                                          bool
	Version                                                                int64
}

type renderedDocument struct {
	Bytes  []byte
	SHA256 string
	Key    string
}

type job struct {
	PK, SK, ReceiptID, DeliveryID string
}

const (
	statusSent           = "SENT"
	statusDocumentReady  = "DOCUMENT_READY"
	statusDeliveryFailed = "DELIVERY_FAILED"
)
