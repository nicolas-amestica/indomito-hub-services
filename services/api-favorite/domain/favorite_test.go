package domain

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// sampleContent devuelve un contenido con todos los campos poblados, incluidos
// los opcionales, para que la forma persistida quede fijada por completo.
func sampleContent() FavoriteContent {
	description := "Gira de cuarto medio"

	return FavoriteContent{
		Generals: program.ProgramGeneral{
			Name:          "Curso 4°A",
			Description:   &description,
			Plan:          program.CatalogRef{ID: "premium", Display: "Premium"},
			Season:        program.CatalogRef{ID: "alta", Display: "Alta"},
			Destination:   program.CatalogRef{ID: "BRF", Display: "Florianópolis", BudgetTemplateID: "BRF"},
			DepartureCity: "Santiago",
		},
		Schedule: program.ScheduleContent{
			StartDate:       "2026-01-05",
			EndDate:         "2026-01-12",
			TotalNights:     7,
			TotalPassengers: 40,
			FreePassengers:  2,
		},
		Pricing: program.PricingContent{
			UsdIncreaseCLP: 50,
			BrlIncreaseCLP: 10,
			UtilityRate:    20,
			RechargeRate:   5,
		},
		Crews: []program.CrewMember{{
			Name:       "Ana Pérez",
			DocumentID: "11111111-1",
			DailyPrice: 45000,
			Currency:   program.CurrencyCLP,
		}},
		Services: []program.ProgramService{{
			Name:       "Hotel",
			ChargeType: program.ChargePerPassengerNight,
			UnitPrice:  38.5,
			Currency:   program.CurrencyUSD,
		}},
	}
}

// TestNewFavoriteItem cubre la construcción del ítem: propaga los errores de la
// clave y exige un nombre con el que el usuario pueda identificar al favorito
// en el panel (Requirement 11.13).
func TestNewFavoriteItem(t *testing.T) {
	createdAt := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		name        string
		key         FavoriteKey
		favoriteNam string
		wantPK      string
		wantSK      string
		wantErr     bool
	}{
		{
			name:        "favorito completo",
			key:         FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
			favoriteNam: "Cuarto medio A",
			wantPK:      "USER#u1",
			wantSK:      "FAV#PROGRAMA#" + ulidA,
		},
		{
			name:        "sin usuario: no hay partición donde guardarlo",
			key:         FavoriteKey{Scope: ScopeProgram, ID: ulidA},
			favoriteNam: "Cuarto medio A",
			wantErr:     true,
		},
		{
			name:        "identificador que no es un ULID",
			key:         FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: "mi-favorito"},
			favoriteNam: "Cuarto medio A",
			wantErr:     true,
		},
		{
			name:        "scope desconocido",
			key:         FavoriteKey{UserID: "u1", Scope: Scope("cotizacion"), ID: ulidA},
			favoriteNam: "Cuarto medio A",
			wantErr:     true,
		},
		{
			name:        "nombre vacío",
			key:         FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
			favoriteNam: "",
			wantErr:     true,
		},
		{
			name:        "nombre en blanco",
			key:         FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
			favoriteNam: "   ",
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewFavoriteItem(tc.key, tc.favoriteNam, sampleContent(), createdAt, createdAt)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewFavoriteItem() error = nil, want error")
				}
				if !reflect.DeepEqual(got, FavoriteItem{}) {
					t.Errorf("NewFavoriteItem() = %+v, want vacío cuando hay error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewFavoriteItem() error = %v, want nil", err)
			}
			if got.PK != tc.wantPK {
				t.Errorf("PK = %q, want %q", got.PK, tc.wantPK)
			}
			if got.SK != tc.wantSK {
				t.Errorf("SK = %q, want %q", got.SK, tc.wantSK)
			}
			if got.Name != tc.favoriteNam {
				t.Errorf("Name = %q, want %q", got.Name, tc.favoriteNam)
			}
			if !got.CreatedAt.Equal(createdAt) || !got.UpdatedAt.Equal(createdAt) {
				t.Errorf("fechas = (%v, %v), want ambas %v", got.CreatedAt, got.UpdatedAt, createdAt)
			}
		})
	}
}

// TestNewFavoriteItemNormalizesToUTC comprueba que las fechas se guarden en UTC.
// El listado las devuelve tal como quedaron escritas, y una zona local filtrada
// hasta la tabla haría que dos favoritos creados en el mismo instante desde
// funciones distintas se vieran con horas distintas.
func TestNewFavoriteItemNormalizesToUTC(t *testing.T) {
	zone := time.FixedZone("CLT", -4*60*60)
	createdAt := time.Date(2026, 3, 14, 9, 30, 0, 0, zone)

	item, err := NewFavoriteItem(
		FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
		"Cuarto medio A", sampleContent(), createdAt, createdAt,
	)
	if err != nil {
		t.Fatalf("NewFavoriteItem() error = %v", err)
	}

	if item.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt.Location() = %v, want UTC", item.CreatedAt.Location())
	}
	if !item.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt = %v, want el mismo instante que %v", item.CreatedAt, createdAt)
	}
}

