package domain

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// ulidA y ulidB son dos identificadores válidos con instantes de creación
// distintos: el primer componente de un ULID es el tiempo en milisegundos, así
// que ulidA es anterior a ulidB y su orden lexicográfico lo refleja.
const (
	ulidA = "01JBQ8V4Z0000000000000000A"
	ulidB = "01JBQ8V4Z1000000000000000B"
)

// TestUserPK cubre la clave de partición, que es lo que aísla a los usuarios
// entre sí (Requirement 19.8).
func TestUserPK(t *testing.T) {
	tests := []struct {
		name    string
		userID  string
		wantPK  string
		wantErr error
	}{
		{
			name:   "identificador de usuario habitual",
			userID: "01JDEV0000000000000000DEV0",
			wantPK: "USER#01JDEV0000000000000000DEV0",
		},
		{
			name:   "un separador dentro del identificador no se escapa ni rompe la clave",
			userID: "tenant#42",
			wantPK: "USER#tenant#42",
		},
		{
			name:    "identificador vacío",
			userID:  "",
			wantErr: ErrEmptyUserID,
		},
		{
			name:    "identificador en blanco",
			userID:  "   ",
			wantErr: ErrEmptyUserID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UserPK(tc.userID)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("UserPK() error = %v, want %v", err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("UserPK() = %q, want vacío cuando hay error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("UserPK() error = %v, want nil", err)
			}
			if got != tc.wantPK {
				t.Errorf("UserPK() = %q, want %q", got, tc.wantPK)
			}
		})
	}
}

// TestUserPKIsolatesUsers es la invariante del Requirement 19: dos usuarios
// distintos no pueden compartir partición, ni siquiera cuando uno de los
// identificadores contiene el separador de claves.
func TestUserPKIsolatesUsers(t *testing.T) {
	userIDs := []string{"alice", "bob", "tenant#42", "tenant", "42"}

	seen := make(map[string]string, len(userIDs))
	for _, userID := range userIDs {
		pk, err := UserPK(userID)
		if err != nil {
			t.Fatalf("UserPK(%q) error = %v", userID, err)
		}

		if other, collision := seen[pk]; collision {
			t.Fatalf("UserPK(%q) y UserPK(%q) producen la misma partición %q", userID, other, pk)
		}
		seen[pk] = userID
	}
}

// TestFavoriteKeySK cubre la construcción de la clave de ordenamiento, con foco
// en el prefijo de scope que permite que otra feature comparta la tabla y en el
// rechazo de identificadores que no son ULID.
func TestFavoriteKeySK(t *testing.T) {
	tests := []struct {
		name    string
		key     FavoriteKey
		wantSK  string
		wantErr error
	}{
		{
			name:   "favorito de programa",
			key:    FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
			wantSK: "FAV#PROGRAMA#" + ulidA,
		},
		{
			name:    "scope desconocido",
			key:     FavoriteKey{UserID: "u1", Scope: Scope("desconocido"), ID: ulidA},
			wantErr: ErrInvalidScope,
		},
		{
			name:    "scope vacío",
			key:     FavoriteKey{UserID: "u1", ID: ulidA},
			wantErr: ErrInvalidScope,
		},
		{
			name:    "scope en mayúsculas: el valor del JSON es en minúsculas",
			key:     FavoriteKey{UserID: "u1", Scope: Scope("PROGRAMA"), ID: ulidA},
			wantErr: ErrInvalidScope,
		},
		{
			name:    "identificador vacío",
			key:     FavoriteKey{UserID: "u1", Scope: ScopeProgram},
			wantErr: ErrInvalidFavoriteID,
		},
		{
			name:    "identificador que no es un ULID",
			key:     FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: "mi-favorito"},
			wantErr: ErrInvalidFavoriteID,
		},
		{
			name:    "identificador con el separador de claves",
			key:     FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: "FAV#PROGRAMA#" + ulidA},
			wantErr: ErrInvalidFavoriteID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.key.SK()

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("SK() error = %v, want %v", err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("SK() = %q, want vacío cuando hay error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("SK() error = %v, want nil", err)
			}
			if got != tc.wantSK {
				t.Errorf("SK() = %q, want %q", got, tc.wantSK)
			}
		})
	}
}

