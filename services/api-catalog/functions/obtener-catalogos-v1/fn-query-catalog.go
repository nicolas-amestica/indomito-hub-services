package catalogs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"

	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/domain"
)

// Nombres de los atributos de clave de la tabla `catalogos`.
//
// El de la clave de ordenamiento se usa para leer la `sk` cruda antes de
// interpretar el ítem: es la forma de la clave, y no un atributo de tipo, lo
// que distingue una opción de catálogo del ítem de parámetros de política.
const (
	pkAttribute = "pk"
	skAttribute = "sk"
)

// Motivos por los que un ítem de la partición se descarta. Viajan en el campo
// `reason` del log de advertencia, que es lo que permite distinguir un ítem mal
// sembrado de un cambio de forma de la tabla sin volcar su contenido.
const (
	reasonMissingSortKey = "sortKeyAusente"
	reasonUnmarshal      = "atributosIlegibles"
	reasonMalformedKey   = "claveMalFormada"
	reasonMissingBudget  = "destinoSinPlantilla"
	reasonUnknownScope   = "scopeDesconocido"
)

// itemDiscardedEvent es el evento de log de un ítem descartado.
const itemDiscardedEvent = "catalogItemDiscarded"

// Response es el cuerpo de GET /catalogos: los tres catálogos vigentes y los
// parámetros de política de la empresa (Requirements 16.1, 16.6 y 16.7).
//
// Las tres listas se serializan siempre, vacías si no hay opciones, porque el
// contrato del frontend las declara como arreglos: un `null` en lugar de `[]`
// obligaría a cada consumidor a defenderse de un caso que la tabla no
// distingue del catálogo vacío.
//
// Settings, en cambio, sí distingue: sus dos campos se omiten cuando el ítem
// SETTINGS no los declara (Requirements 16.8 y 16.9), y de eso se ocupa
// program.CatalogSettings.
type Response struct {
	Plans        []program.CatalogOption     `json:"plans"`
	Seasons      []program.CatalogOption     `json:"seasons"`
	Destinations []program.DestinationOption `json:"destinations"`
	Settings     program.CatalogSettings     `json:"settings"`
}

// OptionCount es la cantidad de opciones que la respuesta expone, sumando los
// tres catálogos. Es el campo `optionCount` del log de la invocación.
func (r Response) OptionCount() int {
	return len(r.Plans) + len(r.Seasons) + len(r.Destinations)
}

// QueryCatalog resuelve la respuesta completa de GET /catalogos con un único
// Query sobre pk = CATALOG (Requirement 16.2).
//
// La consulta es una sola porque las opciones de plan, temporada y destino y el
// ítem de parámetros comparten la clave de partición: ver la nota de encabezado
// de domain/keys.go. Una consulta por scope daría el mismo resultado a costa de
// cuatro viajes a DynamoDB donde alcanza uno.
//
// Los ítems se reparten por la forma de su clave de ordenamiento y se agregan en
// el orden en que DynamoDB los entrega, que es el lexicográfico ascendente de la
// `sk`. Acá no se ordena nada, y es deliberado: el orden de presentación va
// embebido en la clave con relleno de ceros a cuatro dígitos justamente para que
// ese orden coincida con el numérico. Reordenar en Go dejaría el relleno sin
// función y agregaría un segundo lugar donde el orden puede diverger.
//
// Un ítem que no se puede interpretar se descarta con una advertencia y no hace
// fallar la respuesta: un dato mal sembrado no debe dejar el formulario sin
// catálogos, que es la diferencia entre un destino que falta y un formulario que
// no se puede usar.
func QueryCatalog(
	ctx context.Context,
	ddb awsddb.Client,
	tableName string,
	log *zap.Logger,
) (Response, error) {
	response := Response{
		Plans:        make([]program.CatalogOption, 0),
		Seasons:      make([]program.CatalogOption, 0),
		Destinations: make([]program.DestinationOption, 0),
	}

	// La paginación no es defensa contra un caso hipotético: DynamoDB corta una
	// página en 1 MB, y sin este bucle un catálogo que crezca truncaría la
	// respuesta en silencio, sin error que lo delate.
	var startKey map[string]ddbtypes.AttributeValue
	for {
		output, err := ddb.Query(ctx, &dynamodb.QueryInput{
			TableName:                aws.String(tableName),
			KeyConditionExpression:   aws.String("#pk = :pk"),
			ExpressionAttributeNames: map[string]string{"#pk": pkAttribute},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: domain.CatalogPK},
			},
			// Explícito y no por defecto: todo el argumento del relleno de ceros
			// de la clave depende de recibir los ítems en orden ascendente.
			ScanIndexForward:  aws.Bool(true),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return Response{}, apperr.Internal(
				fmt.Errorf("no se pudo consultar la tabla de catálogos: %w", err),
			)
		}

		for _, item := range output.Items {
			collectItem(&response, item, log)
		}

		if len(output.LastEvaluatedKey) == 0 {
			break
		}
		startKey = output.LastEvaluatedKey
	}

	return response, nil
}

