package domain

// PaymentPortal conserva las instrucciones emitidas en el PDF aprobado, no el estado de los pagos.
type PaymentPortal struct {
	URL      string `json:"url" dynamodbav:"url"`
	TripCode string `json:"tripCode" dynamodbav:"tripCode"`
}
