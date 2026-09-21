package program

// Los tipos de este archivo son el contenido de un favorito, así que además de
// viajar en el JSON se guardan enteros en DynamoDB. Por eso declaran
// `dynamodbav` junto a `json`, con los mismos nombres.
//
// La etiqueta duplicada existe porque attributevalue no lee `json` a menos que
// se le pase EncoderOptions.TagKey: sin ella el ítem quedaría escrito con los
// nombres de campo de Go (`TotalPassengers` en vez de `totalPassengers`) y la
// forma guardada dejaría de coincidir con la del contrato. Que los nombres sean
// idénticos es deliberado y no casual — es lo que hace que la forma guardada y
// la transmitida no puedan divergir.
//
// El paquete sigue sin depender de DynamoDB: son metadatos, no una importación
// del SDK. La discusión completa de la decisión, y por qué es la contraria a la
// de services/api-catalog/domain, está en el Godoc de
// services/api-favorite/domain.FavoriteItem. Un campo nuevo acá tiene que
// declarar las dos etiquetas; TestFavoriteItemAttributeNames falla si falta la
// de persistencia.

// ProgramGeneral agrupa los datos generales del programa.
type ProgramGeneral struct {
	Name          string     `dynamodbav:"name"          json:"name"          validate:"required,min=3"`
	Description   *string    `dynamodbav:"description"   json:"description"`
	Plan          CatalogRef `dynamodbav:"plan"          json:"plan"          validate:"required"`
	Season        CatalogRef `dynamodbav:"season"        json:"season"        validate:"required"`
	Destination   CatalogRef `dynamodbav:"destination"   json:"destination"   validate:"required"`
	DepartureCity string     `dynamodbav:"departureCity" json:"departureCity" validate:"required"`
}

// ScheduleContent son las fechas y cantidades que un favorito conserva.
// Omite los campos derivados de ProgramSchedule (TotalDays y PayingPassengers):
// se recalculan al cargar el favorito, no se guardan (Requirement 17.6).
type ScheduleContent struct {
	StartDate       string `dynamodbav:"startDate"       json:"startDate"       validate:"required,datetime=2006-01-02"`
	EndDate         string `dynamodbav:"endDate"         json:"endDate"         validate:"required,datetime=2006-01-02"`
	TotalNights     int    `dynamodbav:"totalNights"     json:"totalNights"     validate:"required,min=1,max=100"`
	TotalPassengers int    `dynamodbav:"totalPassengers" json:"totalPassengers" validate:"required,min=1,max=100"`
	FreePassengers  int    `dynamodbav:"freePassengers"  json:"freePassengers"  validate:"min=0,max=99"`
}

// PricingContent son los parámetros de precio que un favorito conserva. No
// incluye ExchangeSnapshot: al cargar el favorito se usa la tasa vigente, no
// una tasa histórica guardada (Requirements 11.10, 11.17, 17.6).
type PricingContent struct {
	UsdIncreaseCLP int64 `dynamodbav:"usdIncreaseCLP" json:"usdIncreaseCLP" validate:"min=0,max=200"`
	BrlIncreaseCLP int64 `dynamodbav:"brlIncreaseCLP" json:"brlIncreaseCLP" validate:"min=0,max=40"`
	UtilityRate    int   `dynamodbav:"utilityRate"    json:"utilityRate"    validate:"min=0,max=100"`
	RechargeRate   int   `dynamodbav:"rechargeRate"   json:"rechargeRate"   validate:"min=0,max=100"`
}

// CrewMember es un tripulante del programa. Sin montos derivados: el backend
// no calcula, así que BaseAmount y AmountCLP no llegan ni se guardan.
type CrewMember struct {
	Name       string       `dynamodbav:"name"       json:"name"       validate:"required"`
	DocumentID string       `dynamodbav:"documentId" json:"documentId" validate:"required,documentid"`
	DailyPrice float64      `dynamodbav:"dailyPrice" json:"dailyPrice" validate:"required,gte=0.01"`
	Currency   CurrencyCode `dynamodbav:"currency"   json:"currency"   validate:"required,oneof=CLP USD BRL"`
}

// ProgramService es un servicio contratado del programa.
type ProgramService struct {
	Name       string       `dynamodbav:"name"       json:"name"       validate:"required"`
	ChargeType ChargeType   `dynamodbav:"chargeType" json:"chargeType" validate:"required,oneof=fixed per_passenger per_passenger_night per_day per_passenger_day"`
	UnitPrice  float64      `dynamodbav:"unitPrice"  json:"unitPrice"  validate:"required,gte=0.01"`
	Currency   CurrencyCode `dynamodbav:"currency"   json:"currency"   validate:"required,oneof=CLP USD BRL"`
}
