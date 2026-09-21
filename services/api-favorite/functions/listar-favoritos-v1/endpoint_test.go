package listarfavoritosv1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-favorite/functions"
)

// Datos fijos de los casos. El usuario del contexto y el que las peticiones
// intentan imponer son distintos a propósito: es la única forma de comprobar
// cuál de los dos terminó en la consulta.
const (
	testTableName    = "ind-dev-favoritos-ddb-dev-pri-use1"
	contextUserID    = "01JCTXUSER0000000000000000"
	impostorUserID   = "01JIMPOSTOR000000000000000"
	testRequestID    = "01JREQ00000000000000000000"
	unknownScopeName = "presupuesto"
)

// Identificadores de favoritos en orden creciente. Al ser ULIDs, el orden
// lexicográfico es el orden de creación, así que el más reciente es el último.
const (
	oldestFavoriteID = "01JQZ8AAAAAAAAAAAAAAAAAAAA"
	middleFavoriteID = "01JQZ8BBBBBBBBBBBBBBBBBBBB"
	newestFavoriteID = "01JQZ8CCCCCCCCCCCCCCCCCCCC"
)

// queryCall es una invocación registrada de Query, con lo que el handler pidió.
type queryCall struct {
	tableName              string
	keyConditionExpression string
	attributeValues        map[string]types.AttributeValue
	scanIndexForward       *bool
	exclusiveStartKey      map[string]types.AttributeValue
}

// fakeDDB es el doble de [awsddb.Client] de los tests: devuelve páginas
// preparadas y registra cada consulta, sin tocar AWS.
//
// Solo implementa Query con comportamiento. Los otros cuatro métodos existen
// para satisfacer la interfaz y fallan si alguien los llama: este endpoint no
// escribe ni lee por clave completa, y una llamada inesperada debe romper el
// test en vez de pasar inadvertida.
type fakeDDB struct {
	t *testing.T

	// pages son las respuestas de Query en orden. Cada una se entrega en una
	// invocación sucesiva.
	pages []*dynamodb.QueryOutput

	// err es el error que devuelve Query, si está definido.
	err error

	// calls acumula lo que el handler pidió en cada invocación.
	calls []queryCall
}

func (f *fakeDDB) Query(
	ctx context.Context,
	params *dynamodb.QueryInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	f.t.Helper()

	if ctx == nil {
		f.t.Fatal("Query recibió un contexto nil")
	}

	call := queryCall{
		scanIndexForward:  params.ScanIndexForward,
		exclusiveStartKey: params.ExclusiveStartKey,
		attributeValues:   params.ExpressionAttributeValues,
	}
	if params.TableName != nil {
		call.tableName = *params.TableName
	}
	if params.KeyConditionExpression != nil {
		call.keyConditionExpression = *params.KeyConditionExpression
	}
	f.calls = append(f.calls, call)

	if f.err != nil {
		return nil, f.err
	}

	index := len(f.calls) - 1
	if index >= len(f.pages) {
		f.t.Fatalf("Query se invocó %d veces y solo hay %d páginas preparadas", len(f.calls), len(f.pages))
	}

	return f.pages[index], nil
}

func (f *fakeDDB) GetItem(
	context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	f.t.Fatal("el listado de favoritos no debe usar GetItem")

	return nil, nil
}

func (f *fakeDDB) PutItem(
	context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	f.t.Fatal("el listado de favoritos no debe escribir en la tabla")

	return nil, nil
}

func (f *fakeDDB) UpdateItem(
	context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	f.t.Fatal("el listado de favoritos no debe escribir en la tabla")

	return nil, nil
}

func (f *fakeDDB) DeleteItem(
	context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.t.Fatal("el listado de favoritos no debe borrar de la tabla")

	return nil, nil
}

// newApp arma la app del servicio con el doble de DynamoDB.
func newApp(ddb *fakeDDB) *functions.App {
	return &functions.App{
		Config: functions.Config{
			FavoritesTableName: testTableName,
			FunctionName:       "fn-listar-favoritos-v1",
			Port:               "8082",
		},
		DDB:   ddb,
		Stage: "test",
	}
}

// newTestLogger devuelve un logger y el registro de sus líneas, para poder
// afirmar sobre las advertencias que el endpoint debe dejar.
func newTestLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)

	return zap.New(core), logs
}