// TestFavoriteKeyKey comprueba que la clave completa exija las dos partes: sin
// usuario no hay partición, y sin identificador válido no hay ordenamiento.
func TestFavoriteKeyKey(t *testing.T) {
	tests := []struct {
		name    string
		key     FavoriteKey
		want    Key
		wantErr error
	}{
		{
			name: "clave completa",
			key:  FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA},
			want: Key{PK: "USER#u1", SK: "FAV#PROGRAMA#" + ulidA},
		},
		{
			name:    "sin usuario",
			key:     FavoriteKey{Scope: ScopeProgram, ID: ulidA},
			wantErr: ErrEmptyUserID,
		},
		{
			name:    "sin identificador",
			key:     FavoriteKey{UserID: "u1", Scope: ScopeProgram},
			wantErr: ErrInvalidFavoriteID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.key.Key()

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Key() error = %v, want %v", err, tc.wantErr)
				}
				if got != (Key{}) {
					t.Errorf("Key() = %+v, want vacía cuando hay error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("Key() error = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("Key() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseFavoriteSK cubre el parseo de la clave de ordenamiento, que es de
// donde salen el identificador y el scope que expone la respuesta.
func TestParseFavoriteSK(t *testing.T) {
	tests := []struct {
		name      string
		sk        string
		wantScope Scope
		wantID    string
		wantErr   error
	}{
		{
			name:      "favorito de programa",
			sk:        "FAV#PROGRAMA#" + ulidA,
			wantScope: ScopeProgram,
			wantID:    ulidA,
		},
		{
			name:      "cotización",
			sk:        "QUOTE#COTIZACION#" + ulidA,
			wantScope: ScopeQuotation,
			wantID:    ulidA,
		},
		{
			name:    "sin los tres componentes",
			sk:      "FAV#PROGRAMA",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "clave vacía",
			sk:      "",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "prefijo distinto: otra colección de la misma tabla",
			sk:      "PROG#PROGRAMA#" + ulidA,
			wantErr: ErrMalformedSK,
		},
		{
			name:    "segmento de scope desconocido",
			sk:      "FAV#COTIZACION#" + ulidA,
			wantErr: ErrMalformedSK,
		},
		{
			name:    "segmento de scope en minúsculas: la clave lo lleva en mayúsculas",
			sk:      "FAV#programa#" + ulidA,
			wantErr: ErrInvalidScope,
		},
		{
			name:    "identificador que no es un ULID",
			sk:      "FAV#PROGRAMA#mi-favorito",
			wantErr: ErrInvalidFavoriteID,
		},
		{
			name:    "identificador vacío",
			sk:      "FAV#PROGRAMA#",
			wantErr: ErrInvalidFavoriteID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope, id, err := ParseFavoriteSK(tc.sk)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParseFavoriteSK() error = %v, want %v", err, tc.wantErr)
				}
				if scope != "" || id != "" {
					t.Errorf("ParseFavoriteSK() = (%q, %q), want vacíos cuando hay error", scope, id)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseFavoriteSK() error = %v, want nil", err)
			}
			if scope != tc.wantScope {
				t.Errorf("scope = %q, want %q", scope, tc.wantScope)
			}
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
		})
	}
}

// TestFavoriteKeyRoundTrip comprueba que construir y parsear sean inversas para
// todos los scopes conocidos, de modo que un scope nuevo no pueda quedar con
// segmento declarado en una dirección y no en la otra.
func TestFavoriteKeyRoundTrip(t *testing.T) {
	for _, scope := range Scopes() {
		t.Run(string(scope), func(t *testing.T) {
			key := FavoriteKey{UserID: "u1", Scope: scope, ID: ulidA}

			sk, err := key.SK()
			if err != nil {
				t.Fatalf("SK() error = %v", err)
			}

			gotScope, gotID, err := ParseFavoriteSK(sk)
			if err != nil {
				t.Fatalf("ParseFavoriteSK(%q) error = %v", sk, err)
			}
			if gotScope != scope {
				t.Errorf("scope = %q, want %q", gotScope, scope)
			}
			if gotID != key.ID {
				t.Errorf("id = %q, want %q", gotID, key.ID)
			}
		})
	}
}

// TestScopeSKPrefix fija el prefijo que usa la condición begins_with del listado
// (Requirement 11.5) y comprueba que un scope desconocido no devuelva un
// prefijo que haga coincidir ítems ajenos.
func TestScopeSKPrefix(t *testing.T) {
	if got, want := ScopeProgram.SKPrefix(), "FAV#PROGRAMA#"; got != want {
		t.Errorf("ScopeProgram.SKPrefix() = %q, want %q", got, want)
	}
	if got, want := ScopeQuotation.SKPrefix(), "QUOTE#COTIZACION#"; got != want {
		t.Errorf("ScopeQuotation.SKPrefix() = %q, want %q", got, want)
	}

	if got := Scope("desconocido").SKPrefix(); got != "" {
		t.Errorf("SKPrefix() de un scope desconocido = %q, want vacío", got)
	}

	sk, err := FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: ulidA}.SK()
	if err != nil {
		t.Fatalf("SK() error = %v", err)
	}
	if !strings.HasPrefix(sk, ScopeProgram.SKPrefix()) {
		t.Errorf("la clave %q no empieza con el prefijo %q", sk, ScopeProgram.SKPrefix())
	}
}

// TestScopeValid comprueba que solo los scopes declarados sean válidos.
func TestScopeValid(t *testing.T) {
	for _, scope := range Scopes() {
		if !scope.Valid() {
			t.Errorf("Scopes() incluye %q pero Valid() lo rechaza", scope)
		}
	}

	for _, scope := range []Scope{"", "PROGRAMA", "programas", "desconocido"} {
		if Scope(scope).Valid() {
			t.Errorf("Valid() acepta el scope desconocido %q", scope)
		}
	}
}

// TestNewFavoriteIDIsValidULID comprueba que el identificador generado sea
// aceptado por la validación que aplican los handlers al parámetro de path.
func TestNewFavoriteIDIsValidULID(t *testing.T) {
	id := NewFavoriteID()

	if err := ValidateFavoriteID(id); err != nil {
		t.Fatalf("ValidateFavoriteID(%q) error = %v, want nil", id, err)
	}

	if _, err := (FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: id}).SK(); err != nil {
		t.Errorf("SK() con un identificador recién generado error = %v", err)
	}
}

