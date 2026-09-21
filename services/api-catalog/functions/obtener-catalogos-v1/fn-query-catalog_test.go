package catalogs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

// tableName es el nombre de tabla que los tests le pasan al handler. No existe
// en ninguna cuenta: el cliente está sustituido y nada sale de este proceso.
const tableName = "catalogos-test"

// fakeDDB es el doble del cliente de DynamoDB. Devuelve las páginas que se le
// configuran y registra los Query que recibe, que es lo que permite comprobar
// que la respuesta se resuelve con una sola consulta (Requirement 16.2).
//
// Las demás operaciones de awsddb.Client hacen t.Fatal: este endpoint solo lee,
// y una llamada a PutItem o DeleteItem desde acá sería un defecto, no un caso a
// tolerar. Scan no aparece porque la interfaz no lo declara.
type fakeDDB struct {
	t      *testing.T
	pages  []*dynamodb.QueryOutput
	err    error
	inputs []*dynamodb.QueryInput
}

var _ awsddb.Client = (*fakeDDB)(nil)

func (f *fakeDDB) Query(
	_ context.Context,
	params *dynamodb.QueryInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	f.inputs = append(f.inputs, params)

	if f.err != nil {
		return nil, f.err
	}

	index := len(f.inputs) - 1
	if index >= len(f.pages) {
		f.t.Fatalf("el handler pidio la pagina %d y solo hay %d configuradas", index+1, len(f.pages))
	}

	return f.pages[index], nil
}

func (f *fakeDDB) GetItem(
	context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	f.t.Fatal("el endpoint de catalogos no debe usar GetItem")
	return nil, nil
}

func (f *fakeDDB) PutItem(
	context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	f.t.Fatal("el endpoint de catalogos es de solo lectura")
	return nil, nil
}

func (f *fakeDDB) UpdateItem(
	context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	f.t.Fatal("el endpoint de catalogos es de solo lectura")
	return nil, nil
}

func (f *fakeDDB) DeleteItem(
	context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.t.Fatal("el endpoint de catalogos es de solo lectura")
	return nil, nil
}

// singlePage arma un cliente que responde una sola pagina con los items dados.
func singlePage(t *testing.T, items ...map[string]ddbtypes.AttributeValue) *fakeDDB {
	t.Helper()
	return &fakeDDB{
		t:     t,
		pages: []*dynamodb.QueryOutput{{Items: items}},
	}
}

// marshalItem traduce un item de dominio a sus atributos de DynamoDB, tal como
// lo haria la tabla al devolverlo.
func marshalItem(t *testing.T, item any) map[string]ddbtypes.AttributeValue {
	t.Helper()

	attributes, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("no se pudo serializar el item %#v: %v", item, err)
	}

	return attributes
}

// optionItem arma el item de una opcion de catalogo con la clave bien formada.
func optionItem(
	t *testing.T,
	scope domain.Scope,
	order int,
	id, display string,
	active bool,
	budgetTemplateID string,
) map[string]ddbtypes.AttributeValue {
	t.Helper()

	sortKey, err := domain.CatalogItemKey{Scope: scope, Order: order, ID: id}.SK()
	if err != nil {
		t.Fatalf("no se pudo construir la sk de %s/%s: %v", scope, id, err)
	}

	return rawOptionItem(t, sortKey, display, active, budgetTemplateID)
}

// rawOptionItem arma el item de una opcion con la sk que se le indique, sin
// validarla. Es lo que permite sembrar claves mal formadas y scopes
// desconocidos, que la tabla admite y el handler tiene que sobrevivir.
func rawOptionItem(
	t *testing.T,
	sortKey, display string,
	active bool,
	budgetTemplateID string,
) map[string]ddbtypes.AttributeValue {
	t.Helper()

	return marshalItem(t, domain.CatalogItem{
		PK:               domain.CatalogPK,
		SK:               sortKey,
		Display:          display,
		Active:           active,
		BudgetTemplateID: budgetTemplateID,
	})
}

