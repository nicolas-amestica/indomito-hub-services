package program

// ExchangeSnapshot son los tipos de cambio con los que se calcula el
// programa. Es la respuesta del Exchange_Rate_Endpoint (`GET /tasas-cambio`)
// y no es parte de ningún cuerpo entrante: no se persiste, porque el
// programa tampoco se persiste (Requirement 17).
type ExchangeSnapshot struct {
	// Date es la fecha, en formato ISO 8601, informada por la fuente de tasas.
	Date     string `json:"date"`
	UsdToClp int64  `json:"usdToClp"`
	BrlToClp int64  `json:"brlToClp"`
	// Source identifica al proveedor externo que originó los valores. Si el
	// snapshot se sirve desde DynamoDB conserva su fuente original.
	Source ExchangeRateSource `json:"source"`
	// IsFallback es verdadero cuando los valores provienen del último
	// snapshot conocido y no de la fuente externa.
	IsFallback bool `json:"isFallback"`
}

// ExchangeRateSource identifica una fuente auditable de tipos de cambio.
type ExchangeRateSource string

const (
	// ExchangeRateSourceBCCH corresponde a la API BDE del Banco Central.
	ExchangeRateSourceBCCH ExchangeRateSource = "banco-central"
	// ExchangeRateSourceCurrencyAPI corresponde a @fawazahmed0/currency-api.
	ExchangeRateSourceCurrencyAPI ExchangeRateSource = "currency-api"
	// ExchangeRateSourceUnknown cubre snapshots antiguos sin procedencia.
	ExchangeRateSourceUnknown ExchangeRateSource = "unknown"
)

// ExchangeRateOrigin es la procedencia auditable que se guarda junto al
// contenido de un programa favorito, sin convertir la tasa histórica en un
// insumo reutilizable para futuros cálculos.
type ExchangeRateOrigin struct {
	Date       string             `dynamodbav:"date"       json:"date"       validate:"required,datetime=2006-01-02"`
	Source     ExchangeRateSource `dynamodbav:"source"     json:"source"     validate:"required,oneof=banco-central currency-api unknown"`
	IsFallback bool               `dynamodbav:"isFallback" json:"isFallback"`
}
