package program

import (
	"encoding/json"
	"testing"
)

// TestCatalogSettingsJSON confirma que CatalogSettings distingue un valor en
// cero de un campo ausente al serializar (Requirements 16.8, 16.9): el
// omitempty sobre Margin y ScenarioOffsets debe omitir el campo del JSON
// cuando el catálogo no lo declaró, y debe conservarlo cuando el valor es un
// cero legítimo (por ejemplo, un UtilityRate de 0 que significa "vender al
// costo").
func TestCatalogSettingsJSON(t *testing.T) {
	tests := []struct {
		name     string
		settings CatalogSettings
		wantJSON string
	}{
		{
			name:     "sin margin ni scenarioOffsets: ambos campos se omiten",
			settings: CatalogSettings{},
			wantJSON: `{}`,
		},
		{
			name: "defaultPlanId declarado se conserva",
			settings: CatalogSettings{
				DefaultPlanID: "tour",
			},
			wantJSON: `{"defaultPlanId":"tour"}`,
		},
		{
			name: "margin con utilityRate en cero se conserva, no se omite",
			settings: CatalogSettings{
				Margin: &MarginDefaults{
					UsdIncreaseCLP: 0,
					BrlIncreaseCLP: 0,
					UtilityRate:    0,
					RechargeRate:   0,
					MinUtilityRate: 0,
				},
			},
			wantJSON: `{"margin":{"usdIncreaseCLP":0,"brlIncreaseCLP":0,"utilityRate":0,"rechargeRate":0,"minUtilityRate":0}}`,
		},
		{
			name: "scenarioOffsets declarados se conservan",
			settings: CatalogSettings{
				ScenarioOffsets: []int{-10, -5, 0, 5},
			},
			wantJSON: `{"scenarioOffsets":[-10,-5,0,5]}`,
		},
		{
			name: "ambos campos declarados se conservan juntos",
			settings: CatalogSettings{
				DefaultPlanID: "tour",
				Margin: &MarginDefaults{
					UsdIncreaseCLP: 50,
					BrlIncreaseCLP: 10,
					UtilityRate:    20,
					RechargeRate:   5,
					MinUtilityRate: 10,
				},
				ScenarioOffsets: []int{0},
			},
			wantJSON: `{"defaultPlanId":"tour","margin":{"usdIncreaseCLP":50,"brlIncreaseCLP":10,"utilityRate":20,"rechargeRate":5,"minUtilityRate":10},"scenarioOffsets":[0]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.settings)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if string(got) != tc.wantJSON {
				t.Errorf("json.Marshal() = %s, want %s", got, tc.wantJSON)
			}

			// Round-trip: deserializar y volver a serializar debe producir el
			// mismo JSON, confirmando que Margin=nil y ScenarioOffsets=nil no
			// se confunden con valores en cero al leer de vuelta.
			var roundTrip CatalogSettings
			if err := json.Unmarshal(got, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			gotAgain, err := json.Marshal(roundTrip)
			if err != nil {
				t.Fatalf("json.Marshal() en el round-trip error = %v", err)
			}
			if string(gotAgain) != tc.wantJSON {
				t.Errorf("round-trip = %s, want %s", gotAgain, tc.wantJSON)
			}
		})
	}
}

// TestCatalogSettingsAbsentVsZero confirma explícitamente que un JSON sin la
// clave "margin" deserializa a un puntero nil, distinguible de un JSON con
// "margin" en cero.
func TestCatalogSettingsAbsentVsZero(t *testing.T) {
	var absent CatalogSettings
	if err := json.Unmarshal([]byte(`{}`), &absent); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if absent.Margin != nil {
		t.Errorf("Margin = %+v, want nil cuando el campo está ausente", absent.Margin)
	}

	var zero CatalogSettings
	if err := json.Unmarshal([]byte(`{"margin":{"usdIncreaseCLP":0,"brlIncreaseCLP":0,"utilityRate":0,"rechargeRate":0,"minUtilityRate":0}}`), &zero); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if zero.Margin == nil {
		t.Fatal("Margin = nil, want un puntero no nil cuando el campo está presente en cero")
	}
	if zero.Margin.UtilityRate != 0 {
		t.Errorf("Margin.UtilityRate = %d, want 0", zero.Margin.UtilityRate)
	}
}
