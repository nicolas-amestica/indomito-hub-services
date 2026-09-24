package domain

import (
	"time"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// RateItem es la representación en DynamoDB del snapshot de respaldo de tasas
// de cambio: pk = RATES, sk = LATEST (Requirement 14.4).
//
// Es un único ítem que se sobrescribe con PutItem en cada consulta exitosa a la
// fuente externa. No hay historial: lo que el respaldo necesita es "la última
// tasa buena que vimos", y guardar la serie completa agregaría almacenamiento y
// una decisión de retención para un dato que nadie va a consultar hacia atrás.
//
// Vive en su propia clave de partición, separado de [CatalogPK], porque lo lee y
// escribe el otro endpoint del servicio: mezclarlo con los catálogos haría que
// cada consulta de catálogos arrastrara un ítem que no necesita.
type RateItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	// Date es la fecha informada por la fuente de tasas, en formato ISO 8601.
	// Es la fecha que se entrega cuando el snapshot se sirve como respaldo, y
	// por eso se conserva tal como vino y no se reemplaza por la de la lectura
	// (Requirement 14.8).
	Date string `dynamodbav:"date"`

	// UsdToClp es el valor del dólar en pesos, ya redondeado al entero más
	// cercano (Requirement 14.3).
	UsdToClp int64 `dynamodbav:"usdToClp"`

	// BrlToClp es el valor del real en pesos, ya redondeado.
	BrlToClp int64 `dynamodbav:"brlToClp"`

	// Source conserva el proveedor que originó el snapshot.
	Source program.ExchangeRateSource `dynamodbav:"source"`

	// FetchedAt es el instante de la consulta exitosa que escribió el ítem.
	// attributevalue lo guarda como cadena RFC3339Nano.
	//
	// Conviven con Date porque responden preguntas distintas —de cuándo es la
	// tasa y cuándo la conseguimos— y en un respaldo entregado tres días
	// después no coinciden.
	FetchedAt time.Time `dynamodbav:"fetchedAt"`
}

// NewRateItem arma el snapshot a partir de las tasas que se acaban de obtener
// de la fuente externa y del instante en que se obtuvieron.
//
// Ignora la marca IsFallback del snapshot recibido: lo que se persiste es
// siempre un dato fresco, y la condición de respaldo se decide al leer el ítem,
// no al escribirlo.
func NewRateItem(snapshot program.ExchangeSnapshot, fetchedAt time.Time) RateItem {
	key := RatesSnapshotKey()

	return RateItem{
		PK:        key.PK,
		SK:        key.SK,
		Date:      snapshot.Date,
		UsdToClp:  snapshot.UsdToClp,
		BrlToClp:  snapshot.BrlToClp,
		Source:    snapshot.Source,
		FetchedAt: fetchedAt.UTC(),
	}
}

// FallbackSnapshot traduce el ítem a la respuesta de tasas, con su fecha
// original y la marca de respaldo activada (Requirement 14.8).
//
// La marca queda fija en verdadero porque este ítem se lee en un solo caso:
// después de que los tres intentos contra la fuente externa fallaron. Un
// snapshot entregado al cliente es, por definición, una tasa de una fecha
// anterior, y el Requirement 1.7 obliga a decírselo.
func (i RateItem) FallbackSnapshot() program.ExchangeSnapshot {
	source := i.Source
	if source == "" {
		source = program.ExchangeRateSourceUnknown
	}

	return program.ExchangeSnapshot{
		Date:       i.Date,
		UsdToClp:   i.UsdToClp,
		BrlToClp:   i.BrlToClp,
		Source:     source,
		IsFallback: true,
	}
}
