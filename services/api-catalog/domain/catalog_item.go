package domain

import (
	"fmt"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// CatalogItem es la representación en DynamoDB de una opción de catálogo:
// pk = CATALOG, sk = <scope>#<order:04d>#<id>.
//
// El identificador y el orden de presentación no se guardan como atributos: se
// derivan de la clave con [ParseCatalogItemSK]. Guardarlos aparte sería un dato
// redundante que puede quedar inconsistente con la clave que define el orden en
// que DynamoDB entrega los ítems, y ese orden es el que la respuesta expone.
type CatalogItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	// Display es la etiqueta visible de la opción (Requirement 16.3).
	Display string `dynamodbav:"display"`

	// Active marca la vigencia de la opción. El handler omite de la respuesta
	// las opciones inactivas (Requirement 16.4), así que desactivar un destino
	// es un cambio de este atributo y no un borrado: el programa que ya lo
	// referencia sigue teniendo dónde resolver su etiqueta.
	Active bool `dynamodbav:"active"`

	// BudgetTemplateID selecciona el generador de documento del backend
	// (Requirement 16.5). Solo lo declaran los destinos; en planes y temporadas
	// queda vacío y su omitempty lo deja fuera del ítem.
	BudgetTemplateID string `dynamodbav:"budgetTemplateId,omitempty"`
}

// NewCatalogItem arma el ítem de una opción de catálogo a partir de los
// componentes de su clave.
//
// Exige que solo los destinos declaren plantilla de presupuesto: un plan con
// plantilla es un dato que nadie va a leer, y un destino sin ella incumple el
// Requirement 16.5 y terminaría produciendo un PDF fallido mucho después de la
// escritura que lo originó.
func NewCatalogItem(key CatalogItemKey, display string, active bool, budgetTemplateID string) (CatalogItem, error) {
	itemKey, err := key.Key()
	if err != nil {
		return CatalogItem{}, err
	}

	if display == "" {
		return CatalogItem{}, fmt.Errorf("la opción %q no declara etiqueta visible", itemKey.SK)
	}

	if key.Scope == ScopeDestination && budgetTemplateID == "" {
		return CatalogItem{}, fmt.Errorf("el destino %q no declara plantilla de presupuesto", key.ID)
	}

	if key.Scope != ScopeDestination && budgetTemplateID != "" {
		return CatalogItem{}, fmt.Errorf(
			"la opción %q del scope %q no puede declarar plantilla de presupuesto",
			key.ID, string(key.Scope),
		)
	}

	return CatalogItem{
		PK:               itemKey.PK,
		SK:               itemKey.SK,
		Display:          display,
		Active:           active,
		BudgetTemplateID: budgetTemplateID,
	}, nil
}

// Option devuelve el scope del ítem y la opción de catálogo de la respuesta,
// con el identificador y el orden derivados de la clave (Requirement 16.3).
//
// Devuelve el scope junto con la opción para que el handler decida en una sola
// llamada a qué colección de la respuesta pertenece.
func (i CatalogItem) Option() (Scope, program.CatalogOption, error) {
	key, err := ParseCatalogItemSK(i.SK)
	if err != nil {
		return "", program.CatalogOption{}, err
	}

	return key.Scope, program.CatalogOption{
		ID:      key.ID,
		Display: i.Display,
		Order:   key.Order,
	}, nil
}

// Destination devuelve la opción de destino del ítem.
//
// Falla si el ítem no pertenece a [ScopeDestination] o si no declara plantilla
// de presupuesto: el frontend recibe el identificador de plantilla como campo
// obligatorio del destino (Requirement 16.5), y entregarlo vacío trasladaría el
// dato incompleto de la tabla hasta el generador de PDF.
func (i CatalogItem) Destination() (program.DestinationOption, error) {
	scope, option, err := i.Option()
	if err != nil {
		return program.DestinationOption{}, err
	}

	if scope != ScopeDestination {
		return program.DestinationOption{}, fmt.Errorf(
			"%w: %q no es un destino", ErrInvalidScope, i.SK,
		)
	}

	if i.BudgetTemplateID == "" {
		return program.DestinationOption{}, fmt.Errorf(
			"el destino %q no declara plantilla de presupuesto", option.ID,
		)
	}

	return program.DestinationOption{
		CatalogOption:    option,
		BudgetTemplateID: i.BudgetTemplateID,
	}, nil
}

