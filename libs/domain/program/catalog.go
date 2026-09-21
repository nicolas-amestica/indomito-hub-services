package program

// CatalogRef referencia una entidad de catálogo (plan, temporada o destino).
// BudgetTemplateID solo lo declaran los destinos; en plan y temporada queda vacío
// y se omite de la respuesta por su `omitempty`.
//
// Declara `dynamodbav` porque ProgramGeneral lo anida y el contenido de un
// favorito se guarda entero: ver la nota de encabezado de program.go. El
// `omitempty` se repite en las dos etiquetas para que el atributo esté ausente
// del ítem en los mismos casos en que está ausente del JSON.
//
// Es el único tipo de este archivo con etiquetas de persistencia. MarginDefaults
// y CatalogSettings no las llevan porque no se guardan con esta forma: la tabla
// `catalogos` los persiste a través de los tipos de
// services/api-catalog/domain, cuyo ítem tiene una forma distinta a la de la
// respuesta. La regla que separa los dos casos es esa —etiquetas acá cuando el
// tipo se guarda tal cual, tipo de persistencia local cuando la forma
// guardada difiere— y no una preferencia por paquete.
type CatalogRef struct {
	ID               string `dynamodbav:"id"                         json:"id"                         validate:"required"`
	Display          string `dynamodbav:"display"                    json:"display"                    validate:"required"`
	BudgetTemplateID string `dynamodbav:"budgetTemplateId,omitempty" json:"budgetTemplateId,omitempty"`
}

// CatalogOption es una opción de catálogo (plan o temporada) para un selector.
type CatalogOption struct {
	ID      string `json:"id"      validate:"required"`
	Display string `json:"display" validate:"required"`
	Order   int    `json:"order"`
}

// DestinationOption es una opción de destino. Además selecciona el generador
// de PDF del backend a través de BudgetTemplateID.
type DestinationOption struct {
	CatalogOption
	// BudgetTemplateID selecciona el generador de documento del backend.
	BudgetTemplateID string `json:"budgetTemplateId" validate:"required"`
}

// MarginDefaults son los valores por defecto de margen y el piso de política
// de la empresa, servidos por el catálogo.
type MarginDefaults struct {
	UsdIncreaseCLP int64 `json:"usdIncreaseCLP" validate:"min=0,max=200"`
	BrlIncreaseCLP int64 `json:"brlIncreaseCLP" validate:"min=0,max=40"`
	UtilityRate    int   `json:"utilityRate"    validate:"min=0,max=100"`
	RechargeRate   int   `json:"rechargeRate"   validate:"min=0,max=100"`
	MinUtilityRate int   `json:"minUtilityRate" validate:"min=0,max=100"`
}

// CatalogSettings acompaña a los catálogos con la política de empresa
// (Requirements 2.9, 16.6, 16.7 y 16.11). Los campos son opcionales para que
// una tabla aún no sembrada no fuerce valores ficticios en el formulario.
//
// Margin es un puntero y ScenarioOffsets usa `omitempty` para que la ausencia
// del campo (la empresa no declaró el dato) sea distinguible de un valor en
// cero (por ejemplo, un utilityRate de 0 que significa "vender al costo").
// Si el catálogo enviara ceros en vez de omitir el campo, el formulario
// precargaría una utilidad de 0 y produciría exactamente el programa vendido
// al costo que la precarga viene a evitar (Requirements 16.8 y 16.9).
type CatalogSettings struct {
	DefaultPlanID   string          `json:"defaultPlanId,omitempty"`
	Margin          *MarginDefaults `json:"margin,omitempty"`
	ScenarioOffsets []int           `json:"scenarioOffsets,omitempty"`
}