// TestFavoriteItemFavorite cubre la traducción a la respuesta: el identificador
// y el scope salen de la clave y no de atributos del ítem.
func TestFavoriteItemFavorite(t *testing.T) {
	createdAt := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Hour)

	item, err := NewFavoriteItem(
		FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
		"Cuarto medio A", sampleContent(), createdAt, updatedAt,
	)
	if err != nil {
		t.Fatalf("NewFavoriteItem() error = %v", err)
	}

	favorite, err := item.Favorite()
	if err != nil {
		t.Fatalf("Favorite() error = %v", err)
	}

	if favorite.ID != ulidA {
		t.Errorf("ID = %q, want %q", favorite.ID, ulidA)
	}
	if favorite.Scope != ScopeProgram {
		t.Errorf("Scope = %q, want %q", favorite.Scope, ScopeProgram)
	}
	if favorite.Name != "Cuarto medio A" {
		t.Errorf("Name = %q, want %q", favorite.Name, "Cuarto medio A")
	}
	if !reflect.DeepEqual(favorite.Content, sampleContent()) {
		t.Errorf("Content = %+v, want %+v", favorite.Content, sampleContent())
	}
	if !favorite.CreatedAt.Equal(createdAt) || !favorite.UpdatedAt.Equal(updatedAt) {
		t.Errorf("fechas = (%v, %v), want (%v, %v)",
			favorite.CreatedAt, favorite.UpdatedAt, createdAt, updatedAt)
	}
}

// TestFavoriteItemFavoriteMalformedKey comprueba que un ítem con la clave mal
// formada se reporte como error en vez de producir un favorito sin
// identificador. El listado lo descarta con una advertencia y sigue con el
// resto de la partición.
func TestFavoriteItemFavoriteMalformedKey(t *testing.T) {
	for _, sk := range []string{"", "FAV#PROGRAMA", "FAV#COTIZACION#" + ulidA, "FAV#PROGRAMA#x"} {
		t.Run(sk, func(t *testing.T) {
			item := FavoriteItem{PK: "USER#u1", SK: sk, Name: "x"}

			if _, err := item.Favorite(); err == nil {
				t.Errorf("Favorite() error = nil para la clave %q, want error", sk)
			}
		})
	}
}

// TestFavoriteItemKey comprueba que la clave del ítem sea la que se le pasa a
// GetItem, UpdateItem y DeleteItem.
func TestFavoriteItemKey(t *testing.T) {
	item, err := NewFavoriteItem(
		FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
		"Cuarto medio A", sampleContent(), time.Now(), time.Now(),
	)
	if err != nil {
		t.Fatalf("NewFavoriteItem() error = %v", err)
	}

	want := Key{PK: "USER#u1", SK: "FAV#PROGRAMA#" + ulidA}
	if got := item.Key(); got != want {
		t.Errorf("Key() = %+v, want %+v", got, want)
	}
}

// TestFavoriteItemAttributeNames fija los nombres de todo lo que se persiste,
// incluido el contenido anidado.
//
// Es la comprobación que sostiene la decisión de marshalizar el contenido con
// las etiquetas `dynamodbav` de libs/domain/program en vez de con un tipo de
// persistencia local (ver el Godoc de [FavoriteItem]). attributevalue no lee
// las etiquetas `json`, así que un campo nuevo en el programa que declare solo
// `json` se guardaría con el nombre de campo de Go y este test falla con el
// nombre exacto que sobra. Sin él, la etiqueta faltante llegaría a la tabla sin
// que nadie lo note.
func TestFavoriteItemAttributeNames(t *testing.T) {
	item, err := NewFavoriteItem(
		FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
		"Cuarto medio A", sampleContent(),
		time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC),
		time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewFavoriteItem() error = %v", err)
	}

	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	want := []string{
		"content.crews[].currency",
		"content.crews[].dailyPrice",
		"content.crews[].documentId",
		"content.crews[].name",
		"content.generals.departureCity",
		"content.generals.description",
		"content.generals.destination.budgetTemplateId",
		"content.generals.destination.display",
		"content.generals.destination.id",
		"content.generals.name",
		"content.generals.plan.display",
		"content.generals.plan.id",
		"content.generals.season.display",
		"content.generals.season.id",
		"content.pricing.brlIncreaseCLP",
		"content.pricing.rechargeRate",
		"content.pricing.usdIncreaseCLP",
		"content.pricing.utilityRate",
		"content.schedule.endDate",
		"content.schedule.freePassengers",
		"content.schedule.startDate",
		"content.schedule.totalNights",
		"content.schedule.totalPassengers",
		"content.services[].chargeType",
		"content.services[].currency",
		"content.services[].name",
		"content.services[].unitPrice",
		"createdAt",
		"name",
		"pk",
		"sk",
		"updatedAt",
	}

	if got := attributePaths(av); !reflect.DeepEqual(got, want) {
		t.Errorf("atributos persistidos:\n got = %v\nwant = %v", got, want)

		for _, extra := range difference(got, want) {
			t.Errorf("atributo inesperado: %q", extra)
		}
		for _, missing := range difference(want, got) {
			t.Errorf("atributo ausente: %q", missing)
		}
	}
}

