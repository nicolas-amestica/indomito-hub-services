package generarpresupuestov1

import (
	"errors"
	"testing"
	"testing/quick"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1/templates"
)

func validBudgetRequest() domain.BudgetRequest {
	return domain.BudgetRequest{
		ProgramName: "Gira Brasil 2027",
		Destination: program.CatalogRef{
			ID:               "BRX",
			Display:          "Brasil",
			BudgetTemplateID: templates.BrochureDefaultID,
		},
		DepartureCity: "Santiago",
		TotalDays:     8,
		TotalNights:   7,
		ServiceNames:  []string{"Hotel", "Traslados"},
		Scenarios: []domain.BudgetScenario{
			budgetScenario(30, 2, 28, 1_250_000),
			budgetScenario(35, 2, 33, 1_150_000),
		},
	}
}

func budgetScenario(total, free, paying int, price int64) domain.BudgetScenario {
	return domain.BudgetScenario{
		ScenarioShape: program.ScenarioShape{
			TotalPassengers:  total,
			FreePassengers:   free,
			PayingPassengers: paying,
		},
		PricePerPassengerCLP: price,
	}
}

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name              string
		mutate            func(*domain.BudgetRequest)
		wantCode          string
		wantReason        string
		wantScenarioIndex int
	}{
		{
			name:              "cuerpo posible",
			mutate:            func(*domain.BudgetRequest) {},
			wantScenarioIndex: globalValidationScenarioIndex,
		},
		{
			name: "escenarios vacíos",
			mutate: func(request *domain.BudgetRequest) {
				request.Scenarios = []domain.BudgetScenario{}
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonScenarioCount,
			wantScenarioIndex: globalValidationScenarioIndex,
		},
		{
			name: "más de cuatro escenarios",
			mutate: func(request *domain.BudgetRequest) {
				request.Scenarios = append(
					request.Scenarios,
					budgetScenario(40, 3, 37, 1_000_000),
					budgetScenario(45, 3, 42, 950_000),
					budgetScenario(50, 4, 46, 900_000),
				)
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonScenarioCount,
			wantScenarioIndex: globalValidationScenarioIndex,
		},
		{
			name: "plantilla desconocida",
			mutate: func(request *domain.BudgetRequest) {
				request.Destination.BudgetTemplateID = "no-registrada"
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonTemplateNotRegistered,
			wantScenarioIndex: globalValidationScenarioIndex,
		},
		{
			name: "precio no positivo",
			mutate: func(request *domain.BudgetRequest) {
				request.Scenarios[1].PricePerPassengerCLP = 0
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonPriceNotPositive,
			wantScenarioIndex: 2,
		},
		{
			name: "sin pagantes declarados",
			mutate: func(request *domain.BudgetRequest) {
				request.Scenarios[0].PayingPassengers = 0
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonNoPayingPassengers,
			wantScenarioIndex: 1,
		},
		{
			name: "liberados alcanzan al total",
			mutate: func(request *domain.BudgetRequest) {
				request.Scenarios[0] = budgetScenario(30, 30, 1, 1_250_000)
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonNoPayingPassengers,
			wantScenarioIndex: 1,
		},
		{
			name: "pagantes no equivalen a total menos liberados",
			mutate: func(request *domain.BudgetRequest) {
				request.Scenarios[0].PayingPassengers = 27
			},
			wantCode:          apperr.CodeValidationError,
			wantReason:        reasonInconsistentPayingCount,
			wantScenarioIndex: 1,
		},
		{
			name: "campo obligatorio ausente conserva su código",
			mutate: func(request *domain.BudgetRequest) {
				request.ProgramName = ""
			},
			wantCode:          apperr.CodeRequiredFieldMissing,
			wantScenarioIndex: globalValidationScenarioIndex,
		},
	}

	registry := templates.DefaultRegistry()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validBudgetRequest()
			test.mutate(&request)

			err := ValidateRequest(request, registry)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("ValidateRequest() error = %v, want nil", err)
				}
				return
			}

			appErr := requireAppError(t, err)
			if appErr.Code() != test.wantCode {
				t.Errorf("Code() = %q, want %q", appErr.Code(), test.wantCode)
			}
			if test.wantReason == "" {
				return
			}
			if got := appErr.Details()["reason"]; got != test.wantReason {
				t.Errorf("reason = %v, want %q", got, test.wantReason)
			}
			if got := appErr.Details()["scenarioIndex"]; got != test.wantScenarioIndex {
				t.Errorf("scenarioIndex = %v, want %d", got, test.wantScenarioIndex)
			}
		})
	}
}

// Feature: program-form, Property 37: El presupuesto rechaza todo cuerpo cuya forma sea imposible.
func TestBudgetRejectsImpossibleShapes(t *testing.T) {
	registry := templates.DefaultRegistry()
	property := func(
		countSeed uint8,
		priceSeed uint64,
		totalSeed uint8,
		freeSeed uint8,
		invalidPrice bool,
		noPayingPassengers bool,
		inconsistentPayingPassengers bool,
		registeredTemplate bool,
	) bool {
		scenarioCount := int(countSeed % 6)
		totalPassengers := int(totalSeed%100) + 1
		freePassengers := int(freeSeed) % totalPassengers
		payingPassengers := totalPassengers - freePassengers
		if noPayingPassengers {
			payingPassengers = 0
		} else if inconsistentPayingPassengers {
			payingPassengers++
		}

		price := int64(priceSeed%9_000_000) + 1
		if invalidPrice {
			// Incluye cero cuando el seed es múltiplo del módulo y negativos
			// en el resto, cubriendo las dos mitades de "menor o igual a 0".
			price = -int64(priceSeed % 9_000_000)
		}

		request := validBudgetRequest()
		request.Scenarios = make([]domain.BudgetScenario, scenarioCount)
		for index := range request.Scenarios {
			request.Scenarios[index] = budgetScenario(
				totalPassengers,
				freePassengers,
				payingPassengers,
				price,
			)
		}
		if !registeredTemplate {
			request.Destination.BudgetTemplateID = "no-registrada"
		}

		impossible := scenarioCount < domain.MinScenarios ||
			scenarioCount > domain.MaxScenarios ||
			invalidPrice ||
			noPayingPassengers ||
			inconsistentPayingPassengers ||
			!registeredTemplate

		err := ValidateRequest(request, registry)
		if !impossible {
			return err == nil
		}
		if err == nil {
			return false
		}
		var appErr *apperr.AppError
		return errors.As(err, &appErr) && appErr.Code() == apperr.CodeValidationError
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

func requireAppError(t *testing.T, err error) *apperr.AppError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *apperr.AppError")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %T, want *apperr.AppError", err)
	}
	return appErr
}