// request arma un evento de API Gateway con el usuario en el contexto del
// authorizer, que es el único lugar del que el endpoint lee la identidad.
func request(scope string) events.APIGatewayV2HTTPRequest {
	req := events.APIGatewayV2HTTPRequest{
		RawPath: "/favoritos",
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: testRequestID,
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				Lambda: map[string]any{
					lambdautil.AuthorizerUserIDKey: contextUserID,
				},
			},
		},
	}

	if scope != "" {
		req.QueryStringParameters = map[string]string{ScopeQueryParam: scope}
	}

	return req
}

// favoriteItem arma el ítem de un favorito tal como queda guardado.
func favoriteItem(t *testing.T, userID, favoriteID, name string) map[string]types.AttributeValue {
	t.Helper()

	createdAt := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	item, err := domain.NewFavoriteItem(
		domain.FavoriteKey{UserID: userID, Scope: domain.ScopeProgram, ID: favoriteID},
		name,
		domain.FavoriteContent{},
		createdAt,
		createdAt,
	)
	if err != nil {
		t.Fatalf("no se pudo armar el item del favorito %s: %v", favoriteID, err)
	}

	raw, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("no se pudo serializar el item del favorito %s: %v", favoriteID, err)
	}

	return raw
}

// page arma una respuesta de Query con los ítems indicados y sin continuación.
func page(items ...map[string]types.AttributeValue) *dynamodb.QueryOutput {
	return &dynamodb.QueryOutput{Items: items}
}

// decodeFavorites extrae la lista de favoritos del envelope { data } de la
// respuesta.
func decodeFavorites(t *testing.T, body string) []domain.Favorite {
	t.Helper()

	var envelope struct {
		Data []domain.Favorite `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("la respuesta no es el envelope de exito esperado: %v — %s", err, body)
	}

	return envelope.Data
}

// decodeError extrae el código del envelope de error de la respuesta.
func decodeError(t *testing.T, body string) (code string, traceID string) {
	t.Helper()

	var envelope struct {
		Code    string `json:"code"`
		TraceID string `json:"traceId"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("la respuesta no es el envelope de error esperado: %v — %s", err, body)
	}

	return envelope.Code, envelope.TraceID
}

// favoriteIDs devuelve los identificadores de los favoritos en el orden en que
// llegaron.
func favoriteIDs(favorites []domain.Favorite) []string {
	ids := make([]string, 0, len(favorites))
	for _, favorite := range favorites {
		ids = append(ids, favorite.ID)
	}

	return ids
}

// stringValue devuelve el texto de un valor de expresión de DynamoDB.
func stringValue(t *testing.T, values map[string]types.AttributeValue, key string) string {
	t.Helper()

	attribute, present := values[key]
	if !present {
		t.Fatalf("la consulta no declara el valor de expresion %s", key)
	}

	text, isString := attribute.(*types.AttributeValueMemberS)
	if !isString {
		t.Fatalf("el valor de expresion %s no es texto: %T", key, attribute)
	}

	return text.Value
}

