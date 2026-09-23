package domain

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// validRequest devuelve un cuerpo de forma válida, del que cada caso de prueba
// desvía un solo campo. Es un programa mínimo pero completo: dos escenarios
// coherentes y un destino con plantilla.
func validRequest() BudgetRequest {
	return BudgetRequest{
		ProgramName: "Gira Florianópolis 4° medio",
		Destination: program.CatalogRef{
			ID:               "BRF",
			Display:          "Brasil - Florianópolis",
			BudgetTemplateID: "BRF",
		},
		DepartureCity: "Santiago",
		TotalDays:     8,
		TotalNights:   7,
		ServiceNames:  []string{"Hotel", "Traslados"},
		Scenarios: []BudgetScenario{
			{
				ScenarioShape: program.ScenarioShape{
					TotalPassengers:  30,
					FreePassengers:   2,
					PayingPassengers: 28,
				},
				PricePerPassengerCLP: 1_250_000,
			},
			{
				ScenarioShape: program.ScenarioShape{
					TotalPassengers:  40,
					FreePassengers:   3,
					PayingPassengers: 37,
				},
				PricePerPassengerCLP: 1_100_000,
			},
		},
	}
}

// scenario arma un escenario con las cuatro cantidades explícitas, para los
// casos que desvían una sola.
func scenario(total, free, paying int, price int64) BudgetScenario {
	return BudgetScenario{
		ScenarioShape: program.ScenarioShape{
			TotalPassengers:  total,
			FreePassengers:   free,
			PayingPassengers: paying,
		},
		PricePerPassengerCLP: price,
	}
}

