package generarpresupuestov1

import (
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1/templates"
)

const (
	reasonShapeValidation         = "shapeValidation"
	reasonScenarioCount           = "scenarioCount"
	reasonTemplateNotRegistered   = "templateNotRegistered"
	reasonPriceNotPositive        = "priceNotPositive"
	reasonNoPayingPassengers      = "noPayingPassengers"
	reasonInconsistentPayingCount = "inconsistentPayingPassengers"
	globalValidationScenarioIndex = -1
)

// ValidateRequest valida estructura y coherencia interna sin recalcular ningún
// precio. Las comprobaciones imposibles corren antes de las etiquetas para que
// un precio o pagante en cero sea VALIDATION_ERROR y no REQUIRED_FIELD_MISSING.
func ValidateRequest(request domain.BudgetRequest, registry templates.Registry) error {
	// Un slice nil representa un campo ausente. Se delega de inmediato para
	// conservar REQUIRED_FIELD_MISSING; un slice presente pero vacío sí es el
	// rango imposible del Requirement 18.5.
	if request.Scenarios == nil {
		return lambdautil.ValidateStruct(request)
	}
	if len(request.Scenarios) < domain.MinScenarios || len(request.Scenarios) > domain.MaxScenarios {
		return validationFailure(
			reasonScenarioCount,
			globalValidationScenarioIndex,
			"La cantidad de escenarios debe estar entre 1 y 4",
		)
	}

	// Si falta el destino, las etiquetas entregan el error específico de campo.
	// Con el destino presente, un template vacío o desconocido es el mismo error
	// de catálogo: no hay una plantilla que el compositor pueda seleccionar.
	if request.Destination.ID == "" || request.Destination.Display == "" {
		return lambdautil.ValidateStruct(request)
	}
	if _, ok := registry.Lookup(request.Destination.BudgetTemplateID); !ok {
		return validationFailure(
			reasonTemplateNotRegistered,
			globalValidationScenarioIndex,
			"El destino no tiene una plantilla de presupuesto registrada",
		)
	}

	for index, scenario := range request.Scenarios {
		scenarioIndex := index + 1
		if scenario.PricePerPassengerCLP <= 0 {
			return validationFailure(
				reasonPriceNotPositive,
				scenarioIndex,
				"El precio por persona debe ser mayor que cero",
			)
		}
		if scenario.PayingPassengers < 1 || scenario.FreePassengers >= scenario.TotalPassengers {
			return validationFailure(
				reasonNoPayingPassengers,
				scenarioIndex,
				"Cada escenario debe tener al menos un pasajero pagante",
			)
		}
		if scenario.PayingPassengers != scenario.TotalPassengers-scenario.FreePassengers {
			return validationFailure(
				reasonInconsistentPayingCount,
				scenarioIndex,
				"La cantidad de pagantes del escenario es incoherente",
			)
		}
	}

	if err := lambdautil.ValidateStruct(request); err != nil {
		return err
	}
	return nil
}

func validationFailure(reason string, scenarioIndex int, message string) error {
	return apperr.Validation(message).WithDetails(map[string]any{
		"reason":        reason,
		"scenarioIndex": scenarioIndex,
	})
}