// settingsItem arma el item de parametros de politica.
func settingsItem(t *testing.T, settings program.CatalogSettings) map[string]ddbtypes.AttributeValue {
	t.Helper()
	return marshalItem(t, domain.NewSettingsItem(settings))
}

// fullMargin son unos valores de margen completos, para el caso en que la
// empresa si declara su politica.
func fullMargin() program.MarginDefaults {
	return program.MarginDefaults{
		UsdIncreaseCLP: 20,
		BrlIncreaseCLP: 5,
		UtilityRate:    18,
		RechargeRate:   3,
		MinUtilityRate: 12,
	}
}

// optionIDs extrae los identificadores de una lista de opciones, en su orden.
func optionIDs(options []program.CatalogOption) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	return ids
}

// destinationIDs extrae los identificadores de una lista de destinos.
func destinationIDs(destinations []program.DestinationOption) []string {
	ids := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		ids = append(ids, destination.ID)
	}
	return ids
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestQueryCatalogResolvesEverythingWithASingleQuery comprueba el Requirement
// 16.2 y el reparto de los items por su sk: una sola consulta sobre pk = CATALOG
// devuelve los tres catalogos y los parametros de politica.
func TestQueryCatalogResolvesEverythingWithASingleQuery(t *testing.T) {
	ddb := singlePage(t,
		// En el orden lexicografico de sk en que DynamoDB los entrega.
		optionItem(t, domain.ScopeDestination, 1, "brasil", "Brasil", true, "BRF"),
		optionItem(t, domain.ScopeDestination, 2, "cuba", "Cuba", true, "CBU"),
		optionItem(t, domain.ScopePlan, 1, "full", "Full", true, ""),
		optionItem(t, domain.ScopeSeason, 1, "verano", "Verano", true, ""),
		settingsItem(t, program.CatalogSettings{
			DefaultPlanID:   "full",
			Margin:          ptr(fullMargin()),
			ScenarioOffsets: []int{-10, -5, 0, 5},
		}),
	)

	response, err := QueryCatalog(context.Background(), ddb, tableName, zap.NewNop())
	if err != nil {
		t.Fatalf("QueryCatalog devolvio error: %v", err)
	}

	if len(ddb.inputs) != 1 {
		t.Errorf("la respuesta se resolvio con %d consultas y el Requirement 16.2 exige una", len(ddb.inputs))
	}

	input := ddb.inputs[0]
	if input.TableName == nil || *input.TableName != tableName {
		t.Errorf("la consulta apunta a %v y no a %q", input.TableName, tableName)
	}

	partition, ok := input.ExpressionAttributeValues[":pk"].(*ddbtypes.AttributeValueMemberS)
	if !ok || partition.Value != domain.CatalogPK {
		t.Errorf("la consulta no restringe la particion a %q: %#v", domain.CatalogPK, input.ExpressionAttributeValues)
	}

	if input.ExclusiveStartKey != nil {
		t.Errorf("la primera consulta no debe llevar ExclusiveStartKey: %#v", input.ExclusiveStartKey)
	}

	if got := optionIDs(response.Plans); !equalStrings(got, []string{"full"}) {
		t.Errorf("planes = %v", got)
	}
	if got := optionIDs(response.Seasons); !equalStrings(got, []string{"verano"}) {
		t.Errorf("temporadas = %v", got)
	}
	if got := destinationIDs(response.Destinations); !equalStrings(got, []string{"brasil", "cuba"}) {
		t.Errorf("destinos = %v", got)
	}

	if response.Destinations[0].BudgetTemplateID != "BRF" {
		t.Errorf("el destino no expone su plantilla de presupuesto: %q", response.Destinations[0].BudgetTemplateID)
	}

	if response.Settings.Margin == nil || *response.Settings.Margin != fullMargin() {
		t.Errorf("settings.margin = %#v", response.Settings.Margin)
	}
	if response.Settings.DefaultPlanID != "full" {
		t.Errorf("settings.defaultPlanId = %q", response.Settings.DefaultPlanID)
	}
	if len(response.Settings.ScenarioOffsets) != 4 {
		t.Errorf("settings.scenarioOffsets = %v", response.Settings.ScenarioOffsets)
	}

	if response.OptionCount() != 4 {
		t.Errorf("OptionCount = %d y hay 4 opciones vigentes", response.OptionCount())
	}
}