// TestBudgetRequestShapeValidation contrasta las etiquetas `validate` del cuerpo
// contra lambdautil.ValidateStruct, que es lo que corre en el endpoint.
//
// Comprueba el código de error además del rechazo, porque la distinción entre
// REQUIRED_FIELD_MISSING y VALIDATION_ERROR es la que le permite al formulario
// diferenciar "falta completar" de "el valor no sirve" (Requirement 18.4).
func TestBudgetRequestShapeValidation(t *testing.T) {
	tests := []struct {
		name string
		// mutate desvía un campo del cuerpo válido.
		mutate func(req *BudgetRequest)
		// wantCode es el código esperado, o "" si el cuerpo debe aceptarse.
		wantCode string
		// wantField es la ruta del campo que el error señala. Vacía cuando no
		// se comprueba.
		wantField string
	}{
		{
			name:   "cuerpo completo",
			mutate: func(*BudgetRequest) {},
		},
		{
			name:      "sin nombre de programa",
			mutate:    func(req *BudgetRequest) { req.ProgramName = "" },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "programName",
		},
		{
			name:      "nombre de programa más corto que el mínimo",
			mutate:    func(req *BudgetRequest) { req.ProgramName = "ab" },
			wantCode:  apperr.CodeValidationError,
			wantField: "programName",
		},
		{
			name:      "sin destino",
			mutate:    func(req *BudgetRequest) { req.Destination = program.CatalogRef{} },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "destination",
		},
		{
			name:      "destino sin identificador",
			mutate:    func(req *BudgetRequest) { req.Destination.ID = "" },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "destination.id",
		},
		{
			name:      "destino sin etiqueta visible",
			mutate:    func(req *BudgetRequest) { req.Destination.Display = "" },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "destination.display",
		},
		{
			// La forma no exige la plantilla: el Requirement 18.8 la resuelve
			// contra el registro, y eso es de fn-validate-request.go.
			name:   "destino sin plantilla de presupuesto",
			mutate: func(req *BudgetRequest) { req.Destination.BudgetTemplateID = "" },
		},
		{
			name:      "sin ciudad de salida",
			mutate:    func(req *BudgetRequest) { req.DepartureCity = "" },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "departureCity",
		},
		{
			name:      "sin días totales",
			mutate:    func(req *BudgetRequest) { req.TotalDays = 0 },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "totalDays",
		},
		{
			name:      "días totales sobre el máximo",
			mutate:    func(req *BudgetRequest) { req.TotalDays = 101 },
			wantCode:  apperr.CodeValidationError,
			wantField: "totalDays",
		},
		{
			name:      "días totales negativos",
			mutate:    func(req *BudgetRequest) { req.TotalDays = -1 },
			wantCode:  apperr.CodeValidationError,
			wantField: "totalDays",
		},
		{
			name:      "noches negativas",
			mutate:    func(req *BudgetRequest) { req.TotalNights = -1 },
			wantCode:  apperr.CodeValidationError,
			wantField: "totalNights",
		},
		{
			name:      "noches sobre el máximo",
			mutate:    func(req *BudgetRequest) { req.TotalNights = 101 },
			wantCode:  apperr.CodeValidationError,
			wantField: "totalNights",
		},
		{
			// Más noches que días es una advertencia del formulario
			// (Requirement 3.10), no un rechazo del endpoint.
			name: "más noches que días",
			mutate: func(req *BudgetRequest) {
				req.TotalDays = 5
				req.TotalNights = 9
			},
		},
		{
			name:   "sin servicios",
			mutate: func(req *BudgetRequest) { req.ServiceNames = nil },
		},
		{
			name:      "servicio con nombre vacío",
			mutate:    func(req *BudgetRequest) { req.ServiceNames = []string{"Hotel", ""} },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "serviceNames[1]",
		},
		{
			name: "servicios sobre el máximo",
			mutate: func(req *BudgetRequest) {
				req.ServiceNames = make([]string, MaxServiceNames+1)
				for i := range req.ServiceNames {
					req.ServiceNames[i] = "Servicio"
				}
			},
			wantCode:  apperr.CodeValidationError,
			wantField: "serviceNames",
		},
		{
			name:      "sin escenarios",
			mutate:    func(req *BudgetRequest) { req.Scenarios = nil },
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "scenarios",
		},
		{
			// Un `scenarios: []` cumple `required` —el validador solo comprueba
			// que el slice no sea nil— e incumple `min`, así que da el
			// VALIDATION_ERROR del Requirement 18.5 y no el
			// REQUIRED_FIELD_MISSING del caso anterior.
			name:      "escenarios vacíos",
			mutate:    func(req *BudgetRequest) { req.Scenarios = []BudgetScenario{} },
			wantCode:  apperr.CodeValidationError,
			wantField: "scenarios",
		},
		{
			name: "cantidad máxima de escenarios",
			mutate: func(req *BudgetRequest) {
				req.Scenarios = make([]BudgetScenario, 0, MaxScenarios)
				for i := 0; i < MaxScenarios; i++ {
					req.Scenarios = append(req.Scenarios, scenario(20+i, 1, 19+i, 1_000_000))
				}
			},
		},
		{
			name: "escenarios sobre el máximo",
			mutate: func(req *BudgetRequest) {
				req.Scenarios = make([]BudgetScenario, 0, MaxScenarios+1)
				for i := 0; i <= MaxScenarios; i++ {
					req.Scenarios = append(req.Scenarios, scenario(20+i, 1, 19+i, 1_000_000))
				}
			},
			wantCode:  apperr.CodeValidationError,
			wantField: "scenarios",
		},
		{
			// Las tres cantidades del escenario las declara el embebido
			// program.ScenarioShape, y su nombre aparece en la ruta del campo:
			// `scenarios[1].ScenarioShape.totalPassengers`. Sale de
			// lambdautil.jsonFieldName, que devuelve vacío para un campo
			// anónimo sin etiqueta json y deja al validador cayendo de vuelta
			// al identificador Go.
			//
			// Queda fijado acá porque es lo que el cliente recibe hoy, no
			// porque sea deseable: el frontend usa esa ruta para marcar el
			// control y `ScenarioShape` no existe en su contrato. Corregirlo es
			// de libs/lambdautil, no de este paquete, y alcanzaría a cualquier
			// tipo con campos embebidos.
			name: "escenario sin pasajeros",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[1] = scenario(0, 0, 1, 1_000_000)
			},
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "scenarios[1].ScenarioShape.totalPassengers",
		},
		{
			name: "escenario con pasajeros sobre el máximo",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(101, 1, 100, 1_000_000)
			},
			wantCode:  apperr.CodeValidationError,
			wantField: "scenarios[0].ScenarioShape.totalPassengers",
		},
		{
			name: "escenario con liberados negativos",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(30, -1, 31, 1_000_000)
			},
			wantCode:  apperr.CodeValidationError,
			wantField: "scenarios[0].ScenarioShape.freePassengers",
		},
		{
			name: "escenario con liberados sobre el máximo",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(100, 100, 1, 1_000_000)
			},
			wantCode:  apperr.CodeValidationError,
			wantField: "scenarios[0].ScenarioShape.freePassengers",
		},
		{
			name: "escenario sin pagantes",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(30, 30, 0, 1_000_000)
			},
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "scenarios[0].ScenarioShape.payingPassengers",
		},
		{
			// Un precio en 0 incumple `required` y no `gt`, así que la forma lo
			// rechaza con REQUIRED_FIELD_MISSING. El Requirement 18.6 pide
			// VALIDATION_ERROR, y esa es la razón por la que
			// fn-validate-request.go comprueba el precio por su cuenta.
			name: "escenario con precio en cero",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(30, 2, 28, 0)
			},
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "scenarios[0].pricePerPassengerCLP",
		},
		{
			name: "escenario con precio negativo",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(30, 2, 28, -1)
			},
			wantCode:  apperr.CodeValidationError,
			wantField: "scenarios[0].pricePerPassengerCLP",
		},
		{
			// Las dos incoherencias entre campos del escenario pasan la
			// validación de forma: son los Requirements 18.6 y 18.7, que
			// resuelve fn-validate-request.go.
			name: "escenario con pagantes incoherentes",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(30, 2, 15, 1_000_000)
			},
		},
		{
			name: "escenario con más liberados que pasajeros",
			mutate: func(req *BudgetRequest) {
				req.Scenarios[0] = scenario(10, 20, 5, 1_000_000)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest()
			tc.mutate(&req)

			err := lambdautil.ValidateStruct(req)

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("ValidateStruct() error = %v, want nil", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("ValidateStruct() error = nil, want %s", tc.wantCode)
			}

			var appErr *apperr.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("ValidateStruct() error = %T, want *apperr.AppError", err)
			}
			if appErr.Code() != tc.wantCode {
				t.Errorf("Code() = %q, want %q", appErr.Code(), tc.wantCode)
			}
			if appErr.HTTPStatus() != 400 {
				t.Errorf("HTTPStatus() = %d, want 400", appErr.HTTPStatus())
			}
			if tc.wantField == "" {
				return
			}
			if got := appErr.Details()["field"]; got != tc.wantField {
				t.Errorf("details[field] = %v, want %q", got, tc.wantField)
			}
		})
	}
}

