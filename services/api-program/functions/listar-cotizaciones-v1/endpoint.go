// Package listarfavoritosv1 implementa `GET /favoritos?scope=programa`: el
// listado de los favoritos del usuario autenticado en un scope, del más
// reciente al más antiguo (Requirement 11.5).
//
// # De dónde sale el usuario
//
// De `requestContext.authorizer.lambda`, vía
// [lambdautil.UserIDFromContext], y de ningún otro lugar (Requirement 19.8).
// Ese identificador es la clave de partición de la tabla, así que *es* el
// aislamiento entre usuarios: si se pudiera elegir desde la petición, cambiar
// un valor en el cuerpo alcanzaría para leer los favoritos de otra persona.
//
// El handler no compara la identidad del contexto con una recibida ni elige
// entre las dos: solo lee del contexto. Un `userId` que llegue en el cuerpo, en
// un parámetro de consulta o en una cabecera se descarta antes de tocar la
// tabla y se registra en `warn`. La advertencia importa porque el caso tiene
// dos explicaciones y las dos conviene verlas en el log: un intento de
// suplantación, o un cliente mal escrito que cree estar filtrando por usuario y
// en realidad recibe lo que le corresponde por otra vía.
//
// # De dónde sale el orden
//
// De la clave de ordenamiento, con `ScanIndexForward: false`. El identificador
// de un favorito es un ULID y lleva el instante de creación en sus primeros 48
// bits, así que recorrer `sk` hacia atrás *es* recorrer la fecha de creación
// hacia atrás. Por eso no hay índice adicional y **el handler no ordena nada**:
// una llamada a sort.Slice acá sería trabajo redundante que además taparía el
// día en que la clave dejara de ordenar como se espera.
//
// # Por qué el scope se valida antes de consultar
//
// [domain.Scope.SKPrefix] devuelve cadena vacía para un scope desconocido, y
// `begins_with(sk, "")` es verdadero para todo ítem: una consulta con un scope
// no validado devolvería la partición completa del usuario en vez de una
// colección. Hoy eso serían favoritos de programa entregados bajo otro nombre;
// el día que la tabla aloje una segunda colección serían ítems de una feature
// distinta. El scope se valida con [domain.Scope.Valid] y la petición se
// rechaza antes de armar el Query.
package listarcotizacionesv1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions"
)

// ScopeQueryParam es el nombre del parámetro de consulta que trae el scope de
// los favoritos a listar.
//
// El scope viaja en la petición y no en el path porque el panel de favoritos es
// reutilizable: la colección es un dato de la consulta, no un recurso distinto.
// Es obligatorio, según el fragmento OpenAPI del servicio.
const ScopeQueryParam = "scope"

// operationName es el valor del campo `operation` del log. Identifica la
// operación sobre favoritos con independencia del nombre de la función, que ya
// viaja aparte en `functionName`.
const operationName = "list"

// maxLoggedIdentityLength acota el largo del identificador ajeno que se
// registra al advertir. Sin el corte, un cliente podría inflar la factura de
// ingesta de CloudWatch mandando un `userId` de un megabyte: lo que sirve para
// diagnosticar es el prefijo, no el valor completo.
const maxLoggedIdentityLength = 64

// clientIdentityFields son los nombres bajo los que un cliente podría intentar
// declarar de quién son los favoritos que pide, en el cuerpo o en los
// parámetros de consulta. Ninguno se usa: solo se detectan para advertir.
var clientIdentityFields = []string{"userId", "user_id", "sub"}

// clientIdentityHeaders son las cabeceras equivalentes. Van en minúsculas
// porque API Gateway v2 entrega los nombres normalizados así, y
// [lambdautil.EchoAdapter] hace lo mismo en local.
var clientIdentityHeaders = []string{"x-user-id", "userid"}

// Asegura que Register siga encajando en la firma que espera
// [functions.RunLocal]. Si alguna de las dos cambia, el módulo no compila en
// vez de dejar el endpoint sin registrar en el servidor local.
var _ functions.RegisterFunc = Register

// Handle procesa la invocación de `fn-listar-favoritos-v1` en AWS.
func Handle(
	ctx context.Context,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		// Sin app no hay logger: el arranque en frío ya falló y lo único que
		// queda es responder con el envelope de error.
		return lambdautil.ErrorResponse(req, err)
	}

	return handle(ctx, app, app.Logger(req.RequestContext.RequestID), req)
}