// TestListaFavoritosDelUsuarioDelContexto comprueba el camino feliz: la
// consulta se arma sobre la partición del usuario del contexto, acotada al
// prefijo del scope y en orden descendente, y la respuesta devuelve los
// favoritos en el orden en que DynamoDB los entregó.
func TestListaFavoritosDelUsuarioDelContexto(t *testing.T) {
	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{
		page(
			favoriteItem(t, contextUserID, newestFavoriteID, "Bariloche 2026"),
			favoriteItem(t, contextUserID, middleFavoriteID, "Brasil 2026"),
			favoriteItem(t, contextUserID, oldestFavoriteID, "Sur de Chile"),
		),
	}}
	log, _ := newTestLogger()

	response, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram)))
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, se esperaba %d — %s", response.StatusCode, http.StatusOK, response.Body)
	}

	if len(ddb.calls) != 1 {
		t.Fatalf("Query se invoco %d veces, se esperaba 1", len(ddb.calls))
	}

	call := ddb.calls[0]

	if call.tableName != testTableName {
		t.Errorf("TableName = %q, se esperaba %q", call.tableName, testTableName)
	}

	wantExpression := "#pk = :pk AND begins_with(#sk, :skPrefix)"
	if call.keyConditionExpression != wantExpression {
		t.Errorf("KeyConditionExpression = %q, se esperaba %q", call.keyConditionExpression, wantExpression)
	}

	wantPK := domain.UserPKPrefix + contextUserID
	if got := stringValue(t, call.attributeValues, ":pk"); got != wantPK {
		t.Errorf(":pk = %q, se esperaba %q", got, wantPK)
	}

	wantPrefix := domain.ScopeProgram.SKPrefix()
	if got := stringValue(t, call.attributeValues, ":skPrefix"); got != wantPrefix {
		t.Errorf(":skPrefix = %q, se esperaba %q", got, wantPrefix)
	}

	// El orden descendente lo pide la consulta, no el handler: sin esto el
	// panel mostraria los favoritos mas viejos primero.
	if call.scanIndexForward == nil || *call.scanIndexForward {
		t.Error("ScanIndexForward debe ser false para entregar los favoritos del mas reciente al mas antiguo")
	}

	favorites := decodeFavorites(t, response.Body)

	wantIDs := []string{newestFavoriteID, middleFavoriteID, oldestFavoriteID}
	if got := favoriteIDs(favorites); !equalStrings(got, wantIDs) {
		t.Errorf("orden de los favoritos = %v, se esperaba %v", got, wantIDs)
	}

	if favorites[0].Name != "Bariloche 2026" {
		t.Errorf("el primer favorito se llama %q, se esperaba %q", favorites[0].Name, "Bariloche 2026")
	}

	if favorites[0].Scope != domain.ScopeProgram {
		t.Errorf("scope del favorito = %q, se esperaba %q", favorites[0].Scope, domain.ScopeProgram)
	}
}

// TestNoReordenaLoQueEntregaDynamoDB comprueba que el handler no ordene en Go.
//
// Los ítems se entregan a propósito en un orden que no es el de la clave. Si el
// handler ordenara —aunque fuera "para asegurarse"— el resultado saldria
// distinto del que dio la tabla, y esa duplicacion de responsabilidad es la que
// taparia el dia en que ScanIndexForward dejara de estar en false.
func TestNoReordenaLoQueEntregaDynamoDB(t *testing.T) {
	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{
		page(
			favoriteItem(t, contextUserID, middleFavoriteID, "Brasil 2026"),
			favoriteItem(t, contextUserID, oldestFavoriteID, "Sur de Chile"),
			favoriteItem(t, contextUserID, newestFavoriteID, "Bariloche 2026"),
		),
	}}
	log, _ := newTestLogger()

	response, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram)))
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	wantIDs := []string{middleFavoriteID, oldestFavoriteID, newestFavoriteID}
	if got := favoriteIDs(decodeFavorites(t, response.Body)); !equalStrings(got, wantIDs) {
		t.Errorf("orden de los favoritos = %v, se esperaba %v (el orden de la tabla, sin reordenar)", got, wantIDs)
	}
}

// TestSigueLaPaginacionDelQuery comprueba que el handler recorra todas las
// páginas. Sin esto un usuario con muchos favoritos recibiria solo los primeros
// y no habria nada en la respuesta que delatara la perdida.
func TestSigueLaPaginacionDelQuery(t *testing.T) {
	continuation := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: domain.UserPKPrefix + contextUserID},
		"sk": &types.AttributeValueMemberS{Value: domain.ScopeProgram.SKPrefix() + middleFavoriteID},
	}

	firstPage := page(
		favoriteItem(t, contextUserID, newestFavoriteID, "Bariloche 2026"),
		favoriteItem(t, contextUserID, middleFavoriteID, "Brasil 2026"),
	)
	firstPage.LastEvaluatedKey = continuation

	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{
		firstPage,
		page(favoriteItem(t, contextUserID, oldestFavoriteID, "Sur de Chile")),
	}}
	log, _ := newTestLogger()

	response, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram)))
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if len(ddb.calls) != 2 {
		t.Fatalf("Query se invoco %d veces, se esperaban 2 (una por pagina)", len(ddb.calls))
	}

	if ddb.calls[0].exclusiveStartKey != nil {
		t.Error("la primera consulta no debe llevar ExclusiveStartKey")
	}

	if got := stringValue(t, ddb.calls[1].exclusiveStartKey, "sk"); got != domain.ScopeProgram.SKPrefix()+middleFavoriteID {
		t.Errorf("ExclusiveStartKey de la segunda consulta = %q, se esperaba la LastEvaluatedKey de la primera", got)
	}

	wantIDs := []string{newestFavoriteID, middleFavoriteID, oldestFavoriteID}
	if got := favoriteIDs(decodeFavorites(t, response.Body)); !equalStrings(got, wantIDs) {
		t.Errorf("favoritos = %v, se esperaban las dos paginas completas %v", got, wantIDs)
	}
}