// TestBudgetRequestTagsMatchLimits ata los literales de las etiquetas `validate`
// a las constantes que los declaran.
//
// Una etiqueta solo admite literales, así que los números están escritos dos
// veces: en la etiqueta y en program.FieldLimits o en las constantes de este
// paquete. Este test es lo que impide que las dos copias se separen, que es el
// modo de falla del que advierte limits.go.
func TestBudgetRequestTagsMatchLimits(t *testing.T) {
	tests := []struct {
		target    any
		field     string
		rule      string
		wantParam string
	}{
		{BudgetRequest{}, "programName", "min", strconv.Itoa(program.FieldLimits.NameMinLength)},
		{BudgetRequest{}, "totalDays", "min", limitParam(program.FieldLimits.TotalDays.Min)},
		{BudgetRequest{}, "totalDays", "max", limitParam(program.FieldLimits.TotalDays.Max)},
		{BudgetRequest{}, "totalNights", "min", limitParam(program.FieldLimits.TotalNights.Min)},
		{BudgetRequest{}, "totalNights", "max", limitParam(program.FieldLimits.TotalNights.Max)},
		{BudgetRequest{}, "serviceNames", "max", strconv.Itoa(MaxServiceNames)},
		{BudgetRequest{}, "scenarios", "min", strconv.Itoa(MinScenarios)},
		{BudgetRequest{}, "scenarios", "max", strconv.Itoa(MaxScenarios)},
		{BudgetScenario{}, "totalPassengers", "min", limitParam(program.FieldLimits.TotalPassengers.Min)},
		{BudgetScenario{}, "totalPassengers", "max", limitParam(program.FieldLimits.TotalPassengers.Max)},
		{BudgetScenario{}, "freePassengers", "min", limitParam(program.FieldLimits.FreePassengers.Min)},
		{BudgetScenario{}, "freePassengers", "max", limitParam(program.FieldLimits.FreePassengers.Max)},
	}

	for _, tc := range tests {
		name := reflect.TypeOf(tc.target).Name() + "." + tc.field + "/" + tc.rule
		t.Run(name, func(t *testing.T) {
			rules := validateRules(t, tc.target, tc.field)

			got, ok := rules[tc.rule]
			if !ok {
				t.Fatalf("el campo %s no declara la regla %q, reglas = %v", tc.field, tc.rule, rules)
			}
			if got != tc.wantParam {
				t.Errorf("regla %s = %q, want %q", tc.rule, got, tc.wantParam)
			}
		})
	}
}

