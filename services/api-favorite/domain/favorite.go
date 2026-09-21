package domain

import (
	"fmt"
	"strings"
	"time"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// FavoriteContent es el contenido guardado de un favorito: los datos
// generales, las fechas y cantidades, los parámetros de precio, la lista de
// tripulantes y la lista de servicios del programa (Requirement 11.16).
//
// Compone los tipos de libs/domain/program en vez de redeclararlos, que es lo
// que mantiene una sola definición del contenido del programa para los tres
// servicios (Requirement 17.5).
//
// # Lo que este tipo no puede expresar
//
// No hay campo de totales ni de snapshot de tipo de cambio, y esa ausencia es
// el Requirement 11.17 implementado por tipo y no por convención. Los tipos que
// compone tampoco los tienen: program.ScheduleContent omite TotalDays y
// PayingPassengers, y program.PricingContent omite ExchangeSnapshot.
//
// La diferencia con omitirlos al serializar es que acá el defecto es
// imposible, no improbable. Al cargar un favorito los montos se recalculan con
// la tasa vigente (Requirement 11.10); si el ítem guardara la tasa del día en
// que se creó, existiría una tasa histórica en el modelo y alcanzaría con que
// alguien la leyera por descuido para cotizar con un dólar de hace tres meses.
// No guardarla deja al recálculo como el único camino posible.
//
// TotalNights sí se guarda, porque no es un derivado: es una decisión del
// usuario que el formulario deja de precargar en cuanto la toca
// (Requirement 3.12).
type FavoriteContent struct {
	Generals program.ProgramGeneral   `dynamodbav:"generals" json:"generals" validate:"required"`
	Schedule program.ScheduleContent  `dynamodbav:"schedule" json:"schedule" validate:"required"`
	Pricing  program.PricingContent   `dynamodbav:"pricing"  json:"pricing"  validate:"required"`
	Crews    []program.CrewMember     `dynamodbav:"crews"    json:"crews"    validate:"max=20,dive"`
	Services []program.ProgramService `dynamodbav:"services" json:"services" validate:"max=100,dive"`
}

// Favorite es un favorito tal como lo expone la API: el contenido guardado más
// su identidad y sus fechas.
//
// No es el tipo que se persiste —eso es [FavoriteItem]— porque el
// identificador y el scope no son atributos del ítem sino componentes de su
// clave de ordenamiento, y el usuario dueño es su clave de partición.
type Favorite struct {
	// ID es el identificador del favorito, derivado de la clave de
	// ordenamiento con [ParseFavoriteSK].
	ID string `json:"id"`

	// Name es el nombre con el que el usuario guardó el favorito
	// (Requirement 11.13).
	Name string `json:"name"`

	// Scope es la colección a la que pertenece, derivada también de la clave.
	Scope Scope `json:"scope"`

	// Content es el contenido del programa guardado.
	Content FavoriteContent `json:"content"`

	// CreatedAt y UpdatedAt son los instantes de creación y de última
	// modificación. encoding/json los emite como cadenas ISO 8601, que es la
	// forma en que el frontend los recibe.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FavoriteItem es la representación en DynamoDB de un favorito:
// pk = USER#<userId>, sk = FAV#PROGRAMA#<ulid>.
//
// El identificador, el scope y el usuario no se guardan como atributos: se
// derivan de la clave. Guardarlos aparte sería un dato redundante que puede
// quedar inconsistente con la clave, y en el caso del usuario sería además
// engañoso, porque quien decide de quién es un favorito es la partición donde
// está y no un atributo que alguien pueda escribir distinto.
//
// # Por qué el contenido se marshaliza con las etiquetas del tipo compartido
//
// attributevalue no lee las etiquetas `json`: solo `dynamodbav`, salvo que se
// le pase EncoderOptions.TagKey. Como [FavoriteContent] compone tipos de
// libs/domain/program y esos tipos se persisten enteros, hacía falta decidir de
// dónde salen los nombres de atributo del ítem. Se les agregaron etiquetas
// `dynamodbav` a los tipos de libs/domain/program, con los mismos nombres que
// ya tenían en `json`.
//
// Es la decisión contraria a la que tomó services/api-catalog/domain con
// MarginAttributes, y la diferencia está en la forma de lo que se guarda. Allá
// el ítem persistido no se parece al de la respuesta: la opción de catálogo
// deriva su id y su orden de la clave, así que el tipo de persistencia existía
// de todos modos y las etiquetas viajaron dentro de uno que ya era necesario.
// Acá el contenido del favorito se guarda y se devuelve con la misma forma: un
// tipo de persistencia local sería un gemelo campo por campo de siete structs,
// cuya única diferencia sería el nombre de la etiqueta.
//
// Ese gemelo es peor por el modo en que falla. Agregar un campo al programa y
// olvidar reflejarlo en la copia no rompe nada visible: el favorito se guarda
// sin ese campo y el usuario descubre la pérdida al cargarlo. Una etiqueta que
// falta, en cambio, la ve el revisor en la línea de al lado y la detecta
// TestFavoriteItemAttributeNames, que fija los nombres persistidos de todo el
// contenido anidado. La tercera opción —centralizar el marshalling con
// TagKey: "json"— se descartó porque deja la garantía en manos de cada
// llamador: un attributevalue.MarshalMap directo escribiría nombres distintos
// en silencio.
//
// El costo de la elección es que las etiquetas de persistencia viven en un
// paquete de contrato compartido. Se acota a metadatos: libs/domain/program no
// importa el SDK de AWS ni sabe que existe DynamoDB, y los nombres coinciden
// con los de `json` a propósito, para que la forma guardada y la forma
// transmitida no puedan divergir.
type FavoriteItem struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`

	// Name es el nombre que el usuario le dio al favorito.
	Name string `dynamodbav:"name"`

	// Content es el contenido del programa, sin totales ni snapshot de tipo de
	// cambio: ver [FavoriteContent].
	Content FavoriteContent `dynamodbav:"content"`

	// CreatedAt es el instante de creación. Es redundante con el ULID de la
	// clave, que lo lleva embebido, pero se guarda igual porque la respuesta lo
	// expone y decodificar el ULID en cada lectura sería trabajo para recuperar
	// un dato que cabe en un atributo.
	//
	// attributevalue lo guarda como cadena RFC3339Nano.
	CreatedAt time.Time `dynamodbav:"createdAt"`

	// UpdatedAt es el instante de la última modificación. En un favorito recién
	// creado coincide con CreatedAt.
	UpdatedAt time.Time `dynamodbav:"updatedAt"`
}

// NewFavoriteItem arma el ítem de un favorito a partir de los componentes de su
// clave, su nombre, su contenido y sus fechas.
//
// Exige un nombre no vacío: es lo que el usuario ve en el panel de favoritos
// (Requirement 11.13), y un favorito sin nombre queda en la lista como una fila
// que no se puede identificar ni distinguir de otra igual.
//
// No valida el contenido. Los límites numéricos y los campos obligatorios del
// programa son cosa del validador de la petición, que traduce cada
// incumplimiento al código de error que corresponde; acá llegaría demasiado
// tarde para decir cuál campo estuvo mal.
func NewFavoriteItem(
	key FavoriteKey,
	name string,
	content FavoriteContent,
	createdAt time.Time,
	updatedAt time.Time,
) (FavoriteItem, error) {
	itemKey, err := key.Key()
	if err != nil {
		return FavoriteItem{}, err
	}

	if strings.TrimSpace(name) == "" {
		return FavoriteItem{}, fmt.Errorf("el favorito %q no declara nombre", itemKey.SK)
	}

	return FavoriteItem{
		PK:        itemKey.PK,
		SK:        itemKey.SK,
		Name:      name,
		Content:   content,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	}, nil
}

// Key devuelve la clave primaria del ítem, para el campo Key de GetItem,
// UpdateItem y DeleteItem.
func (i FavoriteItem) Key() Key {
	return Key{PK: i.PK, SK: i.SK}
}

// Favorite traduce el ítem al favorito de la respuesta, con el identificador y
// el scope derivados de la clave de ordenamiento.
//
// Falla si la clave está mal formada. El listado descarta esos ítems con una
// advertencia en el log en vez de responder con error: un ítem mal sembrado en
// la partición de un usuario no es razón para dejarlo sin sus otros favoritos.
func (i FavoriteItem) Favorite() (Favorite, error) {
	scope, id, err := ParseFavoriteSK(i.SK)
	if err != nil {
		return Favorite{}, err
	}

	return Favorite{
		ID:        id,
		Name:      i.Name,
		Scope:     scope,
		Content:   i.Content,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}, nil
}
