// Package awsddb expone la superficie minima de DynamoDB que los endpoints
// del backend necesitan: GetItem, Query, PutItem, UpdateItem y DeleteItem.
//
// [Client] es la unica interfaz que el diseno introduce, y existe porque hay
// dos implementaciones reales: el cliente del SDK de AWS en produccion y un
// doble en los tests, que no tocan AWS. Fuera de este caso no se crean
// interfaces, segun docs/standards/backend/go-conventions.md.
//
// Scan no forma parte de la superficie, y no es un olvido: el proyecto
// prohibe el operador Scan en cualquier ambiente
// (docs/standards/global/architecture-principles.md). Toda consulta se
// resuelve con Query sobre una clave de particion conocida o con GetItem
// sobre una clave completa, apoyandose en un GSI/LSI cuando el patron de
// acceso lo requiera. Al no declarar Scan aca, un endpoint que quisiera
// recorrer la tabla completa no compila contra Client: tendria que pedir el
// cliente concreto del SDK, lo que vuelve la intencion visible en revision
// en vez de esconderla en una linea de consulta.
package awsddb

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Client es la superficie de DynamoDB que los servicios reciben como
// dependencia. Las firmas replican exactamente las del SDK, de modo que
// *dynamodb.Client la satisface sin adaptador intermedio y sin perder
// funcionalidad de las operaciones que si estan expuestas: expresiones de
// condicion, proyecciones, ReturnValues y paginacion siguen disponibles a
// traves de los Input del SDK.
//
// Las funciones que la reciben declaran ctx como primer argumento y le pasan
// el contexto de la invocacion, para que el timeout de la Lambda cancele la
// llamada a DynamoDB en vez de esperarla.
type Client interface {
	// GetItem lee un item por su clave completa (pk y sk).
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)

	// Query lee items de una clave de particion conocida, con condicion
	// opcional sobre la clave de ordenamiento. Es la unica forma de lectura
	// multiple disponible: no hay Scan.
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)

	// PutItem escribe un item completo, creandolo o sobrescribiendolo.
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)

	// UpdateItem modifica atributos de un item existente identificado por su
	// clave completa.
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)

	// DeleteItem elimina un item por su clave completa.
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// Asercion de compilacion: garantiza que el cliente del SDK siga
// satisfaciendo [Client]. Si una version futura del SDK cambia una firma, el
// modulo no compila en vez de fallar en runtime dentro de la Lambda.
var _ Client = (*dynamodb.Client)(nil)

// New construye el cliente de DynamoDB del SDK a partir de la configuracion
// de AWS ya cargada por bootstrap. Devuelve el tipo concreto, no la interfaz:
// quien lo recibe declara [Client] en su firma y obtiene la restriccion de
// superficie ahi, mientras el llamador conserva el cliente completo si
// necesita pasarle opciones del SDK.
//
// Se llama una vez durante el arranque del servicio, no por invocacion: el
// cliente es seguro para uso concurrente y reutilizar la conexion entre
// invocaciones de una misma Lambda evita rehacer el handshake TLS.
func New(cfg aws.Config, optFns ...func(*dynamodb.Options)) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg, optFns...)
}
