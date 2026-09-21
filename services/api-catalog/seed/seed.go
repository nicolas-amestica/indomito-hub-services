// Package seed valida y escribe la configuración inicial de la tabla
// `catalogos`. La escritura usa claves deterministas y PutItem, por lo que
// aplicar dos veces el mismo manifiesto deja los mismos ítems persistidos.
package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/domain"
)

const maxScenarioOffsets = 4

// Writer es la operación mínima que la siembra necesita de DynamoDB.
type Writer interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// Option declara una opción de plan, temporada o destino en el manifiesto.
// El scope se obtiene de la colección que la contiene.
type Option struct {
	ID               string `json:"id"`
	Display          string `json:"display"`
	Order            int    `json:"order"`
	Active           bool   `json:"active"`
	BudgetTemplateID string `json:"budgetTemplateId,omitempty"`
}

// Settings declara la política comercial que se guarda con sk SETTINGS.
type Settings struct {
	DefaultPlanID   string                  `json:"defaultPlanId"`
	Margin          *program.MarginDefaults `json:"margin"`
	ScenarioOffsets []int                   `json:"scenarioOffsets"`
}

// Manifest contiene todos los datos que administra el script de siembra.
type Manifest struct {
	Plans        []Option `json:"plans"`
	Seasons      []Option `json:"seasons"`
	Destinations []Option `json:"destinations"`
	Settings     Settings `json:"settings"`
}

// LoadManifest decodifica un único objeto JSON y rechaza campos desconocidos.
func LoadManifest(reader io.Reader) (Manifest, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("no se pudo decodificar el manifiesto: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, fmt.Errorf("el manifiesto contiene más de un valor JSON")
		}
		return Manifest{}, fmt.Errorf("no se pudo leer el final del manifiesto: %w", err)
	}

	return manifest, nil
}

// ValidateManifest comprueba que el manifiesto pueda servir todos los campos
// obligatorios del endpoint antes de abrir una conexión con AWS.
func ValidateManifest(manifest Manifest) error {
	if err := validateOptions("plans", domain.ScopePlan, manifest.Plans); err != nil {
		return err
	}
	if err := validateOptions("seasons", domain.ScopeSeason, manifest.Seasons); err != nil {
		return err
	}
	if err := validateOptions("destinations", domain.ScopeDestination, manifest.Destinations); err != nil {
		return err
	}
	if err := validateDefaultPlan(manifest.Settings.DefaultPlanID, manifest.Plans); err != nil {
		return err
	}
	if manifest.Settings.Margin == nil {
		return fmt.Errorf("settings.margin es obligatorio para la siembra inicial")
	}
	if err := validateMargin(*manifest.Settings.Margin); err != nil {
		return err
	}
	if err := validateScenarioOffsets(manifest.Settings.ScenarioOffsets); err != nil {
		return err
	}

	return nil
}

// ItemCount devuelve la cantidad de ítems que escribirá el manifiesto.
func ItemCount(manifest Manifest) int {
	return len(manifest.Plans) + len(manifest.Seasons) + len(manifest.Destinations) + 1
}

// Apply valida el manifiesto y sobrescribe sus ítems mediante PutItem.
// No elimina datos ajenos al manifiesto ni el snapshot de tasas.
func Apply(ctx context.Context, writer Writer, tableName string, manifest Manifest) error {
	if strings.TrimSpace(tableName) == "" {
		return fmt.Errorf("el nombre de la tabla es obligatorio")
	}
	if err := ValidateManifest(manifest); err != nil {
		return err
	}

	items, err := marshalItems(manifest)
	if err != nil {
		return err
	}

	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("la siembra fue cancelada antes del ítem %d: %w", index+1, err)
		}

		if _, err := writer.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item:      item,
		}); err != nil {
			return fmt.Errorf("no se pudo escribir el ítem %d de %d: %w", index+1, len(items), err)
		}
	}

	return nil
}

