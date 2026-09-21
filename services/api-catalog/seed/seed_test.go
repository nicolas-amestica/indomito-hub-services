package seed

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

type memoryWriter struct {
	items     map[string]map[string]types.AttributeValue
	putInputs []*dynamodb.PutItemInput
	failAt    int
}

func (w *memoryWriter) PutItem(
	_ context.Context,
	input *dynamodb.PutItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	w.putInputs = append(w.putInputs, input)
	if w.failAt > 0 && len(w.putInputs) == w.failAt {
		return nil, errors.New("fallo de prueba")
	}

	pk := input.Item["pk"].(*types.AttributeValueMemberS).Value
	sk := input.Item["sk"].(*types.AttributeValueMemberS).Value
	if w.items == nil {
		w.items = make(map[string]map[string]types.AttributeValue)
	}
	w.items[pk+"|"+sk] = input.Item
	return &dynamodb.PutItemOutput{}, nil
}

func validManifest() Manifest {
	return Manifest{
		Plans: []Option{
			{ID: "tour", Display: "Gira de estudio", Order: 1, Active: true},
		},
		Seasons: []Option{
			{ID: "2027", Display: "2027", Order: 1, Active: true},
		},
		Destinations: []Option{
			{ID: "BRF", Display: "Bariloche Full", Order: 1, Active: true, BudgetTemplateID: "brochure-brf"},
		},
		Settings: Settings{
			DefaultPlanID: "tour",
			Margin: &program.MarginDefaults{
				UsdIncreaseCLP: 50,
				BrlIncreaseCLP: 10,
				UtilityRate:    20,
				RechargeRate:   5,
				MinUtilityRate: 10,
			},
			ScenarioOffsets: []int{-10, -5, 0, 5},
		},
	}
}