// TestQueryCatalogPreservesTableOrder comprueba que el orden de presentacion sea
// el que entrega DynamoDB y no uno recalculado en Go: el relleno de ceros de la
// sk existe justamente para que el orden lexicografico sea el numerico.
func TestQueryCatalogPreservesTableOrder(t *testing.T) {
	ddb := singlePage(t,
		optionItem(t, domain.ScopePlan, 1, "primero", "Primero", true, ""),
		optionItem(t, domain.ScopePlan, 2, "segundo", "Segundo", true, ""),
		optionItem(t, domain.ScopePlan, 10, "decimo", "Decimo", true, ""),
	)

	response, err := QueryCatalog(context.Background(), ddb, tableName, zap.NewNop())
	if err != nil {
		t.Fatalf("QueryCatalog devolvio error: %v", err)
	}

	if got := optionIDs(response.Plans); !equalStrings(got, []string{"primero", "segundo", "decimo"}) {
		t.Errorf("planes = %v, se esperaba el orden de la tabla", got)
	}

	if response.Plans[2].Order != 10 {
		t.Errorf("el orden de presentacion no viene de la clave: %d", response.Plans[2].Order)
	}
}

// TestProperty38CatalogsNeverExposeInactiveOptions recorre cada scope y las
// combinaciones representativas de actividad mediante una tabla, sustituyendo
// por completo el cliente de DynamoDB.
//
// Feature: program-form, Property 38: Los catálogos nunca exponen opciones inactivas.
func TestProperty38CatalogsNeverExposeInactiveOptions(t *testing.T) {
	scopes := []struct {
		name  string
		scope domain.Scope
	}{
		{name: "planes", scope: domain.ScopePlan},
		{name: "temporadas", scope: domain.ScopeSeason},
		{name: "destinos", scope: domain.ScopeDestination},
	}
	activityCases := []struct {
		name   string
		active []bool
	}{
		{name: "todas activas", active: []bool{true, true, true}},
		{name: "todas inactivas", active: []bool{false, false, false}},
		{name: "solo primera activa", active: []bool{true, false, false}},
		{name: "actividad intercalada", active: []bool{false, true, false}},
		{name: "solo ultima inactiva", active: []bool{true, true, false}},
	}
	ids := []string{"primera", "segunda", "tercera"}

	for _, scopeCase := range scopes {
		t.Run(scopeCase.name, func(t *testing.T) {
			for _, activityCase := range activityCases {
				t.Run(activityCase.name, func(t *testing.T) {
					items := make([]map[string]ddbtypes.AttributeValue, 0, len(ids))
					want := make([]string, 0, len(ids))

					for index, id := range ids {
						budgetTemplateID := ""
						if scopeCase.scope == domain.ScopeDestination {
							budgetTemplateID = "brochure-default"
						}
						items = append(items, optionItem(
							t,
							scopeCase.scope,
							index+1,
							id,
							strings.ToUpper(id[:1])+id[1:],
							activityCase.active[index],
							budgetTemplateID,
						))
						if activityCase.active[index] {
							want = append(want, id)
						}
					}

					response, err := QueryCatalog(
						context.Background(),
						singlePage(t, items...),
						tableName,
						zap.NewNop(),
					)
					if err != nil {
						t.Fatalf("QueryCatalog devolvio error: %v", err)
					}

					var got []string
					switch scopeCase.scope {
					case domain.ScopePlan:
						got = optionIDs(response.Plans)
					case domain.ScopeSeason:
						got = optionIDs(response.Seasons)
					case domain.ScopeDestination:
						got = destinationIDs(response.Destinations)
					}

					if !equalStrings(got, want) {
						t.Errorf("opciones expuestas = %v, se esperaban solo las activas %v", got, want)
					}
				})
			}
		})
	}
}