// TestProperty39UserIdentityComesFromAuthorizerContext es el corazón del
// Requirement 19.8: la identidad sale del contexto del authorizer y el valor
// que venga en el cuerpo, en un parámetro de consulta o en una cabecera se
// descarta y queda registrado como advertencia.
//
// Feature: program-form, Property 39: La identidad del usuario proviene del contexto del authorizer.
func TestProperty39UserIdentityComesFromAuthorizerContext(t *testing.T) {
	cases := []struct {
		name       string
		wantSource string
		mutate     func(req *events.APIGatewayV2HTTPRequest)
	}{
		{
			name:       "en el cuerpo",
			wantSource: "body",
			mutate: func(req *events.APIGatewayV2HTTPRequest) {
				req.Body = `{"userId":"` + impostorUserID + `"}`
			},
		},
		{
			name:       "en un parametro de consulta",
			wantSource: "queryString",
			mutate: func(req *events.APIGatewayV2HTTPRequest) {
				req.QueryStringParameters["userId"] = impostorUserID
			},
		},
		{
			name:       "en una cabecera",
			wantSource: "header",
			mutate: func(req *events.APIGatewayV2HTTPRequest) {
				req.Headers = map[string]string{"x-user-id": impostorUserID}
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{
				page(favoriteItem(t, contextUserID, newestFavoriteID, "Bariloche 2026")),
			}}
			log, logs := newTestLogger()

			req := request(string(domain.ScopeProgram))
			testCase.mutate(&req)

			response, err := handle(context.Background(), newApp(ddb), log, req)
			if err != nil {
				t.Fatalf("handle devolvio error: %v", err)
			}

			if response.StatusCode != http.StatusOK {
				t.Fatalf("StatusCode = %d, se esperaba %d — %s", response.StatusCode, http.StatusOK, response.Body)
			}

			// Lo decisivo: la particion consultada es la del usuario del
			// contexto, no la del identificador recibido.
			wantPK := domain.UserPKPrefix + contextUserID
			if got := stringValue(t, ddb.calls[0].attributeValues, ":pk"); got != wantPK {
				t.Errorf(":pk = %q, se esperaba %q — la identidad debe salir del contexto del authorizer", got, wantPK)
			}

			warnings := logs.FilterMessage("clientIdentityIgnored").All()
			if len(warnings) != 1 {
				t.Fatalf("se registraron %d advertencias clientIdentityIgnored, se esperaba 1", len(warnings))
			}

			warning := warnings[0]
			if warning.Level != zapcore.WarnLevel {
				t.Errorf("nivel de la advertencia = %s, se esperaba warn", warning.Level)
			}

			fields := warning.ContextMap()
			if fields["source"] != testCase.wantSource {
				t.Errorf("source = %v, se esperaba %q", fields["source"], testCase.wantSource)
			}
			if fields["receivedUserId"] != impostorUserID {
				t.Errorf("receivedUserId = %v, se esperaba %q", fields["receivedUserId"], impostorUserID)
			}
			if fields["userId"] != contextUserID {
				t.Errorf("userId = %v, se esperaba %q (el del contexto)", fields["userId"], contextUserID)
			}
		})
	}
}

// TestNoAdvierteCuandoLaPeticionNoTraeIdentidad comprueba que la advertencia sea
// específica del caso que la motiva y no ruido en toda invocación.
func TestNoAdvierteCuandoLaPeticionNoTraeIdentidad(t *testing.T) {
	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{page()}}
	log, logs := newTestLogger()

	if _, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram))); err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if count := logs.FilterMessage("clientIdentityIgnored").Len(); count != 0 {
		t.Errorf("se registraron %d advertencias clientIdentityIgnored, se esperaban 0", count)
	}
}