// TestFavoriteItemRoundTrip comprueba que el contenido sobreviva el viaje a
// DynamoDB y de vuelta. Es lo que hace que cargar un favorito devuelva
// exactamente lo que se guardó (Requirement 11.16).
func TestFavoriteItemRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)

	item, err := NewFavoriteItem(
		FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
		"Cuarto medio A", sampleContent(), createdAt, updatedAt,
	)
	if err != nil {
		t.Fatalf("NewFavoriteItem() error = %v", err)
	}

	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	var roundTrip FavoriteItem
	if err := attributevalue.UnmarshalMap(av, &roundTrip); err != nil {
		t.Fatalf("UnmarshalMap() error = %v", err)
	}

	if roundTrip.PK != item.PK || roundTrip.SK != item.SK || roundTrip.Name != item.Name {
		t.Errorf("clave y nombre = %q/%q/%q, want %q/%q/%q",
			roundTrip.PK, roundTrip.SK, roundTrip.Name, item.PK, item.SK, item.Name)
	}
	if !reflect.DeepEqual(roundTrip.Content, item.Content) {
		t.Errorf("contenido = %+v, want %+v", roundTrip.Content, item.Content)
	}
	if !roundTrip.CreatedAt.Equal(createdAt) || !roundTrip.UpdatedAt.Equal(updatedAt) {
		t.Errorf("fechas = (%v, %v), want (%v, %v)",
			roundTrip.CreatedAt, roundTrip.UpdatedAt, createdAt, updatedAt)
	}
}

// TestFavoriteContentOmitsTotalsAndExchangeByType es el Requirement 11.17
// comprobado sobre los tipos y no sobre un ítem de ejemplo.
//
// La diferencia importa: un test que mirara los atributos de un ítem concreto
// pasaría igual si alguien agregara un campo de totales y lo dejara en cero,
// porque el `omitempty` lo escondería. Este recorre la definición de
// [FavoriteContent] y de todo lo que anida, así que falla en cuanto el campo
// existe, esté poblado o no.
//
// El motivo del requerimiento: al cargar un favorito los montos se recalculan
// con la tasa vigente (Requirement 11.10). Una tasa histórica guardada en el
// modelo alcanza para que alguien cotice con el dólar de hace tres meses.
func TestFavoriteContentOmitsTotalsAndExchangeByType(t *testing.T) {
	// Campos derivados del motor de cálculo y snapshot de tipo de cambio. Los
	// nombres son los de las interfaces del diseño; TotalNights y
	// TotalPassengers no están porque son decisiones del usuario, no derivados.
	forbiddenFields := map[string]string{
		"Totals":            "totales calculados (Requirement 11.17)",
		"ProgramTotals":     "totales calculados (Requirement 11.17)",
		"Exchange":          "snapshot de tipo de cambio (Requirement 11.17)",
		"ExchangeSnapshot":  "snapshot de tipo de cambio (Requirement 11.17)",
		"UsdToClp":          "tasa histórica (Requirements 11.10, 11.17)",
		"BrlToClp":          "tasa histórica (Requirements 11.10, 11.17)",
		"TotalDays":         "derivado del rango de fechas (Requirement 17.6)",
		"PayingPassengers":  "derivado de pasajeros y liberados (Requirement 17.6)",
		"BaseAmount":        "monto derivado del motor de cálculo",
		"AmountCLP":         "monto derivado del motor de cálculo",
		"SubtotalCLP":       "total derivado del motor de cálculo",
		"TotalCLP":          "total derivado del motor de cálculo",
		"PricePerPassenger": "total derivado del motor de cálculo",
	}

	forbiddenType := reflect.TypeOf(program.ExchangeSnapshot{})

	var walk func(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			typ = typ.Elem()
		}

		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true

		for i := range typ.NumField() {
			field := typ.Field(i)
			fieldPath := path + "." + field.Name

			if reason, forbidden := forbiddenFields[field.Name]; forbidden {
				t.Errorf("%s no puede existir: %s", fieldPath, reason)
			}

			if field.Type == forbiddenType {
				t.Errorf("%s es un %s: el favorito no guarda la tasa con la que se calculó",
					fieldPath, forbiddenType)
			}

			walk(t, field.Type, fieldPath, seen)
		}
	}

	walk(t, reflect.TypeOf(FavoriteContent{}), "FavoriteContent", map[reflect.Type]bool{})
}