// TestQueryCatalogDiscardsUninterpretableItems comprueba que un item mal
// sembrado se descarte sin arrastrar consigo el resto del catalogo: dejar al
// formulario sin opciones seria un fallo mucho mayor que la opcion que falta.
func TestQueryCatalogDiscardsUninterpretableItems(t *testing.T) {
	sortKeyless := map[string]ddbtypes.AttributeValue{
		"pk": &ddbtypes.AttributeValueMemberS{Value: domain.CatalogPK},
	}

	unreadable := map[string]ddbtypes.AttributeValue{
		"pk": &ddbtypes.AttributeValueMemberS{Value: domain.CatalogPK},
		"sk": &ddbtypes.AttributeValueMemberS{Value: "PLAN#0003#ilegible"},
		// `active` como cadena: la tabla lo admite, el deserializador no.
		"active": &ddbtypes.AttributeValueMemberS{Value: "si"},
	}

	tests := []struct {
		name string
		item map[string]ddbtypes.AttributeValue
	}{
		{"sin clave de ordenamiento", sortKeyless},
		{"clave sin los tres componentes", rawOptionItem(t, "PLAN#incompleta", "Incompleta", true, "")},
		{"orden sin relleno de ceros", rawOptionItem(t, "PLAN#7#sin-relleno", "Sin relleno", true, "")},
		{"scope desconocido", rawOptionItem(t, "REGION#0001#araucania", "Araucania", true, "")},
		{"destino sin plantilla", rawOptionItem(t, "DESTINATION#0009#peru", "Peru", true, "")},
		{"atributos ilegibles", unreadable},
	}

	survivor := optionItem(t, domain.ScopePlan, 1, "full", "Full", true, "")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ddb := singlePage(t, test.item, survivor)

			response, err := QueryCatalog(context.Background(), ddb, tableName, zap.NewNop())
			if err != nil {
				t.Fatalf("un item mal sembrado no debe fallar la respuesta: %v", err)
			}

			if got := optionIDs(response.Plans); !equalStrings(got, []string{"full"}) {
				t.Errorf("planes = %v, se esperaba solo el item sano", got)
			}
			if len(response.Destinations) != 0 {
				t.Errorf("destinos = %v, el item descartado no debe aparecer", response.Destinations)
			}
		})
	}
}

// TestQueryCatalogFollowsPagination comprueba que una particion que no cabe en
// una pagina se recorra completa. Sin esto la respuesta se truncaria en silencio
// a medida que el catalogo crece.
func TestQueryCatalogFollowsPagination(t *testing.T) {
	lastKey := map[string]ddbtypes.AttributeValue{
		"pk": &ddbtypes.AttributeValueMemberS{Value: domain.CatalogPK},
		"sk": &ddbtypes.AttributeValueMemberS{Value: "PLAN#0001#full"},
	}

	ddb := &fakeDDB{
		t: t,
		pages: []*dynamodb.QueryOutput{
			{
				Items:            []map[string]ddbtypes.AttributeValue{optionItem(t, domain.ScopePlan, 1, "full", "Full", true, "")},
				LastEvaluatedKey: lastKey,
			},
			{
				Items: []map[string]ddbtypes.AttributeValue{
					optionItem(t, domain.ScopePlan, 2, "basico", "Basico", true, ""),
					settingsItem(t, program.CatalogSettings{ScenarioOffsets: []int{0}}),
				},
			},
		},
	}

	response, err := QueryCatalog(context.Background(), ddb, tableName, zap.NewNop())
	if err != nil {
		t.Fatalf("QueryCatalog devolvio error: %v", err)
	}

	if len(ddb.inputs) != 2 {
		t.Fatalf("se hicieron %d consultas y la particion venia paginada en 2", len(ddb.inputs))
	}

	second := ddb.inputs[1].ExclusiveStartKey
	startKey, ok := second["sk"].(*ddbtypes.AttributeValueMemberS)
	if !ok || startKey.Value != "PLAN#0001#full" {
		t.Errorf("la segunda consulta no continuo desde LastEvaluatedKey: %#v", second)
	}

	if got := optionIDs(response.Plans); !equalStrings(got, []string{"full", "basico"}) {
		t.Errorf("planes = %v, se esperaban los de ambas paginas", got)
	}
	if len(response.Settings.ScenarioOffsets) != 1 {
		t.Errorf("los parametros de la segunda pagina se perdieron: %#v", response.Settings)
	}
}