func TestLoadManifest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "manifiesto completo",
			input: `{
				"plans":[{"id":"tour","display":"Gira","order":1,"active":true}],
				"seasons":[{"id":"2027","display":"2027","order":1,"active":true}],
				"destinations":[{"id":"BRF","display":"Bariloche","order":1,"active":true,"budgetTemplateId":"brochure-brf"}],
				"settings":{"defaultPlanId":"tour","margin":{"usdIncreaseCLP":50,"brlIncreaseCLP":10,"utilityRate":20,"rechargeRate":5,"minUtilityRate":10},"scenarioOffsets":[-10,-5,0,5]}
			}`,
		},
		{name: "campo desconocido", input: `{"unknown":true}`, wantErr: true},
		{name: "dos objetos", input: `{ } { }`, wantErr: true},
		{name: "json inválido", input: `{`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadManifest(strings.NewReader(test.input))
			if (err != nil) != test.wantErr {
				t.Fatalf("LoadManifest() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestConfiguredManifest(t *testing.T) {
	file, err := os.Open("../configs/catalog-seed.json")
	if err != nil {
		t.Fatalf("no se pudo abrir el manifiesto versionado: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("no se pudo cerrar el manifiesto versionado: %v", err)
		}
	}()

	manifest, err := LoadManifest(file)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("ValidateManifest() error = %v", err)
	}

	if got, want := ItemCount(manifest), 26; got != want {
		t.Errorf("ItemCount() = %d, want %d", got, want)
	}
	if got, want := manifest.Settings.DefaultPlanID, "01M2ET1GWJQ9YHHCJP7WQR9RNH"; got != want {
		t.Errorf("DefaultPlanID = %q, want %q", got, want)
	}
	if got, want := manifest.Seasons[0].ID, "2025"; got != want {
		t.Errorf("primera temporada = %q, want %q", got, want)
	}
	if got, want := manifest.Seasons[len(manifest.Seasons)-1].ID, "2040"; got != want {
		t.Errorf("última temporada = %q, want %q", got, want)
	}

	foundBRX := false
	for _, destination := range manifest.Destinations {
		if destination.BudgetTemplateID != "brochure-default" {
			t.Errorf("destino %q usa template %q", destination.ID, destination.BudgetTemplateID)
		}
		foundBRX = foundBRX || destination.ID == "BRX"
	}
	if !foundBRX {
		t.Error("el manifiesto no declara BRX para Bariloche Express")
	}
}

func TestValidateManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "plans vacío", mutate: func(m *Manifest) { m.Plans = nil }},
		{name: "temporadas vacío", mutate: func(m *Manifest) { m.Seasons = nil }},
		{name: "destinos vacío", mutate: func(m *Manifest) { m.Destinations = nil }},
		{name: "plan por defecto ausente", mutate: func(m *Manifest) { m.Settings.DefaultPlanID = "" }},
		{name: "plan por defecto desconocido", mutate: func(m *Manifest) { m.Settings.DefaultPlanID = "otro" }},
		{name: "plan por defecto inactivo", mutate: func(m *Manifest) { m.Plans[0].Active = false }},
		{name: "margin ausente", mutate: func(m *Manifest) { m.Settings.Margin = nil }},
		{name: "incremento fuera de rango", mutate: func(m *Manifest) { m.Settings.Margin.UsdIncreaseCLP = 201 }},
		{name: "offsets vacíos", mutate: func(m *Manifest) { m.Settings.ScenarioOffsets = nil }},
		{name: "más de cuatro offsets", mutate: func(m *Manifest) { m.Settings.ScenarioOffsets = []int{-10, -5, 0, 5, 10} }},
		{name: "id repetido", mutate: func(m *Manifest) { m.Plans = append(m.Plans, Option{ID: "tour", Display: "Otro", Order: 2}) }},
		{name: "destino sin plantilla", mutate: func(m *Manifest) { m.Destinations[0].BudgetTemplateID = "" }},
		{name: "plan con plantilla", mutate: func(m *Manifest) { m.Plans[0].BudgetTemplateID = "brochure-brf" }},
	}

	if err := ValidateManifest(validManifest()); err != nil {
		t.Fatalf("ValidateManifest() error = %v, want nil", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest)
			if err := ValidateManifest(manifest); err == nil {
				t.Fatal("ValidateManifest() error = nil, want error")
			}
		})
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	manifest := validManifest()
	writer := &memoryWriter{}

	if err := Apply(context.Background(), writer, "catalogos", manifest); err != nil {
		t.Fatalf("primera Apply() error = %v", err)
	}
	firstState := make(map[string]map[string]types.AttributeValue, len(writer.items))
	for key, item := range writer.items {
		firstState[key] = item
	}

	if err := Apply(context.Background(), writer, "catalogos", manifest); err != nil {
		t.Fatalf("segunda Apply() error = %v", err)
	}

	if !reflect.DeepEqual(writer.items, firstState) {
		t.Errorf("la segunda siembra cambió el estado: got %v, want %v", writer.items, firstState)
	}
	wantPuts := ItemCount(manifest) * 2
	if len(writer.putInputs) != wantPuts {
		t.Errorf("PutItem se llamó %d veces, se esperaban %d", len(writer.putInputs), wantPuts)
	}
	if _, exists := writer.items["CATALOG|SETTINGS"]; !exists {
		t.Error("la siembra no escribió el ítem SETTINGS")
	}
}

func TestApplyStopsOnError(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		writer  *memoryWriter
		wantPut int
	}{
		{
			name:    "DynamoDB falla",
			ctx:     context.Background(),
			writer:  &memoryWriter{failAt: 2},
			wantPut: 2,
		},
		{
			name: "contexto cancelado",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			writer:  &memoryWriter{},
			wantPut: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Apply(test.ctx, test.writer, "catalogos", validManifest())
			if err == nil {
				t.Fatal("Apply() error = nil, want error")
			}
			if len(test.writer.putInputs) != test.wantPut {
				t.Errorf("PutItem se llamó %d veces, se esperaban %d", len(test.writer.putInputs), test.wantPut)
			}
		})
	}
}
