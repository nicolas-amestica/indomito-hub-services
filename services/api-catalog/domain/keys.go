// Package domain contiene la representación en DynamoDB de la tabla
// `catalogos`: las opciones de catálogo, los parámetros de política de la
// empresa y el snapshot de respaldo de tasas de cambio.
//
// El paquete se ocupa solo de la forma persistida y de su traducción a los
// tipos de la respuesta, que viven en libs/domain/program. Ni consulta
// DynamoDB ni conoce el nombre de la tabla: eso es de los handlers y de
// functions/config.go.
//
// La tabla guarda tres colecciones bajo dos claves de partición:
//
//	pk = CATALOG   sk = <scope>#<order:04d>#<id>   opción de plan, temporada o destino
//	pk = CATALOG   sk = SETTINGS                   parámetros de margen y escenarios
//	pk = RATES     sk = LATEST                     último snapshot de tasas conocido
//
// Las opciones y los parámetros comparten la partición CATALOG a propósito:
// es lo que permite resolver GET /catalogos con un único Query
// (Requirement 16.2). El snapshot va aparte porque lo lee y escribe el otro
// endpoint del servicio, con otra frecuencia y otro ciclo de vida.
package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Claves fijas de la tabla `catalogos`.
const (
	// CatalogPK es la clave de partición que comparten las opciones de
	// catálogo y el ítem de parámetros de política. Es constante y conocida en
	// el código, que es lo que permite leerlas con un Query en vez de un Scan.
	CatalogPK = "CATALOG"

	// SettingsSK es la clave de ordenamiento del ítem de parámetros de
	// política, que vive en la partición CatalogPK.
	//
	// No contiene [keySeparator], y por eso no puede colisionar con la sk de
	// una opción de catálogo: el handler separa los dos casos por la forma de
	// la clave, sin necesitar un atributo de tipo.
	SettingsSK = "SETTINGS"

	// RatesPK es la clave de partición del snapshot de respaldo de tasas
	// (Requirement 14.4). Está separada de CatalogPK para que cada consulta de
	// catálogos no arrastre un ítem que no necesita.
	RatesPK = "RATES"

	// RatesLatestSK es la clave de ordenamiento del único snapshot de tasas.
	// No hay historial: el ítem se sobrescribe en cada consulta exitosa a la
	// fuente externa.
	RatesLatestSK = "LATEST"
)

// keySeparator separa los tres componentes de la clave de ordenamiento de una
// opción de catálogo.
const keySeparator = "#"

// Límites del orden de presentación de una opción de catálogo.
//
// El orden viaja embebido en la sk con relleno de ceros a [orderWidth]
// dígitos, de modo que el orden lexicográfico con el que DynamoDB entrega los
// ítems coincida con el numérico y el handler no tenga que ordenar nada. Sin
// el relleno, "10" precedería a "2".
//
// El relleno es también lo que fija los límites: cuatro dígitos no admiten un
// orden mayor que 9999, y un orden negativo produciría un signo donde debería
// haber un dígito ("-005"), rompiendo justamente la correspondencia entre los
// dos órdenes. Los dos casos se rechazan al construir la clave en vez de
// dejarlos llegar a la tabla.
const (
	// OrderMin es el orden de presentación más bajo admitido.
	OrderMin = 0

	// OrderMax es el orden de presentación más alto que caben en el relleno.
	OrderMax = 9999

	// orderWidth es la cantidad de dígitos del relleno de ceros.
	orderWidth = 4
)

// Errores de construcción y de parseo de claves. Se exponen para que los
// handlers puedan distinguir un ítem con la clave mal formada —que se descarta
// con una advertencia en el log— de un fallo de DynamoDB.
var (
	// ErrInvalidScope indica un scope que no está entre los de [Scopes].
	ErrInvalidScope = errors.New("scope de catálogo desconocido")

	// ErrOrderOutOfRange indica un orden de presentación fuera de
	// [OrderMin, OrderMax].
	ErrOrderOutOfRange = errors.New("orden de presentación fuera de rango")

	// ErrEmptyID indica un identificador de opción vacío.
	ErrEmptyID = errors.New("identificador de opción vacío")

	// ErrMalformedSK indica una clave de ordenamiento que no respeta el
	// formato <scope>#<order:04d>#<id>.
	ErrMalformedSK = errors.New("clave de ordenamiento mal formada")
)

// Scope identifica la colección a la que pertenece una opción de catálogo. Es
// el primer componente de la clave de ordenamiento, y es lo que permite al
// handler repartir en planes, temporadas y destinos los ítems que devuelve un
// único Query sobre [CatalogPK].
type Scope string

// Scopes de las tres colecciones de opciones que sirve GET /catalogos.
const (
	// ScopePlan agrupa las opciones de plan del programa.
	ScopePlan Scope = "PLAN"

	// ScopeSeason agrupa las opciones de temporada.
	ScopeSeason Scope = "SEASON"

	// ScopeDestination agrupa las opciones de destino, las únicas que declaran
	// plantilla de presupuesto (Requirement 16.5).
	ScopeDestination Scope = "DESTINATION"
)

// Scopes devuelve los scopes conocidos, en el orden en que la respuesta
// presenta sus colecciones.
func Scopes() []Scope {
	return []Scope{ScopePlan, ScopeSeason, ScopeDestination}
}

// Valid informa si s es uno de los scopes conocidos. Un scope desconocido en
// la tabla es un dato mal sembrado, no una colección nueva: el handler lo
// descarta en vez de inventarle un lugar en la respuesta.
func (s Scope) Valid() bool {
	switch s {
	case ScopePlan, ScopeSeason, ScopeDestination:
		return true
	default:
		return false
	}
}