// TestQueryCatalogFailsWhenQueryFails comprueba que un fallo de DynamoDB si
// llegue como error: no hay respuesta parcial que valga, porque un catalogo
// vacio y un catalogo inaccesible piden reacciones distintas del frontend.
func TestQueryCatalogFailsWhenQueryFails(t *testing.T) {
	ddb := &fakeDDB{t: t, err: errors.New("throughput excedido")}

	if _, err := QueryCatalog(context.Background(), ddb, tableName, zap.NewNop()); err == nil {
		t.Fatal("un fallo de la consulta debe propagarse como error")
	}
}

// TestHandleServesCacheableResponse cubre el Requirement 16.10 y el envelope de
// exito del contrato.
func TestHandleServesCacheableResponse(t *testing.T) {
	ddb := singlePage(t,
		optionItem(t, domain.ScopePlan, 1, "full", "Full", true, ""),
		settingsItem(t, program.CatalogSettings{
			Margin:          ptr(fullMargin()),
			ScenarioOffsets: []int{-10, 0},
		}),
	)

	response, err := handle(context.Background(), testApp(ddb), testRequest())
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("estado = %d, se esperaba 200", response.StatusCode)
	}

	if got := response.Headers[cacheControlHeader]; got != cacheControlValue {
		t.Errorf("%s = %q, se esperaba %q", cacheControlHeader, got, cacheControlValue)
	}

	var envelope struct {
		Data Response `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("el cuerpo no respeta el envelope de exito: %v", err)
	}

	if got := optionIDs(envelope.Data.Plans); !equalStrings(got, []string{"full"}) {
		t.Errorf("planes del cuerpo = %v", got)
	}
	if envelope.Data.Settings.Margin == nil {
		t.Error("el cuerpo no expone los parametros de margen declarados")
	}
}

// TestHandleOmitsAbsentSettings cubre los Requirements 16.8 y 16.9: los campos
// que el item SETTINGS no declara se omiten del JSON y no viajan en cero. Un
// utilityRate de 0 significa vender al costo, y enviarlo donde no hay dato haria
// que el formulario precargue exactamente eso.
func TestHandleOmitsAbsentSettings(t *testing.T) {
	tests := []struct {
		name  string
		items []map[string]ddbtypes.AttributeValue
	}{
		{
			name:  "sin item de parametros",
			items: []map[string]ddbtypes.AttributeValue{optionItem(t, domain.ScopePlan, 1, "full", "Full", true, "")},
		},
		{
			name: "item de parametros sin declarar nada",
			items: []map[string]ddbtypes.AttributeValue{
				optionItem(t, domain.ScopePlan, 1, "full", "Full", true, ""),
				settingsItem(t, program.CatalogSettings{}),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := handle(context.Background(), testApp(singlePage(t, test.items...)), testRequest())
			if err != nil {
				t.Fatalf("handle devolvio error: %v", err)
			}

			var envelope struct {
				Data struct {
					Plans    []program.CatalogOption `json:"plans"`
					Settings map[string]any          `json:"settings"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
				t.Fatalf("el cuerpo no respeta el envelope de exito: %v", err)
			}

			if _, present := envelope.Data.Settings["margin"]; present {
				t.Error("margin no debe estar en el JSON cuando el catalogo no lo declara")
			}
			if _, present := envelope.Data.Settings["scenarioOffsets"]; present {
				t.Error("scenarioOffsets no debe estar en el JSON cuando el catalogo no lo declara")
			}
			if len(envelope.Data.Plans) != 1 {
				t.Errorf("los catalogos se sirven igual sin parametros: %v", envelope.Data.Plans)
			}
		})
	}
}

