package domain

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// TestNewCatalogItem cubre la construcción del ítem de una opción, incluida la
// invariante de que solo los destinos declaran plantilla de presupuesto
// (Requirement 16.5).
func TestNewCatalogItem(t *testing.T) {
	tests := []struct {
		name             string
		key              CatalogItemKey
		display          string
		active           bool
		budgetTemplateID string
		want             CatalogItem
		wantErr          bool
	}{
		{
			name:    "plan activo",
			key:     CatalogItemKey{Scope: ScopePlan, Order: 1, ID: "basico"},
			display: "Básico",
			active:  true,
			want: CatalogItem{
				PK:      CatalogPK,
				SK:      "PLAN#0001#basico",
				Display: "Básico",
				Active:  true,
			},
		},
		{
			name:    "temporada inactiva",
			key:     CatalogItemKey{Scope: ScopeSeason, Order: 12, ID: "baja"},
			display: "Temporada baja",
			active:  false,
			want: CatalogItem{
				PK:      CatalogPK,
				SK:      "SEASON#0012#baja",
				Display: "Temporada baja",
				Active:  false,
			},
		},
		{
			name:             "destino con plantilla",
			key:              CatalogItemKey{Scope: ScopeDestination, Order: 1, ID: "BRF"},
			display:          "Brasil - Florianópolis",
			active:           true,
			budgetTemplateID: "BRF",
			want: CatalogItem{
				PK:               CatalogPK,
				SK:               "DESTINATION#0001#BRF",
				Display:          "Brasil - Florianópolis",
				Active:           true,
				BudgetTemplateID: "BRF",
			},
		},
		{
			name:    "destino sin plantilla",
			key:     CatalogItemKey{Scope: ScopeDestination, Order: 1, ID: "BRF"},
			display: "Brasil - Florianópolis",
			active:  true,
			wantErr: true,
		},
		{
			name:             "plan con plantilla",
			key:              CatalogItemKey{Scope: ScopePlan, Order: 1, ID: "basico"},
			display:          "Básico",
			active:           true,
			budgetTemplateID: "BRF",
			wantErr:          true,
		},
		{
			name:    "sin etiqueta visible",
			key:     CatalogItemKey{Scope: ScopePlan, Order: 1, ID: "basico"},
			display: "",
			active:  true,
			wantErr: true,
		},
		{
			name:    "clave inválida",
			key:     CatalogItemKey{Scope: ScopePlan, Order: OrderMax + 1, ID: "basico"},
			display: "Básico",
			active:  true,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewCatalogItem(tc.key, tc.display, tc.active, tc.budgetTemplateID)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewCatalogItem() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewCatalogItem() error = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("NewCatalogItem() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestCatalogItemOption confirma que el identificador y el orden de la respuesta
// salen de la clave y no de atributos aparte (Requirement 16.3).
func TestCatalogItemOption(t *testing.T) {
	tests := []struct {
		name      string
		item      CatalogItem
		wantScope Scope
		want      program.CatalogOption
		wantErr   bool
	}{
		{
			name:      "plan",
			item:      CatalogItem{PK: CatalogPK, SK: "PLAN#0002#basico", Display: "Básico", Active: true},
			wantScope: ScopePlan,
			want:      program.CatalogOption{ID: "basico", Display: "Básico", Order: 2},
		},
		{
			name:      "temporada con orden de dos dígitos",
			item:      CatalogItem{PK: CatalogPK, SK: "SEASON#0010#alta", Display: "Alta", Active: true},
			wantScope: ScopeSeason,
			want:      program.CatalogOption{ID: "alta", Display: "Alta", Order: 10},
		},
		{
			name: "destino: Option no expone la plantilla",
			item: CatalogItem{
				PK: CatalogPK, SK: "DESTINATION#0001#BRF",
				Display: "Florianópolis", Active: true, BudgetTemplateID: "BRF",
			},
			wantScope: ScopeDestination,
			want:      program.CatalogOption{ID: "BRF", Display: "Florianópolis", Order: 1},
		},
		{
			name:    "clave de parámetros de política",
			item:    CatalogItem{PK: CatalogPK, SK: SettingsSK},
			wantErr: true,
		},
		{
			name:    "clave mal formada",
			item:    CatalogItem{PK: CatalogPK, SK: "PLAN#2#basico"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope, got, err := tc.item.Option()

			if tc.wantErr {
				if err == nil {
					t.Fatalf("Option() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("Option() error = %v, want nil", err)
			}
			if scope != tc.wantScope {
				t.Errorf("Option() scope = %q, want %q", scope, tc.wantScope)
			}
			if got != tc.want {
				t.Errorf("Option() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestCatalogItemDestination cubre la conversión a opción de destino, que exige
// scope de destino y plantilla declarada (Requirement 16.5).
func TestCatalogItemDestination(t *testing.T) {
	tests := []struct {
		name      string
		item      CatalogItem
		want      program.DestinationOption
		wantErr   bool
		wantErrIs error
	}{
		{
			name: "destino completo",
			item: CatalogItem{
				PK: CatalogPK, SK: "DESTINATION#0003#CBU",
				Display: "Cataratas - Buzios", Active: true, BudgetTemplateID: "CBU",
			},
			want: program.DestinationOption{
				CatalogOption:    program.CatalogOption{ID: "CBU", Display: "Cataratas - Buzios", Order: 3},
				BudgetTemplateID: "CBU",
			},
		},
		{
			name: "plan: no es un destino",
			item: CatalogItem{
				PK: CatalogPK, SK: "PLAN#0001#basico", Display: "Básico", Active: true,
			},
			wantErr:   true,
			wantErrIs: ErrInvalidScope,
		},
		{
			name: "destino sin plantilla",
			item: CatalogItem{
				PK: CatalogPK, SK: "DESTINATION#0001#BRF", Display: "Florianópolis", Active: true,
			},
			wantErr: true,
		},
		{
			name:    "clave mal formada",
			item:    CatalogItem{PK: CatalogPK, SK: "DESTINATION#1#BRF", BudgetTemplateID: "BRF"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.item.Destination()

			if tc.wantErr {
				if err == nil {
					t.Fatalf("Destination() error = nil, want error")
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Errorf("Destination() error = %v, want %v", err, tc.wantErrIs)
				}
				return
			}

			if err != nil {
				t.Fatalf("Destination() error = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("Destination() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestCatalogItemAttributeNames fija los nombres con los que el ítem de una
// opción se guarda en DynamoDB. Son el contrato con el script de siembra y con
// cualquier consulta escrita a mano: cambiarlos deja los datos ya cargados
// inalcanzables, así que la ruptura tiene que aparecer acá.
func TestCatalogItemAttributeNames(t *testing.T) {
	tests := []struct {
		name  string
		item  CatalogItem
		wantK []string
	}{
		{
			name: "plan: budgetTemplateId se omite",
			item: CatalogItem{
				PK: CatalogPK, SK: "PLAN#0001#basico", Display: "Básico", Active: true,
			},
			wantK: []string{"active", "display", "pk", "sk"},
		},
		{
			name: "destino: budgetTemplateId está presente",
			item: CatalogItem{
				PK: CatalogPK, SK: "DESTINATION#0001#BRF",
				Display: "Florianópolis", Active: false, BudgetTemplateID: "BRF",
			},
			wantK: []string{"active", "budgetTemplateId", "display", "pk", "sk"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			av, err := attributevalue.MarshalMap(tc.item)
			if err != nil {
				t.Fatalf("MarshalMap() error = %v", err)
			}

			if got := attributeNames(av); !reflect.DeepEqual(got, tc.wantK) {
				t.Errorf("atributos = %v, want %v", got, tc.wantK)
			}

			var roundTrip CatalogItem
			if err := attributevalue.UnmarshalMap(av, &roundTrip); err != nil {
				t.Fatalf("UnmarshalMap() error = %v", err)
			}
			if roundTrip != tc.item {
				t.Errorf("round-trip = %+v, want %+v", roundTrip, tc.item)
			}
		})
	}
}

// TestSettingsItemAttributeNames fija la forma persistida de los parámetros de
// política, incluidos los nombres del mapa `margin`.
//
// Es la razón de existir de [MarginAttributes]: program.MarginDefaults solo
// declara etiquetas `json`, que attributevalue no lee, así que reutilizarlo
// directamente guardaría `UsdIncreaseCLP` en vez de `usdIncreaseCLP`.
func TestSettingsItemAttributeNames(t *testing.T) {
	item := NewSettingsItem(program.CatalogSettings{
		DefaultPlanID: "tour",
		Margin: &program.MarginDefaults{
			UsdIncreaseCLP: 50,
			BrlIncreaseCLP: 10,
			UtilityRate:    20,
			RechargeRate:   5,
			MinUtilityRate: 10,
		},
		ScenarioOffsets: []int{-10, -5, 0, 5},
	})

	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	wantTop := []string{"defaultPlanId", "margin", "pk", "scenarioOffsets", "sk"}
	if got := attributeNames(av); !reflect.DeepEqual(got, wantTop) {
		t.Fatalf("atributos = %v, want %v", got, wantTop)
	}

	margin, ok := av["margin"].(*types.AttributeValueMemberM)
	if !ok {
		t.Fatalf("margin = %T, want un mapa", av["margin"])
	}

	wantMargin := []string{"brlIncreaseCLP", "minUtilityRate", "rechargeRate", "usdIncreaseCLP", "utilityRate"}
	if got := attributeNames(margin.Value); !reflect.DeepEqual(got, wantMargin) {
		t.Errorf("atributos de margin = %v, want %v", got, wantMargin)
	}
}

// TestSettingsItemAbsentVsZero es la invariante de los Requirements 16.8 y 16.9:
// un ítem sin parámetros omite los atributos, y un ítem con parámetros en cero
// los conserva. Confundir los dos casos haría que el formulario precargara una
// utilidad de 0 y cotizara al costo.
func TestSettingsItemAbsentVsZero(t *testing.T) {
	t.Run("sin parámetros: margin y scenarioOffsets se omiten", func(t *testing.T) {
		item := NewSettingsItem(program.CatalogSettings{})

		if item.Margin != nil {
			t.Errorf("Margin = %+v, want nil", item.Margin)
		}

		av, err := attributevalue.MarshalMap(item)
		if err != nil {
			t.Fatalf("MarshalMap() error = %v", err)
		}
		if got, want := attributeNames(av), []string{"pk", "sk"}; !reflect.DeepEqual(got, want) {
			t.Errorf("atributos = %v, want %v", got, want)
		}

		settings := item.Settings()
		if settings.Margin != nil {
			t.Errorf("Settings().Margin = %+v, want nil", settings.Margin)
		}
		if settings.ScenarioOffsets != nil {
			t.Errorf("Settings().ScenarioOffsets = %v, want nil", settings.ScenarioOffsets)
		}
	})

	t.Run("parámetros en cero: se conservan", func(t *testing.T) {
		item := NewSettingsItem(program.CatalogSettings{
			Margin:          &program.MarginDefaults{},
			ScenarioOffsets: []int{0},
		})

		av, err := attributevalue.MarshalMap(item)
		if err != nil {
			t.Fatalf("MarshalMap() error = %v", err)
		}
		if _, ok := av["margin"]; !ok {
			t.Error("el atributo margin no está presente, want presente con los cinco campos en cero")
		}

		settings := item.Settings()
		if settings.Margin == nil {
			t.Fatal("Settings().Margin = nil, want un puntero no nil")
		}
		if settings.Margin.UtilityRate != 0 {
			t.Errorf("Settings().Margin.UtilityRate = %d, want 0", settings.Margin.UtilityRate)
		}
	})
}

// TestSettingsItemRoundTrip confirma que los parámetros sobreviven el viaje
// completo: tipo de la respuesta, ítem, DynamoDB, ítem y tipo de la respuesta.
func TestSettingsItemRoundTrip(t *testing.T) {
	original := program.CatalogSettings{
		DefaultPlanID: "tour",
		Margin: &program.MarginDefaults{
			UsdIncreaseCLP: 50,
			BrlIncreaseCLP: 10,
			UtilityRate:    20,
			RechargeRate:   5,
			MinUtilityRate: 12,
		},
		ScenarioOffsets: []int{-10, -5, 0, 5},
	}

	av, err := attributevalue.MarshalMap(NewSettingsItem(original))
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	var item SettingsItem
	if err := attributevalue.UnmarshalMap(av, &item); err != nil {
		t.Fatalf("UnmarshalMap() error = %v", err)
	}

	if item.PK != CatalogPK || item.SK != SettingsSK {
		t.Errorf("clave = {%q %q}, want {%q %q}", item.PK, item.SK, CatalogPK, SettingsSK)
	}

	got := item.Settings()
	if got.DefaultPlanID != original.DefaultPlanID {
		t.Errorf("Settings().DefaultPlanID = %q, want %q", got.DefaultPlanID, original.DefaultPlanID)
	}
	if got.Margin == nil {
		t.Fatal("Settings().Margin = nil, want un puntero no nil")
	}
	if *got.Margin != *original.Margin {
		t.Errorf("Settings().Margin = %+v, want %+v", *got.Margin, *original.Margin)
	}
	if !reflect.DeepEqual(got.ScenarioOffsets, original.ScenarioOffsets) {
		t.Errorf("Settings().ScenarioOffsets = %v, want %v", got.ScenarioOffsets, original.ScenarioOffsets)
	}
}

// attributeNames devuelve los nombres de atributo de un ítem, ordenados, para
// comparar la forma persistida sin depender del recorrido del mapa.
func attributeNames(av map[string]types.AttributeValue) []string {
	names := make([]string, 0, len(av))
	for name := range av {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}
