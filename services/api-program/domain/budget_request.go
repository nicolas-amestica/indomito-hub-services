// Package domain define el cuerpo de POST /programas:presupuesto, el único
// endpoint de api-program (Requirement 17.1).
//
// El paquete declara la forma del cuerpo y nada más. No calcula: los precios por
// escenario llegan calculados y redondeados por el motor del frontend, y este
// servicio los maqueta (Requirement 18.1). Por eso los montos en CLP son int64 y
// no float64: un float acá sería una invitación a recalcular, y el motor en Go se
// eliminó del diseño a propósito para no mantener dos implementaciones del mismo
// cálculo.
//
// Lo que las etiquetas `validate` cubren y lo que no:
//
//   - Cubren la forma del cuerpo: campos obligatorios presentes, los rangos de
//     días, noches y pasajeros, y la cantidad de escenarios entre
//     [MinScenarios] y [MaxScenarios]. libs/lambdautil.ValidateStruct las
//     traduce a REQUIRED_FIELD_MISSING cuando falta un campo obligatorio
//     (Requirement 18.4) y a VALIDATION_ERROR en cualquier otro
//     incumplimiento.
//   - No cubren la coherencia entre campos: que cada escenario tenga al menos
//     un pagante, que payingPassengers sea totalPassengers menos
//     freePassengers, y que el budgetTemplateId del destino corresponda a una
//     plantilla registrada. Esas comparaciones son de
//     functions/generar-presupuesto-v1/fn-validate-request.go (Requirements
//     18.6, 18.7 y 18.8), que además decide en qué orden corre respecto de
//     ValidateStruct: un cero en un campo con `required` produce
//     REQUIRED_FIELD_MISSING, así que un precio por persona en 0 solo produce
//     el VALIDATION_ERROR del Requirement 18.6 si esa comprobación corre
//     primero.
package domain

import (
	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// BudgetScenario es una columna del presupuesto: las cantidades de pasajeros del
// escenario más su precio por persona ya calculado.
//
// Es un alias de program.BudgetScenario y no un tipo nuevo. El tipo compartido ya
// declara las etiquetas `validate` del diseño y los rangos que salen de
// program.FieldLimits; un tipo propio los duplicaría y obligaría a convertir en
// cada frontera. El alias le da al paquete el nombre local con el que el
// compositor de PDF arma el bloque ScenarioTable, sin agregar una segunda
// declaración de la misma forma.
type BudgetScenario = program.BudgetScenario

// Cotas de la cantidad de elementos que acepta el cuerpo.
//
// Las etiquetas `validate` solo admiten literales, así que estas constantes no
// aparecen en las etiquetas. Existen para que el literal tenga un nombre y para
// que TestBudgetRequestTagsMatchLimits ate uno al otro: cambiar el literal sin
// cambiar la constante rompe ese test.
const (
	// MinScenarios es la cantidad mínima de escenarios del presupuesto: cero
	// escenarios no imprimen ninguna columna (Requirements 13.11 y 18.5).
	MinScenarios = 1

	// MaxScenarios es la cantidad máxima de escenarios: el documento tiene
	// cuatro columnas y una quinta no cabe (Requirements 13.11 y 18.5).
	//
	// Es el gemelo de MAX_SCENARIOS de `constants/scenario-defaults.ts` del
	// frontend, que también vale 4. Una divergencia entre ambos es un defecto:
	// el formulario enviaría escenarios que el endpoint rechaza.
	MaxScenarios = 4

	// MaxServiceNames acota la lista de nombres de servicios. No es un límite
	// del formulario sino una cota de sanidad del cuerpo: cien servicios ya
	// exceden cualquier programa real, y sin tope una lista arbitrariamente
	// larga llegaría hasta el compositor de PDF.
	MaxServiceNames = 100
)

// BudgetRequest es el cuerpo de POST /programas:presupuesto (Requirement 13.10).
//
// Trae el programa reducido a lo que el documento imprime: los datos generales
// que van en el encabezado, la duración, los nombres de los servicios y las
// columnas de escenarios con su precio. No trae tripulantes, precios unitarios,
// parámetros de margen ni tasas de cambio, porque el documento no los muestra y
// el servicio no recalcula nada con ellos.
type BudgetRequest struct {
	// ProgramName encabeza el documento. El mínimo de 3 es
	// program.FieldLimits.NameMinLength.
	ProgramName string `json:"programName" validate:"required,min=3"`

	// Destination es el destino del programa. Su BudgetTemplateID selecciona la
	// plantilla del documento (Requirement 18.3).
	//
	// BudgetTemplateID no lleva `required` en program.CatalogRef, y la ausencia
	// es deliberada: un destino sin plantilla, igual que uno con una plantilla
	// que nadie registró, es el VALIDATION_ERROR del Requirement 18.8, que se
	// resuelve contra el registro de plantillas y no contra la forma del
	// cuerpo.
	Destination program.CatalogRef `json:"destination" validate:"required"`

	// DepartureCity es la ciudad de salida del viaje.
	DepartureCity string `json:"departureCity" validate:"required"`

	// TotalDays son los días totales del programa, ingresados por el usuario.
	// Acá se comprueba su rango, que es
	// program.FieldLimits.TotalDays (1 a 100).
	TotalDays int `json:"totalDays" validate:"required,min=1,max=100"`

	// TotalNights son las noches de estadía. Rango:
	// program.FieldLimits.TotalNights (0 a 100).
	//
	// El backend no compara noches contra días: la incoherencia entre ambos es
	// una advertencia no bloqueante del formulario (Requirement 3.10), no un
	// rechazo del endpoint.
	TotalNights int `json:"totalNights" validate:"min=0,max=100"`

	// ServiceNames son los nombres de los servicios contratados, para el bloque
	// ServiceList del documento. Solo los nombres: el presupuesto lista los
	// servicios incluidos, no los valoriza fila por fila.
	//
	// El tope de 100 es [MaxServiceNames]. El `dive,required` rechaza un nombre
	// vacío dentro de la lista, que imprimiría una fila en blanco. La lista
	// vacía se acepta y no lleva `required`: el formulario ya expone al menos
	// una fila de servicios (Requirement 6.1), así que un cuerpo sin servicios
	// no viene del formulario, y un presupuesto con la sección vacía es un
	// documento pobre pero no un documento imposible.
	ServiceNames []string `json:"serviceNames" validate:"max=100,dive,required"`

	// Scenarios son las columnas del presupuesto, entre [MinScenarios] y
	// [MaxScenarios].
	//
	// El `dive` aplica a cada elemento las etiquetas de [BudgetScenario]: sin
	// él, un escenario con cantidades fuera de rango pasaría sin revisión.
	//
	// Las dos formas de incumplir la cantidad dan códigos distintos, y las dos
	// son las que piden los requisitos: un `scenarios` ausente incumple
	// `required` y produce REQUIRED_FIELD_MISSING (Requirement 18.4), mientras
	// que un `scenarios: []` lo cumple —el validador solo comprueba que el
	// slice no sea nil— e incumple `min`, igual que una lista de cinco
	// incumple `max`, así que ambos producen el VALIDATION_ERROR del
	// Requirement 18.5.
	Scenarios []BudgetScenario `json:"scenarios" validate:"required,min=1,max=4,dive"`
}
