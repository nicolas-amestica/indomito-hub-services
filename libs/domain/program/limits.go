package program

// Limit es un rango numérico con paso opcional (0 cuando el campo no tiene
// paso, como ItemPrice).
type Limit struct {
	Min  float64
	Max  float64
	Step float64
}

// fieldLimits es el gemelo en Go de `constants/field-limits.ts` del frontend
// (`ind-hub-app-ngx-pri-gh`). Es la única fuente de estos rangos en el
// backend: los validadores de api-program la usan en vez de
// declarar los números otra vez. Una divergencia entre este archivo y
// field-limits.ts es un defecto.
//
// Ver la tabla "Límites de los campos numéricos" de requirements.md.
type fieldLimits struct {
	// TotalDays se deriva del rango de fechas; no lo valida un campo de
	// entrada, pero el rango es el mismo que aplica el frontend.
	TotalDays       Limit
	TotalNights     Limit
	TotalPassengers Limit
	// FreePassengers siempre debe ser menor que TotalPassengers.
	FreePassengers Limit
	// UsdIncreaseCLP y BrlIncreaseCLP son montos absolutos en CLP.
	UsdIncreaseCLP Limit
	BrlIncreaseCLP Limit
	// UtilityRate y RechargeRate son porcentajes.
	UtilityRate  Limit
	RechargeRate Limit
	// ItemPrice es el precio unitario de tripulantes (DailyPrice) y de
	// servicios (UnitPrice). Sin paso: la restricción de entero para CLP la
	// aplica el validador, no este rango.
	ItemPrice Limit
	// NameMinLength es el largo mínimo del nombre del programa sobre el valor
	// recortado.
	NameMinLength int
}

// FieldLimits son los límites numéricos de los campos del programa,
// compartidos por api-program para validar el contenido de
// favoritos y el cuerpo del presupuesto respectivamente.
var FieldLimits = fieldLimits{
	TotalDays:       Limit{Min: 1, Max: 100, Step: 1},
	TotalNights:     Limit{Min: 0, Max: 100, Step: 1},
	TotalPassengers: Limit{Min: 1, Max: 100, Step: 1},
	FreePassengers:  Limit{Min: 0, Max: 99, Step: 1},
	UsdIncreaseCLP:  Limit{Min: 0, Max: 200, Step: 5},
	BrlIncreaseCLP:  Limit{Min: 0, Max: 40, Step: 5},
	UtilityRate:     Limit{Min: 0, Max: 100, Step: 1},
	RechargeRate:    Limit{Min: 0, Max: 100, Step: 1},
	ItemPrice:       Limit{Min: 0.01, Max: 99_999_999},
	NameMinLength:   3,
}