// MarginAttributes es la forma en DynamoDB del mapa `margin` del ítem de
// parámetros de política.
//
// Traduce program.MarginDefaults en vez de reutilizarlo porque attributevalue
// no lee las etiquetas `json` a menos que se le pase TagKey, y el tipo
// compartido no declara `dynamodbav`: sin esta traducción el mapa se guardaría
// con los nombres de campo de Go (`UsdIncreaseCLP`) en vez de los del diseño.
// Tener las etiquetas de persistencia acá deja libs/domain/program libre de
// DynamoDB y evita que la forma escrita dependa de que cada llamador recuerde
// pasarle una opción al encoder.
type MarginAttributes struct {
	UsdIncreaseCLP int64 `dynamodbav:"usdIncreaseCLP"`
	BrlIncreaseCLP int64 `dynamodbav:"brlIncreaseCLP"`
	UtilityRate    int   `dynamodbav:"utilityRate"`
	RechargeRate   int   `dynamodbav:"rechargeRate"`
	MinUtilityRate int   `dynamodbav:"minUtilityRate"`
}

// NewMarginAttributes traduce los valores por defecto de margen a su forma
// persistida.
func NewMarginAttributes(defaults program.MarginDefaults) MarginAttributes {
	return MarginAttributes{
		UsdIncreaseCLP: defaults.UsdIncreaseCLP,
		BrlIncreaseCLP: defaults.BrlIncreaseCLP,
		UtilityRate:    defaults.UtilityRate,
		RechargeRate:   defaults.RechargeRate,
		MinUtilityRate: defaults.MinUtilityRate,
	}
}

// Defaults traduce los atributos persistidos al tipo de la respuesta.
func (m MarginAttributes) Defaults() program.MarginDefaults {
	return program.MarginDefaults{
		UsdIncreaseCLP: m.UsdIncreaseCLP,
		BrlIncreaseCLP: m.BrlIncreaseCLP,
		UtilityRate:    m.UtilityRate,
		RechargeRate:   m.RechargeRate,
		MinUtilityRate: m.MinUtilityRate,
	}
}

// SettingsItem es la representación en DynamoDB de los parámetros de política
// de la empresa: pk = CATALOG, sk = SETTINGS.
//
// Comparte partición con las opciones de catálogo a propósito, y eso es lo que
// permite servir todo GET /catalogos con una sola consulta (Requirement 16.2).
//
// Margin es un puntero y ScenarioOffsets lleva omitempty por la misma razón que
// en program.CatalogSettings: la ausencia del campo (la empresa no declaró el
// dato) tiene que ser distinguible de un valor en cero. Si el ítem guardara
// ceros donde no hay dato, el formulario precargaría una utilidad de 0 y
// produciría el programa vendido al costo que la precarga viene a evitar
// (Requirements 16.8 y 16.9).
type SettingsItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	// DefaultPlanID identifica el plan que el formulario preselecciona al
	// terminar de cargar el catálogo (Requirement 2.9).
	DefaultPlanID string `dynamodbav:"defaultPlanId,omitempty"`

	// Margin son los valores por defecto de margen y el piso de utilidad de la
	// política de empresa (Requirement 16.6). Nil cuando no se declararon.
	Margin *MarginAttributes `dynamodbav:"margin,omitempty"`

	// ScenarioOffsets son los desplazamientos de escenario del presupuesto
	// (Requirement 16.7). Vacío cuando no se declararon.
	ScenarioOffsets []int `dynamodbav:"scenarioOffsets,omitempty"`
}

// NewSettingsItem arma el ítem de parámetros de política a partir de los
// parámetros de la respuesta. Es lo que usa la siembra de la tabla.
func NewSettingsItem(settings program.CatalogSettings) SettingsItem {
	item := SettingsItem{
		PK:              CatalogPK,
		SK:              SettingsSK,
		DefaultPlanID:   settings.DefaultPlanID,
		ScenarioOffsets: settings.ScenarioOffsets,
	}

	if settings.Margin != nil {
		margin := NewMarginAttributes(*settings.Margin)
		item.Margin = &margin
	}

	return item
}

// Settings traduce el ítem a los parámetros de la respuesta.
//
// Un Margin nil o unos ScenarioOffsets vacíos se propagan como ausentes, y el
// omitempty de program.CatalogSettings los deja fuera del JSON, que es
// exactamente lo que piden los Requirements 16.8 y 16.9.
func (i SettingsItem) Settings() program.CatalogSettings {
	settings := program.CatalogSettings{
		DefaultPlanID:   i.DefaultPlanID,
		ScenarioOffsets: i.ScenarioOffsets,
	}

	if i.Margin != nil {
		defaults := i.Margin.Defaults()
		settings.Margin = &defaults
	}

	return settings
}