// collectItem ubica un ítem de la partición CATALOG en la respuesta, o lo
// descarta con una advertencia si no se puede interpretar.
func collectItem(response *Response, item map[string]ddbtypes.AttributeValue, log *zap.Logger) {
	sortKey, ok := sortKeyOf(item)
	if !ok {
		log.Warn(itemDiscardedEvent, zap.String("reason", reasonMissingSortKey))
		return
	}

	if domain.IsSettingsSK(sortKey) {
		collectSettings(response, item, sortKey, log)
		return
	}

	var catalogItem domain.CatalogItem
	if err := attributevalue.UnmarshalMap(item, &catalogItem); err != nil {
		log.Warn(itemDiscardedEvent,
			zap.String("sk", sortKey),
			zap.String("reason", reasonUnmarshal),
			zap.Error(err),
		)
		return
	}

	// Requirement 16.4. Sin log: una opción retirada es el caso normal del
	// atributo, no una anomalía. Se omite de la respuesta pero sigue en la
	// tabla, y por eso un programa que ya referencia un destino desactivado
	// conserva dónde resolver su etiqueta.
	if !catalogItem.Active {
		return
	}

	scope, option, err := catalogItem.Option()
	if err != nil {
		log.Warn(itemDiscardedEvent,
			zap.String("sk", sortKey),
			zap.String("reason", reasonMalformedKey),
			zap.Error(err),
		)
		return
	}

	switch scope {
	case domain.ScopePlan:
		response.Plans = append(response.Plans, option)

	case domain.ScopeSeason:
		response.Seasons = append(response.Seasons, option)

	case domain.ScopeDestination:
		destination, err := catalogItem.Destination()
		if err != nil {
			log.Warn(itemDiscardedEvent,
				zap.String("sk", sortKey),
				zap.String("reason", reasonMissingBudget),
				zap.Error(err),
			)
			return
		}
		response.Destinations = append(response.Destinations, destination)

	default:
		// Inalcanzable hoy: Option ya rechaza los scopes que no están en
		// domain.Scopes(). Queda como advertencia para el día en que se agregue
		// un scope al dominio y se olvide darle lugar en la respuesta, que si no
		// sería una colección que desaparece sin dejar rastro en el log.
		log.Warn(itemDiscardedEvent,
			zap.String("sk", sortKey),
			zap.String("reason", reasonUnknownScope),
			zap.String("scope", string(scope)),
		)
	}
}

// collectSettings interpreta el ítem SETTINGS y lo deja en los parámetros de la
// respuesta.
//
// Un ítem ilegible se descarta como cualquier otro: la respuesta sale sin
// `margin` ni `scenarioOffsets` y el frontend aplica sus respaldos
// (Requirements 4.11 y 13.3), que es el mismo camino que cuando el ítem no
// existe. Fallar la petición dejaría al formulario sin catálogos por un dato
// que es opcional por contrato.
func collectSettings(
	response *Response,
	item map[string]ddbtypes.AttributeValue,
	sortKey string,
	log *zap.Logger,
) {
	var settingsItem domain.SettingsItem
	if err := attributevalue.UnmarshalMap(item, &settingsItem); err != nil {
		log.Warn(itemDiscardedEvent,
			zap.String("sk", sortKey),
			zap.String("reason", reasonUnmarshal),
			zap.Error(err),
		)
		return
	}

	response.Settings = settingsItem.Settings()
}

// sortKeyOf devuelve la clave de ordenamiento cruda del ítem.
//
// Se lee antes de deserializar porque es la `sk` la que decide en qué tipo se
// deserializa el ítem. Informa false cuando el atributo falta o no es una
// cadena, en vez de devolver la cadena vacía: un ítem sin `sk` no puede existir
// en esta tabla, y confundirlo con uno cuya clave es "" lo haría pasar por
// opción mal formada en el log en lugar de por lo que es.
func sortKeyOf(item map[string]ddbtypes.AttributeValue) (string, bool) {
	attribute, ok := item[skAttribute]
	if !ok {
		return "", false
	}

	value, ok := attribute.(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return "", false
	}

	return value.Value, true
}