// TestFavoriteContentPersistsRequiredGroups es la otra mitad del
// Requirement 11.16: los cinco grupos que el favorito sí debe conservar están
// presentes y llegan a la forma persistida.
func TestFavoriteContentPersistsRequiredGroups(t *testing.T) {
	typ := reflect.TypeOf(FavoriteContent{})

	for _, group := range []string{"Generals", "Schedule", "Pricing", "Crews", "Services"} {
		if _, present := typ.FieldByName(group); !present {
			t.Errorf("FavoriteContent no declara %s, que el Requirement 11.16 exige persistir", group)
		}
	}

	// totalNights es una decisión del usuario y no un derivado, así que a
	// diferencia de totalDays sí se guarda.
	if _, present := reflect.TypeOf(program.ScheduleContent{}).FieldByName("TotalNights"); !present {
		t.Error("ScheduleContent no declara TotalNights, que el favorito debe conservar")
	}
}

// TestContentFieldsDeclarePersistenceTags comprueba que todo campo del
// contenido declare `dynamodbav` además de `json`, y que los dos nombres
// coincidan.
//
// Es el guardarraíl de la decisión descrita en el Godoc de [FavoriteItem]: la
// forma guardada y la transmitida son la misma, y lo que las mantiene iguales
// son estas etiquetas. Un campo con solo `json` se persistiría con el nombre de
// campo de Go, y uno con nombres distintos en cada etiqueta produciría un
// favorito que se guarda con una forma y se devuelve con otra.
func TestContentFieldsDeclarePersistenceTags(t *testing.T) {
	var walk func(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(t *testing.T, typ reflect.Type, path string, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			typ = typ.Elem()
		}

		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true

		for i := range typ.NumField() {
			field := typ.Field(i)
			fieldPath := path + "." + field.Name

			jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			ddbName, _, _ := strings.Cut(field.Tag.Get("dynamodbav"), ",")

			switch {
			case jsonName == "":
				t.Errorf("%s no declara etiqueta `json`", fieldPath)
			case ddbName == "":
				t.Errorf("%s no declara etiqueta `dynamodbav`: se persistiría como %q",
					fieldPath, field.Name)
			case ddbName != jsonName:
				t.Errorf("%s se guarda como %q y se transmite como %q: los nombres deben coincidir",
					fieldPath, ddbName, jsonName)
			}

			walk(t, field.Type, fieldPath, seen)
		}
	}

	walk(t, reflect.TypeOf(FavoriteContent{}), "FavoriteContent", map[reflect.Type]bool{})
}

// attributePaths devuelve las rutas de los atributos hoja de un ítem,
// ordenadas, para comparar la forma persistida completa sin depender del
// recorrido de los mapas.
//
// Los elementos de una lista se colapsan en un solo segmento "[]": lo que se
// compara es la forma de las filas, no cuántas hay.
func attributePaths(av map[string]types.AttributeValue) []string {
	paths := make(map[string]struct{})

	var walk func(prefix string, value types.AttributeValue)
	walk = func(prefix string, value types.AttributeValue) {
		switch typed := value.(type) {
		case *types.AttributeValueMemberM:
			for name, nested := range typed.Value {
				walk(prefix+"."+name, nested)
			}
		case *types.AttributeValueMemberL:
			for _, element := range typed.Value {
				walk(prefix+"[]", element)
			}
		default:
			paths[prefix] = struct{}{}
		}
	}

	for name, value := range av {
		walk(name, value)
	}

	names := make([]string, 0, len(paths))
	for path := range paths {
		names = append(names, path)
	}
	sort.Strings(names)

	return names
}

// difference devuelve los elementos de a que no están en b, para que un fallo
// de la forma persistida señale el atributo exacto que sobra o falta.
func difference(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, value := range b {
		inB[value] = struct{}{}
	}

	var only []string
	for _, value := range a {
		if _, present := inB[value]; !present {
			only = append(only, value)
		}
	}

	return only
}
