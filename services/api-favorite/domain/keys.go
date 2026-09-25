// Package domain contiene la representación en DynamoDB de la tabla
// `favoritos`: el contenido guardado de un favorito y la construcción y el
// parseo de sus claves.
//
// El paquete se ocupa solo de la forma persistida y de su traducción a los
// tipos de la respuesta. Ni consulta DynamoDB ni conoce el nombre de la tabla:
// eso es de los handlers y de functions/config.go.
//
// La tabla guarda una sola colección:
//
//	pk = USER#<userId>   sk = FAV#PROGRAMA#<ulid>   favorito de programa
//
// La partición es el usuario, y eso es exactamente el aislamiento que protege
// el Requirement 19: un usuario solo alcanza los ítems de su propia partición,
// y el userId viene del contexto del authorizer y nunca del cuerpo
// (Requirement 19.8, libs/lambdautil.UserIDFromContext).
//
// El scope viaja en la clave de ordenamiento para que el panel de favoritos de
// otra feature pueda convivir en la misma tabla sin colisionar. Hoy hay un
// único scope, [ScopeProgram].
//
// # Por qué el identificador basta como clave de ordenamiento
//
// Los primeros 48 bits de un ULID son el instante de creación en milisegundos,
// y su codificación base32 conserva el orden lexicográfico. Ordenar por sk
// descendente *es* ordenar por fecha de creación descendente, así que el
// listado del Requirement 11.5 se resuelve con un Query sobre la partición del
// usuario, sin índice adicional y sin que el handler ordene nada.
//
// De ahí que la clave no lleve createdAt: sería un componente redundante con
// el identificador que puede quedar inconsistente con él, y el ítem ya guarda
// la fecha como atributo para exponerla en la respuesta.
package domain

import (
	"errors"
	"fmt"
	"strings"

	"github.com/oklog/ulid/v2"
)

// Componentes fijos de las claves de la tabla `favoritos`.
const (
	// UserPKPrefix precede al identificador del usuario en la clave de
	// partición.
	UserPKPrefix = "USER#"

	// FavoritePrefix es el primer componente de la clave de ordenamiento de
	// todo favorito, cualquiera sea su scope.
	FavoritePrefix  = "FAV"
	QuotationPrefix = "QUOTE"

	// keySeparator separa los componentes de la clave de ordenamiento.
	keySeparator = "#"
)

// Errores de construcción y de parseo de claves. Se exponen para que los
// handlers puedan distinguir una clave mal formada —un identificador que llega
// por el path y no es un ULID, o un ítem de la tabla que se descarta con una
// advertencia en el log— de un fallo de DynamoDB.
var (
	// ErrEmptyUserID indica un identificador de usuario vacío o en blanco.
	ErrEmptyUserID = errors.New("identificador de usuario vacío")

	// ErrInvalidScope indica un scope que no está entre los de [Scopes].
	ErrInvalidScope = errors.New("scope de favorito desconocido")

	// ErrInvalidFavoriteID indica un identificador de favorito que no es un
	// ULID válido.
	ErrInvalidFavoriteID = errors.New("identificador de favorito inválido")

	// ErrMalformedSK indica una clave de ordenamiento que no respeta el
	// formato FAV#<SCOPE>#<ulid>.
	ErrMalformedSK = errors.New("clave de ordenamiento mal formada")
)

// Scope identifica la colección de favoritos a la que pertenece un ítem. Es el
// valor que la respuesta expone en el campo `scope` y, en mayúsculas, el
// segundo componente de la clave de ordenamiento.
type Scope string

// Scopes de las colecciones de favoritos que admite la tabla.
const (
	// ScopeProgram agrupa los favoritos del formulario de programa, el único
	// scope de esta feature.
	ScopeProgram Scope = "programa"
	// ScopeQuotation identifica las cotizaciones persistidas en la tabla programas.
	ScopeQuotation Scope = "cotizacion"
)

// scopeSKSegments asocia cada scope conocido con su segmento de clave de
// ordenamiento.
//
// Los dos valores se declaran por separado en vez de derivar uno del otro con
// strings.ToUpper porque cumplen contratos distintos: el del scope es el que
// viaja en el JSON de la respuesta, en español y en minúsculas como el resto
// de la API, y el del segmento es el que queda escrito en las claves de la
// tabla, donde ya no se puede cambiar sin migrar los ítems existentes. Un
// scope nuevo tiene que declarar ambos, y eso obliga a decidir el segmento en
// lugar de heredarlo de cómo se escribió el otro.
var scopeSKSegments = map[Scope]string{
	ScopeProgram:   "PROGRAMA",
	ScopeQuotation: "COTIZACION",
}

var scopeSKPrefixes = map[Scope]string{
	ScopeProgram:   FavoritePrefix,
	ScopeQuotation: QuotationPrefix,
}

// Scopes devuelve los scopes conocidos.
func Scopes() []Scope {
	return []Scope{ScopeProgram, ScopeQuotation}
}

// Valid informa si s es uno de los scopes conocidos. Un scope desconocido es un
// dato mal formado, no una colección nueva: el handler lo rechaza en vez de
// inventarle un lugar en la tabla.
func (s Scope) Valid() bool {
	_, known := scopeSKSegments[s]

	return known
}