// TestHandleSerializesEmptyCatalogsAsArrays comprueba que un catalogo vacio
// viaje como [] y no como null: el contrato del frontend declara arreglos.
func TestHandleSerializesEmptyCatalogsAsArrays(t *testing.T) {
	response, err := handle(context.Background(), testApp(singlePage(t)), testRequest())
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("el cuerpo no respeta el envelope de exito: %v", err)
	}

	for _, field := range []string{"plans", "seasons", "destinations"} {
		if got := string(envelope.Data[field]); got != "[]" {
			t.Errorf("%s = %s, se esperaba []", field, got)
		}
	}
}

// TestHandleReturnsErrorEnvelopeWithoutCache comprueba que un fallo de DynamoDB
// se traduzca al envelope de error y que la respuesta fallida no se cachee: con
// Cache-Control puesto, el navegador dejaria el formulario roto cinco minutos.
func TestHandleReturnsErrorEnvelopeWithoutCache(t *testing.T) {
	ddb := &fakeDDB{t: t, err: errors.New("tabla inaccesible")}

	response, err := handle(context.Background(), testApp(ddb), testRequest())
	if err != nil {
		t.Fatalf("el handler no debe devolver el error como segundo valor: %v", err)
	}

	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("estado = %d, se esperaba 500", response.StatusCode)
	}

	if got, present := response.Headers[cacheControlHeader]; present {
		t.Errorf("una respuesta de error no debe llevar %s: %q", cacheControlHeader, got)
	}

	var envelope struct {
		Code    string `json:"code"`
		TraceID string `json:"traceId"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("el cuerpo no respeta el envelope de error: %v", err)
	}

	if envelope.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q, se esperaba INTERNAL_ERROR", envelope.Code)
	}
	if envelope.TraceID != testRequestID {
		t.Errorf("traceId = %q, se esperaba el requestId del evento", envelope.TraceID)
	}

	// El nombre de la tabla y el mensaje del SDK son detalle interno y no deben
	// salir en la respuesta.
	if body := response.Body; strings.Contains(body, tableName) {
		t.Errorf("la respuesta expone el nombre de la tabla: %s", body)
	}
}

// testRequestID es el requestId del evento simulado, que el envelope de error
// devuelve como traceId.
const testRequestID = "01JTESTREQUESTID0000000000"

// testApp arma la App del servicio con el cliente sustituido.
func testApp(ddb awsddb.Client) *functions.App {
	return &functions.App{
		Config: functions.Config{
			CatalogsTableName: tableName,
			FunctionName:      "fn-obtener-catalogos-v1",
		},
		DDB:   ddb,
		Stage: "test",
	}
}

// testRequest arma el evento de API Gateway de una peticion sin cuerpo ni
// parametros, que es todo lo que este endpoint recibe.
func testRequest() events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: testRequestID,
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: http.MethodGet,
				Path:   functions.CatalogsRoute.Path,
			},
		},
	}
}

// ptr devuelve un puntero al valor recibido, para los campos opcionales de
// program.CatalogSettings.
func ptr[T any](value T) *T {
	return &value
}
