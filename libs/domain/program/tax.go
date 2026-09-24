package program

// TaxSettings define los porcentajes tributarios usados al calcular un programa.
type TaxSettings struct {
	VATRate             float64 `dynamodbav:"vatRate"             json:"vatRate"             validate:"gte=0,lte=100"`
	CrewWithholdingRate float64 `dynamodbav:"crewWithholdingRate" json:"crewWithholdingRate" validate:"gte=0,lte=100"`
}