func validateOptions(collection string, scope domain.Scope, options []Option) error {
	if len(options) == 0 {
		return fmt.Errorf("%s debe declarar al menos una opción", collection)
	}

	ids := make(map[string]struct{}, len(options))
	for index, option := range options {
		if strings.TrimSpace(option.ID) == "" {
			return fmt.Errorf("%s[%d].id es obligatorio", collection, index)
		}
		if strings.TrimSpace(option.Display) == "" {
			return fmt.Errorf("%s[%d].display es obligatorio", collection, index)
		}

		if _, exists := ids[option.ID]; exists {
			return fmt.Errorf("%s repite el id %q", collection, option.ID)
		}
		ids[option.ID] = struct{}{}

		if _, err := domain.NewCatalogItem(
			domain.CatalogItemKey{Scope: scope, Order: option.Order, ID: option.ID},
			option.Display,
			option.Active,
			option.BudgetTemplateID,
		); err != nil {
			return fmt.Errorf("%s[%d] es inválido: %w", collection, index, err)
		}
	}

	return nil
}

func validateMargin(margin program.MarginDefaults) error {
	checks := []struct {
		name  string
		value float64
		limit program.Limit
	}{
		{name: "usdIncreaseCLP", value: float64(margin.UsdIncreaseCLP), limit: program.FieldLimits.UsdIncreaseCLP},
		{name: "brlIncreaseCLP", value: float64(margin.BrlIncreaseCLP), limit: program.FieldLimits.BrlIncreaseCLP},
		{name: "utilityRate", value: float64(margin.UtilityRate), limit: program.FieldLimits.UtilityRate},
		{name: "rechargeRate", value: float64(margin.RechargeRate), limit: program.FieldLimits.RechargeRate},
		{name: "minUtilityRate", value: float64(margin.MinUtilityRate), limit: program.FieldLimits.UtilityRate},
	}

	for _, check := range checks {
		if check.value < check.limit.Min || check.value > check.limit.Max {
			return fmt.Errorf(
				"settings.margin.%s debe estar entre %.0f y %.0f",
				check.name,
				check.limit.Min,
				check.limit.Max,
			)
		}
	}

	return nil
}

func validateDefaultPlan(defaultPlanID string, plans []Option) error {
	if strings.TrimSpace(defaultPlanID) == "" {
		return fmt.Errorf("settings.defaultPlanId es obligatorio para la siembra inicial")
	}

	for _, plan := range plans {
		if plan.ID == defaultPlanID {
			if !plan.Active {
				return fmt.Errorf("settings.defaultPlanId referencia el plan inactivo %q", defaultPlanID)
			}
			return nil
		}
	}

	return fmt.Errorf("settings.defaultPlanId referencia el plan desconocido %q", defaultPlanID)
}

func validateScenarioOffsets(offsets []int) error {
	if len(offsets) == 0 {
		return fmt.Errorf("settings.scenarioOffsets debe declarar al menos un desplazamiento")
	}
	if len(offsets) > maxScenarioOffsets {
		return fmt.Errorf("settings.scenarioOffsets admite como máximo %d desplazamientos", maxScenarioOffsets)
	}

	return nil
}

func marshalItems(manifest Manifest) ([]map[string]types.AttributeValue, error) {
	items := make([]map[string]types.AttributeValue, 0, ItemCount(manifest))

	collections := []struct {
		scope   domain.Scope
		options []Option
	}{
		{scope: domain.ScopePlan, options: manifest.Plans},
		{scope: domain.ScopeSeason, options: manifest.Seasons},
		{scope: domain.ScopeDestination, options: manifest.Destinations},
	}

	for _, collection := range collections {
		for _, option := range collection.options {
			catalogItem, err := domain.NewCatalogItem(
				domain.CatalogItemKey{Scope: collection.scope, Order: option.Order, ID: option.ID},
				option.Display,
				option.Active,
				option.BudgetTemplateID,
			)
			if err != nil {
				return nil, fmt.Errorf("no se pudo construir la opción %q: %w", option.ID, err)
			}

			item, err := attributevalue.MarshalMap(catalogItem)
			if err != nil {
				return nil, fmt.Errorf("no se pudo serializar la opción %q: %w", option.ID, err)
			}
			items = append(items, item)
		}
	}

	settings := program.CatalogSettings{
		DefaultPlanID:   manifest.Settings.DefaultPlanID,
		Margin:          manifest.Settings.Margin,
		ScenarioOffsets: manifest.Settings.ScenarioOffsets,
	}
	item, err := attributevalue.MarshalMap(domain.NewSettingsItem(settings))
	if err != nil {
		return nil, fmt.Errorf("no se pudo serializar settings: %w", err)
	}

	return append(items, item), nil
}
