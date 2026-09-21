package program

// ScenarioShape son las cantidades de un escenario del presupuesto, antes de
// calcular su precio. Es la salida de `deriveScenarios` en el frontend.
type ScenarioShape struct {
	// TotalPassengers son los pasajeros del programa más el desplazamiento del
	// escenario, acotado a un mínimo de 1.
	TotalPassengers int `json:"totalPassengers" validate:"required,min=1,max=100"`
	// FreePassengers se deriva de la proporción de liberados del programa.
	FreePassengers int `json:"freePassengers" validate:"min=0,max=99"`
	// PayingPassengers es TotalPassengers menos FreePassengers, con un mínimo de 1.
	PayingPassengers int `json:"payingPassengers" validate:"required,min=1"`
}

// BudgetScenario es un escenario del presupuesto, ya con su precio calculado
// por el motor de cálculo del frontend. Es una columna del cuerpo que recibe
// el Budget_Pdf_Endpoint (`POST /programas:presupuesto`).
type BudgetScenario struct {
	ScenarioShape
	// PricePerPassengerCLP lo calcula el frontend; este servicio lo maqueta.
	PricePerPassengerCLP int64 `json:"pricePerPassengerCLP" validate:"required,gt=0"`
}