// Prefix devuelve el prefijo de clave de ordenamiento de las opciones del
// scope ("PLAN#"), útil para una condición begins_with sobre la sk.
func (s Scope) Prefix() string {
	return string(s) + keySeparator
}

// Key es la clave primaria completa de un ítem de la tabla `catalogos`. Sus
// etiquetas dynamodbav la vuelven marshalizable con attributevalue.MarshalMap
// para el campo Key de GetItem, UpdateItem y DeleteItem.
type Key struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
}

// SettingsKey devuelve la clave del ítem de parámetros de política.
func SettingsKey() Key {
	return Key{PK: CatalogPK, SK: SettingsSK}
}

// RatesSnapshotKey devuelve la clave del snapshot de respaldo de tasas. Es
// completa y fija, así que el snapshot se lee con GetItem y se sobrescribe con
// PutItem, sin consulta que resolver.
func RatesSnapshotKey() Key {
	return Key{PK: RatesPK, SK: RatesLatestSK}
}

// IsSettingsSK informa si sk es la clave del ítem de parámetros de política.
//
// Es la primera pregunta que hace el handler de catálogos sobre cada ítem del
// Query: si es SETTINGS va a los parámetros de la respuesta, y si no, se
// interpreta como opción con [ParseCatalogItemSK].
func IsSettingsSK(sk string) bool {
	return sk == SettingsSK
}

// CatalogItemKey son los tres componentes de la clave de ordenamiento de una
// opción de catálogo: su scope, su orden de presentación y su identificador.
type CatalogItemKey struct {
	// Scope es la colección a la que pertenece la opción.
	Scope Scope

	// Order es el orden de presentación, entre [OrderMin] y [OrderMax].
	Order int

	// ID es el identificador de la opción, tal como lo consume el frontend.
	ID string
}

// SK construye la clave de ordenamiento de la opción, con el orden rellenado
// con ceros a cuatro dígitos.
//
// Devuelve error en vez de una clave a medio armar: una sk mal formada no
// rompe la escritura, se guarda sin ruido y reaparece después como una opción
// que el handler no puede interpretar o, peor, en el lugar equivocado del
// orden de presentación.
func (k CatalogItemKey) SK() (string, error) {
	if !k.Scope.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidScope, string(k.Scope))
	}

	if k.Order < OrderMin || k.Order > OrderMax {
		return "", fmt.Errorf("%w: %d no está en [%d, %d]", ErrOrderOutOfRange, k.Order, OrderMin, OrderMax)
	}

	if k.ID == "" {
		return "", ErrEmptyID
	}

	return fmt.Sprintf("%s%s%0*d%s%s",
		k.Scope, keySeparator, orderWidth, k.Order, keySeparator, k.ID,
	), nil
}

// Key devuelve la clave primaria completa de la opción.
func (k CatalogItemKey) Key() (Key, error) {
	sk, err := k.SK()
	if err != nil {
		return Key{}, err
	}

	return Key{PK: CatalogPK, SK: sk}, nil
}

// ParseCatalogItemSK descompone la clave de ordenamiento de una opción de
// catálogo en sus tres componentes.
//
// Es la operación inversa de [CatalogItemKey.SK] y la que devuelve al handler
// el identificador y el orden que la respuesta expone (Requirement 16.3), sin
// que la tabla los guarde como atributos aparte.
//
// El identificador puede contener el separador: el corte se hace en las dos
// primeras apariciones y el resto de la clave es el identificador completo.
func ParseCatalogItemSK(sk string) (CatalogItemKey, error) {
	parts := strings.SplitN(sk, keySeparator, 3)
	if len(parts) != 3 {
		return CatalogItemKey{}, fmt.Errorf(
			"%w: %q no tiene los tres componentes de <scope>%s<order>%s<id>",
			ErrMalformedSK, sk, keySeparator, keySeparator,
		)
	}

	scope := Scope(parts[0])
	if !scope.Valid() {
		return CatalogItemKey{}, fmt.Errorf("%w: %q en la clave %q", ErrInvalidScope, parts[0], sk)
	}

	order, err := parsePaddedOrder(parts[1])
	if err != nil {
		return CatalogItemKey{}, fmt.Errorf("%w en la clave %q", err, sk)
	}

	if parts[2] == "" {
		return CatalogItemKey{}, fmt.Errorf("%w: la clave %q no lo declara", ErrEmptyID, sk)
	}

	return CatalogItemKey{Scope: scope, Order: order, ID: parts[2]}, nil
}

// parsePaddedOrder interpreta el componente de orden de una clave de
// ordenamiento.
//
// Exige exactamente [orderWidth] dígitos decimales, sin signo: el relleno de
// ceros no es cosmética, es lo que hace que el orden lexicográfico coincida
// con el numérico. Una clave con "5" o con "-005" donde debería haber "0005"
// está mal formada, aunque strconv.Atoi la lea sin problemas, porque ya quedó
// ubicada en el lugar equivocado del orden que DynamoDB entrega.
func parsePaddedOrder(padded string) (int, error) {
	if len(padded) != orderWidth {
		return 0, fmt.Errorf(
			"%w: el orden %q no tiene %d dígitos de relleno",
			ErrMalformedSK, padded, orderWidth,
		)
	}

	for _, r := range padded {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: el orden %q no es decimal sin signo", ErrMalformedSK, padded)
		}
	}

	// Cuatro dígitos decimales sin signo caen siempre en [OrderMin, OrderMax],
	// así que a esta altura no hace falta volver a comprobar el rango.
	order, err := strconv.Atoi(padded)
	if err != nil {
		return 0, fmt.Errorf("%w: el orden %q no se pudo interpretar", ErrMalformedSK, padded)
	}

	return order, nil
}