// TestMaxScenariosMatchesFrontend fija la cota de escenarios en el mismo valor
// que MAX_SCENARIOS de `constants/scenario-defaults.ts` del frontend
// (Requirement 13.11).
//
// Es el único número del cuerpo que no está en program.FieldLimits, porque su
// gemelo en el frontend tampoco está en field-limits.ts. Si el formulario sube
// su cota y el backend no, el endpoint rechaza escenarios que el formulario
// envía; este test deja el acuerdo escrito en un lugar que falla.
func TestMaxScenariosMatchesFrontend(t *testing.T) {
	const frontendMaxScenarios = 4

	if MaxScenarios != frontendMaxScenarios {
		t.Errorf("MaxScenarios = %d, want %d (MAX_SCENARIOS de scenario-defaults.ts)",
			MaxScenarios, frontendMaxScenarios)
	}
	if MinScenarios != 1 {
		t.Errorf("MinScenarios = %d, want 1", MinScenarios)
	}
}

// validateRules devuelve las reglas de la etiqueta `validate` del campo con ese
// nombre json, indexadas por regla. Una regla sin parámetro queda con valor
// vacío.
//
// Busca dentro de los campos embebidos, porque las cantidades de
// [BudgetScenario] las declara program.ScenarioShape.
func validateRules(t *testing.T, target any, jsonName string) map[string]string {
	t.Helper()

	field, ok := findFieldByJSONName(reflect.TypeOf(target), jsonName)
	if !ok {
		t.Fatalf("el tipo %s no declara un campo con json %q", reflect.TypeOf(target).Name(), jsonName)
	}

	rules := map[string]string{}
	for _, rule := range strings.Split(field.Tag.Get("validate"), ",") {
		name, param, _ := strings.Cut(rule, "=")
		rules[name] = param
	}

	return rules
}

// findFieldByJSONName busca un campo por el nombre de su etiqueta json,
// descendiendo a los campos embebidos.
func findFieldByJSONName(structType reflect.Type, jsonName string) (reflect.StructField, bool) {
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)

		if strings.SplitN(field.Tag.Get("json"), ",", 2)[0] == jsonName {
			return field, true
		}

		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			if found, ok := findFieldByJSONName(field.Type, jsonName); ok {
				return found, true
			}
		}
	}

	return reflect.StructField{}, false
}

// limitParam formatea un límite de program.FieldLimits como lo escribe una
// etiqueta `validate`.
func limitParam(limit float64) string {
	return strconv.FormatInt(int64(limit), 10)
}