// Register registra el endpoint en el servidor local de Echo, que es la única
// forma de ejercitarlo mientras el authorizer compartido no exista
// (Requirements 19.4 y 19.5).
//
// El logger compartido que recibe no se usa: viene etiquetado con
// `requestId: "local"`, que es el arranque del servidor y no una solicitud, y
// derivar de él agregaría un segundo campo con el mismo nombre a cada línea.
// Cada solicitud construye el suyo con app.Logger, igual que en AWS, así que el
// log de local y el de una función desplegada tienen la misma forma.
func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.ListQuotationsRoute.Register(e, lambdautil.EchoAdapter(
		func(
			ctx context.Context,
			req events.APIGatewayV2HTTPRequest,
		) (events.APIGatewayV2HTTPResponse, error) {
			return handle(ctx, app, app.Logger(req.RequestContext.RequestID), req)
		},
	))
}

// handle resuelve la solicitud con las dependencias ya construidas.
//
// Es donde vive el endpoint completo, y recibe la app en vez de pedirla a
// [functions.GetApp] para que [Handle] y [Register] compartan un solo camino y
// para que los tests puedan pasarle un doble de DynamoDB sin tocar AWS.
func handle(
	ctx context.Context,
	app *functions.App,
	log *zap.Logger,
	req events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	userID, err := lambdautil.UserIDFromContext(req)
	if err != nil {
		logger.WarnOrError(log, err, "listFavoritesRejected",
			zap.String("operation", operationName),
		)

		return lambdautil.ErrorResponse(req, err)
	}

	// Desde acá toda línea lleva el usuario del contexto y la operación, que
	// son los dos campos que el diseño de observabilidad pide para favoritos.
	log = log.With(
		zap.String("userId", userID),
		zap.String("operation", operationName),
	)

	warnOnClientIdentity(log, req)

	scope, err := scopeFromRequest(req)
	if err != nil {
		logger.WarnOrError(log, err, "listFavoritesRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	partitionKey, err := userPartitionKey(userID)
	if err != nil {
		logger.WarnOrError(log, err, "listFavoritesRejected")

		return lambdautil.ErrorResponse(req, err)
	}

	rawItems, err := queryFavorites(
		ctx,
		app.DDB,
		app.Config.ProgramsTableName,
		partitionKey,
		scope.SKPrefix(),
	)
	if err != nil {
		logger.WarnOrError(log, err, "listFavoritesFailed")

		return lambdautil.ErrorResponse(req, err)
	}

	favorites := favoritesFromItems(log, rawItems)

	log.Info("favoritesListed",
		zap.String("scope", string(scope)),
		zap.Int("favoriteCount", len(favorites)),
	)

	return lambdautil.SuccessResponse(http.StatusOK, favorites)
}

// scopeFromRequest lee y valida el scope del parámetro de consulta.
//
// Un scope ausente y uno desconocido devuelven los dos VALIDATION_ERROR, que es
// lo que declara el fragmento OpenAPI del servicio para el 400 de este
// endpoint. El catálogo de api-design.md tiene códigos más específicos
// —INVALID_QUERY_PARAMETER y REQUIRED_FIELD_MISSING— y no se usan a propósito:
// el primero no está entre los códigos que la feature declara, y el segundo
// corresponde a un campo obligatorio del cuerpo. Cambiar el código acá sería
// romper el contrato publicado sin actualizarlo primero.
func scopeFromRequest(req events.APIGatewayV2HTTPRequest) (domain.Scope, error) {
	// Indexar un mapa nil es válido en Go y devuelve el valor cero, así que una
	// petición sin parámetros de consulta entra por la rama del scope ausente.
	raw := strings.TrimSpace(req.QueryStringParameters[ScopeQueryParam])
	if raw == "" {
		return "", apperr.
			Validationf("Falta el parámetro de consulta %s", ScopeQueryParam).
			WithDetails(map[string]any{"field": ScopeQueryParam})
	}

	scope := domain.Scope(raw)
	if !scope.Valid() {
		return "", apperr.
			Validationf("El scope %q no existe", raw).
			WithDetails(map[string]any{
				"field":    ScopeQueryParam,
				"received": raw,
			})
	}

	return scope, nil
}

// userPartitionKey arma la clave de partición del usuario.
//
// Traduce el error de [domain.UserPK] a UNAUTHORIZED en vez de dejarlo
// convertirse en un 500. Hoy la rama es inalcanzable, porque
// [lambdautil.UserIDFromContext] ya rechaza un identificador vacío o en blanco;
// se traduce igual para que un cambio futuro en esa función no transforme la
// falta de identidad en una falla interna.
func userPartitionKey(userID string) (string, error) {
	partitionKey, err := domain.UserPK(userID)
	if err != nil {
		return "", apperr.
			Unauthorized("No se pudo identificar al usuario de la solicitud").
			WithCause(err)
	}

	return partitionKey, nil
}

// queryFavorites lee los ítems de favoritos del usuario en un scope, del más
// reciente al más antiguo, siguiendo la paginación hasta agotarla.
//
// Devuelve los ítems crudos y no favoritos ya traducidos porque la traducción
// puede descartar ítems, y esa decisión necesita el logger: ver
// [favoritesFromItems].
//
// El recorrido de páginas no lleva tope propio. Un usuario tiene decenas de
// favoritos y DynamoDB garantiza que LastEvaluatedKey se agote, así que el
// único caso en que el bucle no terminaría por sí solo es una falla del
// servicio — y de eso se encarga ctx, que al vencer el timeout de la invocación
// hace que Query devuelva error y corte. Un tope de páginas, en cambio,
// truncaría la lista en silencio y le entregaría al usuario menos favoritos de
// los que tiene sin que nada lo delate.
func queryFavorites(
	ctx context.Context,
	client awsddb.Client,
	tableName string,
	partitionKey string,
	sortKeyPrefix string,
) ([]map[string]types.AttributeValue, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		KeyConditionExpression: aws.String("#pk = :pk AND begins_with(#sk, :skPrefix)"),
		// pk y sk se pasan como nombres de expresión por prolijidad: no son
		// palabras reservadas de DynamoDB, pero la expresión queda legible y
		// deja de importar si alguna vez lo fueran.
		ExpressionAttributeNames: map[string]string{
			"#pk": "pk",
			"#sk": "sk",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":       &types.AttributeValueMemberS{Value: partitionKey},
			":skPrefix": &types.AttributeValueMemberS{Value: sortKeyPrefix},
		},
		// El orden descendente lo da la clave de ordenamiento: ver la nota del
		// paquete sobre de dónde sale el orden.
		ScanIndexForward: aws.Bool(false),
	}

	var rawItems []map[string]types.AttributeValue

	for {
		output, err := client.Query(ctx, input)
		if err != nil {
			// El nombre de la tabla y la expresión quedan en la causa, que solo
			// viaja al log: la respuesta HTTP no expone detalles internos.
			return nil, apperr.Internal(
				fmt.Errorf("consulta de favoritos en %q: %w", tableName, err),
			)
		}

		rawItems = append(rawItems, output.Items...)

		if len(output.LastEvaluatedKey) == 0 {
			return rawItems, nil
		}

		input.ExclusiveStartKey = output.LastEvaluatedKey
	}
}

// favoritesFromItems traduce los ítems de la tabla a los favoritos de la
// respuesta, conservando el orden en que DynamoDB los entregó.
//
// Un ítem que no se puede interpretar se descarta con una advertencia y no
// hace fallar la respuesta. Es deliberado: un ítem mal sembrado en la partición
// de un usuario no es razón para dejarlo sin los otros favoritos que sí están
// bien, y un 500 tampoco le daría al operador más información que el `warn`
// —que además nombra la clave del ítem culpable.
//
// La traducción es ítem por ítem y no con UnmarshalListOfMaps por esa misma
// razón: la versión de lista falla entera ante un solo ítem defectuoso.
//
// El resultado nunca es nil, sino una porción vacía. La diferencia es visible
// en el contrato: un nil se serializa como `null` y lo que el fragmento OpenAPI
// declara para un usuario sin favoritos es una lista vacía.
func favoritesFromItems(
	log *zap.Logger,
	rawItems []map[string]types.AttributeValue,
) []domain.Favorite {
	favorites := make([]domain.Favorite, 0, len(rawItems))

	for _, rawItem := range rawItems {
		var item domain.FavoriteItem
		if err := attributevalue.UnmarshalMap(rawItem, &item); err != nil {
			log.Warn("favoriteItemDiscarded",
				zap.String("reason", "unmarshalError"),
				zap.String("sk", rawSortKey(rawItem)),
				zap.Error(err),
			)

			continue
		}

		favorite, err := item.Favorite()
		if err != nil {
			log.Warn("favoriteItemDiscarded",
				zap.String("reason", "malformedSortKey"),
				zap.String("sk", item.SK),
				zap.Error(err),
			)

			continue
		}

		favorites = append(favorites, favorite)
	}

	return favorites
}

// rawSortKey extrae la clave de ordenamiento de un ítem sin decodificar, para
// poder nombrarlo en la advertencia cuando es justamente la decodificación lo
// que falló. Devuelve cadena vacía si el atributo no está o no es texto, que es
// en sí mismo el diagnóstico de un ítem que no pertenece a esta tabla.
func rawSortKey(rawItem map[string]types.AttributeValue) string {
	attribute, present := rawItem["sk"]
	if !present {
		return ""
	}

	text, isString := attribute.(*types.AttributeValueMemberS)
	if !isString {
		return ""
	}

	return text.Value
}

// warnOnClientIdentity registra una advertencia por cada identificador de
// usuario que la petición traiga por su cuenta, en el cuerpo, en un parámetro
// de consulta o en una cabecera.
//
// No devuelve error y no altera el resultado: la identidad ya se resolvió desde
// el contexto del authorizer antes de llamar acá. Rechazar la petición sería
// una alternativa razonable, pero convertiría en un fallo lo que muchas veces
// es un cliente que manda un campo de más, y el aislamiento no depende de eso:
// depende de que el valor recibido no se use nunca, que es lo que garantiza que
// esta función no lo devuelva.
func warnOnClientIdentity(log *zap.Logger, req events.APIGatewayV2HTTPRequest) {
	for _, field := range clientIdentityFields {
		if value, present := req.QueryStringParameters[field]; present {
			warnIdentityIgnored(log, "queryString", field, value)
		}
	}

	for _, header := range clientIdentityHeaders {
		if value, present := req.Headers[header]; present {
			warnIdentityIgnored(log, "header", header, value)
		}
	}

	for field, value := range identityFieldsInBody(req) {
		warnIdentityIgnored(log, "body", field, value)
	}
}

// warnIdentityIgnored registra que un identificador recibido se descartó.
//
// La línea ya lleva el `userId` del contexto, que es el que se usó, así que el
// par de campos deja ver de una sola lectura si alguien pidió los favoritos de
// otra persona o solo repitió los suyos.
func warnIdentityIgnored(log *zap.Logger, source, field, received string) {
	log.Warn("clientIdentityIgnored",
		zap.String("source", source),
		zap.String("field", field),
		zap.String("receivedUserId", truncate(received, maxLoggedIdentityLength)),
	)
}

// identityFieldsInBody busca identificadores de usuario en el cuerpo JSON de la
// solicitud.
//
// Toda falla al leer el cuerpo se ignora en silencio y no rechaza la petición.
// Este endpoint es un GET y no tiene cuerpo: lo inspecciona solo para advertir,
// así que un cuerpo ilegible es exactamente igual de irrelevante que un cuerpo
// ausente. Tratarlo como INVALID_REQUEST_FORMAT, que es lo que haría
// [lambdautil.BindJSON], convertiría un dato que no se usa en un motivo de
// rechazo.
func identityFieldsInBody(req events.APIGatewayV2HTTPRequest) map[string]string {
	body := req.Body

	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil
		}
		body = string(decoded)
	}

	if strings.TrimSpace(body) == "" {
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return nil
	}

	found := make(map[string]string, len(clientIdentityFields))

	for _, field := range clientIdentityFields {
		raw, present := fields[field]
		if !present {
			continue
		}

		// El valor puede no ser una cadena. Se registra su forma cruda en ese
		// caso, porque un `userId` numérico o un objeto es tan indicativo como
		// uno de texto y perderlo dejaría la advertencia sin el dato que la
		// hace útil.
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			value = string(raw)
		}

		found[field] = value
	}

	return found
}

// truncate acorta text a limit caracteres, dejando marca de que se recortó.
//
// Cuenta runas y no bytes: cortar por bytes partiría un carácter multibyte al
// medio y dejaría en el log una secuencia UTF-8 inválida.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	return string(runes[:limit]) + "…"
}