// TestSortBySKOrdersByCreation es la invariante que hace innecesario un índice
// adicional y que sostiene la decisión de no agregar createdAt a la clave: un
// Query descendente sobre sk entrega los favoritos más recientes primero
// (Requirement 11.5).
//
// El orden se comprueba sobre las claves completas, no sobre los ULID sueltos,
// porque lo que DynamoDB ordena es la sk: el prefijo de scope es común a todos
// los ítems de la colección y por lo tanto no interviene en la comparación.
func TestSortBySKOrdersByCreation(t *testing.T) {
	// Tres identificadores en orden de creación: el mismo milisegundo con
	// aleatoriedad distinta (A y B) y uno posterior (C).
	const (
		first  = "01JBQ8V4Z0000000000000000A"
		second = "01JBQ8V4Z0000000000000000B"
		third  = "01JBQ8V4Z1000000000000000A"
	)

	sks := make([]string, 0, 3)
	for _, id := range []string{third, first, second} {
		sk, err := FavoriteKey{UserID: "u1", Scope: ScopeProgram, ID: id}.SK()
		if err != nil {
			t.Fatalf("SK() error = %v", err)
		}
		sks = append(sks, sk)
	}

	sort.Sort(sort.Reverse(sort.StringSlice(sks)))

	want := []string{
		"FAV#PROGRAMA#" + third,
		"FAV#PROGRAMA#" + second,
		"FAV#PROGRAMA#" + first,
	}
	for i, got := range sks {
		if got != want[i] {
			t.Fatalf("orden descendente = %v, want %v", sks, want)
		}
	}
}