// TestRechazaSinConsultarCuandoElScopeNoSirve comprueba el rechazo del scope
// ausente y del desconocido.
//
// La afirmación de que no hubo ninguna consulta es la que importa más que el
// código de error: SKPrefix devuelve cadena vacía para un scope desconocido, y
// `begins_with(sk, "")` es verdadero para todo item, asi que una consulta con un
// scope no validado devolveria la particion completa del usuario.
func TestRechazaSinConsultarCuandoElScopeNoSirve(t *testing.T) {
	cases := []struct {
		name  string
		scope string
	}{
		{name: "scope ausente", scope: ""},
		{name: "scope desconocido", scope: unknownScopeName},
		{name: "scope en blanco", scope: "   "},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ddb := &fakeDDB{t: t}
			log, _ := newTestLogger()

			response, err := handle(context.Background(), newApp(ddb), log, request(testCase.scope))
			if err != nil {
				t.Fatalf("handle devolvio error: %v", err)
			}

			if len(ddb.calls) != 0 {
				t.Errorf("se consulto la tabla %d veces con un scope invalido, no debe consultarse ninguna", len(ddb.calls))
			}

			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("StatusCode = %d, se esperaba %d — %s", response.StatusCode, http.StatusBadRequest, response.Body)
			}

			code, traceID := decodeError(t, response.Body)
			if code != apperr.CodeValidationError {
				t.Errorf("code = %q, se esperaba %q (lo que declara el fragmento OpenAPI)", code, apperr.CodeValidationError)
			}
			if traceID != testRequestID {
				t.Errorf("traceId = %q, se esperaba el requestId del evento %q", traceID, testRequestID)
			}
		})
	}
}

// TestRechazaSinConsultarCuandoNoHayIdentidad comprueba que una invocación sin
// contexto de authorizer se rechace con 401 y sin llegar a la tabla.
//
// Es el escenario real mientras el authorizer compartido no exista: si alguien
// desplegara el endpoint como publico por descuido, no responderia los favoritos
// de nadie.
func TestRechazaSinConsultarCuandoNoHayIdentidad(t *testing.T) {
	ddb := &fakeDDB{t: t}
	log, _ := newTestLogger()

	req := request(string(domain.ScopeProgram))
	req.RequestContext.Authorizer = nil

	response, err := handle(context.Background(), newApp(ddb), log, req)
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if len(ddb.calls) != 0 {
		t.Errorf("se consulto la tabla %d veces sin identidad, no debe consultarse ninguna", len(ddb.calls))
	}

	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, se esperaba %d — %s", response.StatusCode, http.StatusUnauthorized, response.Body)
	}

	if code, _ := decodeError(t, response.Body); code != apperr.CodeUnauthorized {
		t.Errorf("code = %q, se esperaba %q", code, apperr.CodeUnauthorized)
	}
}

// TestDescartaItemsMalFormadosSinPerderElResto comprueba que un ítem con clave
// de ordenamiento invalida se descarte con una advertencia y que los favoritos
// sanos lleguen igual.
//
// Un item mal sembrado en la particion de un usuario no es razon para dejarlo
// sin el resto de su trabajo guardado.
func TestDescartaItemsMalFormadosSinPerderElResto(t *testing.T) {
	malformed := favoriteItem(t, contextUserID, middleFavoriteID, "Item roto")
	malformed["sk"] = &types.AttributeValueMemberS{Value: "FAV#PROGRAMA"}

	foreignCollection := favoriteItem(t, contextUserID, oldestFavoriteID, "Otra coleccion")
	foreignCollection["sk"] = &types.AttributeValueMemberS{Value: "OTRO#PROGRAMA#" + oldestFavoriteID}

	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{
		page(
			favoriteItem(t, contextUserID, newestFavoriteID, "Bariloche 2026"),
			malformed,
			foreignCollection,
		),
	}}
	log, logs := newTestLogger()

	response, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram)))
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, se esperaba %d — un item roto no debe hacer fallar la respuesta", response.StatusCode, http.StatusOK)
	}

	wantIDs := []string{newestFavoriteID}
	if got := favoriteIDs(decodeFavorites(t, response.Body)); !equalStrings(got, wantIDs) {
		t.Errorf("favoritos = %v, se esperaba %v", got, wantIDs)
	}

	discarded := logs.FilterMessage("favoriteItemDiscarded").All()
	if len(discarded) != 2 {
		t.Fatalf("se registraron %d descartes, se esperaban 2", len(discarded))
	}

	for _, entry := range discarded {
		if entry.Level != zapcore.WarnLevel {
			t.Errorf("nivel del descarte = %s, se esperaba warn", entry.Level)
		}
		if entry.ContextMap()["sk"] == "" {
			t.Error("el descarte debe nombrar la clave del item culpable")
		}
	}
}

