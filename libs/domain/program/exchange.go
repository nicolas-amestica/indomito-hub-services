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
	// IsFallback es verdadero cuando los valores provienen del último
	// snapshot conocido y no de la fuente externa.
	IsFallback bool `json:"isFallback"`
}
