// Package program contiene los tipos de dominio del programa que comparten
// api-catalog, api-favorite y api-program. Ningún servicio importa de otro:
// los tres dependen únicamente de esta librería (Requirement 17.5, 17.6).
package program

// ChargeType es el tipo de cobro de un servicio del programa. Define el
// multiplicador que aplica el motor de cálculo del frontend sobre el precio
// unitario. El backend no calcula: solo valida que el valor recibido sea uno
// de los cinco declarados aquí.
type ChargeType string

const (
	// ChargeFixed es un valor único: precio fijo, independiente de pasajeros y días.
	ChargeFixed ChargeType = "fixed"
	// ChargePerPassenger se cobra una vez por pasajero.
	ChargePerPassenger ChargeType = "per_passenger"
	// ChargePerPassengerNight se cobra por pasajero por noche.
	ChargePerPassengerNight ChargeType = "per_passenger_night"
	// ChargePerDay se cobra por día, independiente de la cantidad de pasajeros.
	ChargePerDay ChargeType = "per_day"
	// ChargePerPassengerDay se cobra por pasajero por día.
	ChargePerPassengerDay ChargeType = "per_passenger_day"
)

// CurrencyCode es una moneda soportada por el cálculo del programa.
type CurrencyCode string

const (
	// CurrencyCLP es el peso chileno.
	CurrencyCLP CurrencyCode = "CLP"
	// CurrencyUSD es el dólar estadounidense.
	CurrencyUSD CurrencyCode = "USD"
	// CurrencyBRL es el real brasileño.
	CurrencyBRL CurrencyCode = "BRL"
)