// TestUsuarioSinFavoritosDevuelveListaVacia comprueba que la respuesta lleve
// `[]` y no `null`.
//
// El fragmento OpenAPI declara una lista vacia para el usuario sin favoritos, y
// la diferencia no es cosmetica: el panel recorre `data` y un `null` lo obligaria
// a distinguir dos formas del mismo caso.
func TestUsuarioSinFavoritosDevuelveListaVacia(t *testing.T) {
	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{page()}}
	log, logs := newTestLogger()

	response, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram)))
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, se esperaba %d", response.StatusCode, http.StatusOK)
	}

	if want := `{"data":[]}`; response.Body != want {
		t.Errorf("Body = %s, se esperaba %s", response.Body, want)
	}

	listed := logs.FilterMessage("favoritesListed").All()
	if len(listed) != 1 {
		t.Fatalf("se registraron %d lineas favoritesListed, se esperaba 1", len(listed))
	}

	fields := listed[0].ContextMap()
	if fields["favoriteCount"] != int64(0) {
		t.Errorf("favoriteCount = %v, se esperaba 0", fields["favoriteCount"])
	}
}

// TestLogDeExitoLlevaLosCamposDelDisenoYNadaMas comprueba los campos del log
// `info` de la operación.
//
// La segunda mitad del nombre es la que importa: el contenido del favorito, los
// nombres y los documentos de los tripulantes no pueden aparecer en el log. Son
// datos personales, y un programa con 100 servicios convertiria cada listado en
// una linea enorme que se paga por GB de ingesta.
func TestLogDeExitoLlevaLosCamposDelDisenoYNadaMas(t *testing.T) {
	ddb := &fakeDDB{t: t, pages: []*dynamodb.QueryOutput{
		page(favoriteItem(t, contextUserID, newestFavoriteID, "Bariloche 2026")),
	}}
	log, logs := newTestLogger()

	if _, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram))); err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	listed := logs.FilterMessage("favoritesListed").All()
	if len(listed) != 1 {
		t.Fatalf("se registraron %d lineas favoritesListed, se esperaba 1", len(listed))
	}

	fields := listed[0].ContextMap()

	if listed[0].Level != zapcore.InfoLevel {
		t.Errorf("nivel = %s, se esperaba info", listed[0].Level)
	}
	if fields["userId"] != contextUserID {
		t.Errorf("userId = %v, se esperaba %q", fields["userId"], contextUserID)
	}
	if fields["operation"] != operationName {
		t.Errorf("operation = %v, se esperaba %q", fields["operation"], operationName)
	}
	if fields["favoriteCount"] != int64(1) {
		t.Errorf("favoriteCount = %v, se esperaba 1", fields["favoriteCount"])
	}

	for _, forbidden := range []string{"content", "favorite", "crews", "services", "name"} {
		if _, present := fields[forbidden]; present {
			t.Errorf("el log no debe llevar el campo %q", forbidden)
		}
	}
}

// TestFallaDeDynamoDBDevuelveErrorInterno comprueba que una falla de la tabla se
// traduzca a INTERNAL_ERROR sin exponer el mensaje crudo del SDK ni el nombre de
// la tabla al cliente.
func TestFallaDeDynamoDBDevuelveErrorInterno(t *testing.T) {
	ddb := &fakeDDB{t: t, err: errors.New("ProvisionedThroughputExceededException")}
	log, _ := newTestLogger()

	response, err := handle(context.Background(), newApp(ddb), log, request(string(domain.ScopeProgram)))
	if err != nil {
		t.Fatalf("handle devolvio error: %v", err)
	}

	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, se esperaba %d", response.StatusCode, http.StatusInternalServerError)
	}

	if code, _ := decodeError(t, response.Body); code != apperr.CodeInternalError {
		t.Errorf("code = %q, se esperaba %q", code, apperr.CodeInternalError)
	}

	for _, leaked := range []string{testTableName, "ProvisionedThroughputExceededException", "begins_with"} {
		if strings.Contains(response.Body, leaked) {
			t.Errorf("la respuesta expone un detalle interno (%q): %s", leaked, response.Body)
		}
	}
}

// equalStrings compara dos porciones de texto elemento por elemento.
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