// SKPrefix devuelve el prefijo de clave de ordenamiento del scope
// ("FAV#PROGRAMA#").
//
// Es la condición begins_with del Query que lista los favoritos de un usuario
// en un scope (Requirement 11.5). Devuelve cadena vacía para un scope
// desconocido, que ningún ítem tiene como prefijo.
func (s Scope) SKPrefix() string {
	segment, known := scopeSKSegments[s]
	if !known {
		return ""
	}

	return scopeSKPrefixes[s] + keySeparator + segment + keySeparator
}

// NewFavoriteID genera el identificador de un favorito nuevo.
//
// Es un ULID, y esa elección es la que sostiene el orden del listado: ver la
// nota del paquete sobre por qué el identificador basta como clave de
// ordenamiento.
func NewFavoriteID() string {
	return ulid.Make().String()
}

// ValidateFavoriteID comprueba que id sea un ULID válido.
//
// Los handlers de actualizar y eliminar lo aplican al parámetro de path antes
// de armar la clave. No es una formalidad: un identificador arbitrario
// aceptado como clave de ordenamiento produciría un ítem que el listado
// entrega en el lugar equivocado del orden por fecha, y ese orden es lo único
// que hace innecesario un índice.
func ValidateFavoriteID(id string) error {
	if _, err := ulid.Parse(id); err != nil {
		return fmt.Errorf("%w: %q no es un ULID", ErrInvalidFavoriteID, id)
	}

	return nil
}

// UserPK devuelve la clave de partición del usuario.
//
// El identificador se concatena tal cual detrás de un prefijo fijo y la clave
// nunca se descompone, así que un `#` dentro del identificador no genera
// ambigüedad: dos usuarios distintos producen siempre particiones distintas.
// Lo único que se rechaza es un identificador vacío, que colapsaría a todos
// los usuarios sin identidad en una misma partición compartida.
func UserPK(userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", ErrEmptyUserID
	}

	return UserPKPrefix + userID, nil
}

// Key es la clave primaria completa de un ítem de la tabla `favoritos`. Sus
// etiquetas dynamodbav la vuelven marshalizable con attributevalue.MarshalMap
// para el campo Key de GetItem, UpdateItem y DeleteItem.
type Key struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
}

// FavoriteKey son los componentes de la clave primaria de un favorito: el
// usuario dueño, el scope de la colección y el identificador del favorito.
type FavoriteKey struct {
	// UserID es el identificador del usuario, tomado del contexto del
	// authorizer (Requirement 19.8).
	UserID string

	// Scope es la colección a la que pertenece el favorito.
	Scope Scope

	// ID es el identificador del favorito, un ULID.
	ID string
}

// PK devuelve la clave de partición del favorito.
func (k FavoriteKey) PK() (string, error) {
	return UserPK(k.UserID)
}

// SK devuelve la clave de ordenamiento del favorito.
//
// Devuelve error en vez de una clave a medio armar: una sk mal formada no rompe
// la escritura, se guarda sin ruido y reaparece después como un favorito que el
// listado no puede interpretar o que aparece fuera de orden.
func (k FavoriteKey) SK() (string, error) {
	if !k.Scope.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidScope, string(k.Scope))
	}

	if err := ValidateFavoriteID(k.ID); err != nil {
		return "", err
	}

	return k.Scope.SKPrefix() + k.ID, nil
}

// Key devuelve la clave primaria completa del favorito.
func (k FavoriteKey) Key() (Key, error) {
	pk, err := k.PK()
	if err != nil {
		return Key{}, err
	}

	sk, err := k.SK()
	if err != nil {
		return Key{}, err
	}

	return Key{PK: pk, SK: sk}, nil
}

// ParseFavoriteSK descompone la clave de ordenamiento de un favorito en su
// scope y su identificador.
//
// Es la operación inversa de [FavoriteKey.SK] y la que devuelve al handler el
// identificador que la respuesta expone, sin que la tabla lo guarde como
// atributo aparte.
//
// El usuario no sale de acá: es la clave de partición, que el handler ya
// conoce porque la usó para consultar. Un ítem no puede informar a qué usuario
// pertenece de forma distinta a la partición donde está.
func ParseFavoriteSK(sk string) (Scope, string, error) {
	parts := strings.SplitN(sk, keySeparator, 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf(
			"%w: %q no tiene los tres componentes de %s%s<scope>%s<id>",
			ErrMalformedSK, sk, FavoritePrefix, keySeparator, keySeparator,
		)
	}

	if parts[0] != FavoritePrefix && parts[0] != QuotationPrefix {
		return "", "", fmt.Errorf(
			"%w: %q no empieza con %q", ErrMalformedSK, sk, FavoritePrefix,
		)
	}

	scope, err := scopeFromSKSegment(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("%w en la clave %q", err, sk)
	}

	if err := ValidateFavoriteID(parts[2]); err != nil {
		return "", "", fmt.Errorf("%w en la clave %q", err, sk)
	}
	if scopeSKPrefixes[scope] != parts[0] {
		return "", "", fmt.Errorf("%w: prefijo %q incompatible con scope %q", ErrMalformedSK, parts[0], scope)
	}

	return scope, parts[2], nil
}

// scopeFromSKSegment traduce el segmento de clave de ordenamiento al scope que
// la respuesta expone.
func scopeFromSKSegment(segment string) (Scope, error) {
	for scope, knownSegment := range scopeSKSegments {
		if knownSegment == segment {
			return scope, nil
		}
	}

	return "", fmt.Errorf("%w: %q", ErrInvalidScope, segment)
}
