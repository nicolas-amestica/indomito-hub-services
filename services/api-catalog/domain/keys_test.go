package domain

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// TestCatalogItemKeySK cubre la construcción de la clave de ordenamiento de una
// opción de catálogo, con foco en el relleno de ceros a cuatro dígitos: es lo
// que hace que el orden lexicográfico de DynamoDB coincida con el numérico
// (Requirements 16.2, 16.3).
func TestCatalogItemKeySK(t *testing.T) {
	tests := []struct {
		name    string
		key     CatalogItemKey
		wantSK  string
		wantErr error
	}{
		{
			name:   "orden de un dígito se rellena con tres ceros",
			key:    CatalogItemKey{Scope: ScopePlan, Order: 2, ID: "basico"},
			wantSK: "PLAN#0002#basico",
		},
		{
			name:   "orden de dos dígitos se rellena con dos ceros",
			key:    CatalogItemKey{Scope: ScopePlan, Order: 10, ID: "premium"},
			wantSK: "PLAN#0010#premium",
		},
		{
			name:   "orden en el mínimo",
			key:    CatalogItemKey{Scope: ScopeSeason, Order: OrderMin, ID: "alta"},
			wantSK: "SEASON#0000#alta",
		},
		{
			name:   "orden en el máximo no lleva relleno",
			key:    CatalogItemKey{Scope: ScopeSeason, Order: OrderMax, ID: "baja"},
			wantSK: "SEASON#9999#baja",
		},
		{
			name:   "destino con identificador de plantilla como id",
			key:    CatalogItemKey{Scope: ScopeDestination, Order: 1, ID: "BRF"},
			wantSK: "DESTINATION#0001#BRF",
		},
		{
			name:    "scope desconocido",
			key:     CatalogItemKey{Scope: Scope("HOTEL"), Order: 1, ID: "x"},
			wantErr: ErrInvalidScope,
		},
		{
			name:    "scope vacío",
			key:     CatalogItemKey{Order: 1, ID: "x"},
			wantErr: ErrInvalidScope,
		},
		{
			name:    "orden negativo: el signo ocuparía un dígito del relleno",
			key:     CatalogItemKey{Scope: ScopePlan, Order: -1, ID: "x"},
			wantErr: ErrOrderOutOfRange,
		},
		{
			name:    "orden por encima del máximo: no cabe en cuatro dígitos",
			key:     CatalogItemKey{Scope: ScopePlan, Order: OrderMax + 1, ID: "x"},
			wantErr: ErrOrderOutOfRange,
		},
		{
			name:    "identificador vacío",
			key:     CatalogItemKey{Scope: ScopePlan, Order: 1},
			wantErr: ErrEmptyID,
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

// TestCatalogItemKeyKey confirma que la clave completa lleva la partición
// compartida CATALOG, que es lo que permite resolver GET /catalogos con un
// único Query (Requirement 16.2).
func TestCatalogItemKeyKey(t *testing.T) {
	key, err := CatalogItemKey{Scope: ScopeDestination, Order: 3, ID: "CBU"}.Key()
	if err != nil {
		t.Fatalf("Key() error = %v, want nil", err)
	}

	if key.PK != CatalogPK {
		t.Errorf("Key().PK = %q, want %q", key.PK, CatalogPK)
	}
	if key.SK != "DESTINATION#0003#CBU" {
		t.Errorf("Key().SK = %q, want %q", key.SK, "DESTINATION#0003#CBU")
	}

	if _, err := (CatalogItemKey{Scope: ScopePlan, Order: -5, ID: "x"}).Key(); !errors.Is(err, ErrOrderOutOfRange) {
		t.Errorf("Key() error = %v, want %v", err, ErrOrderOutOfRange)
	}
}

// TestParseCatalogItemSK cubre el parseo de la clave de ordenamiento, incluido
// el rechazo de un orden sin relleno: una clave con "5" donde debería haber
// "0005" ya quedó en el lugar equivocado del orden que entrega DynamoDB, así
// que interpretarla como válida escondería el problema.
func TestParseCatalogItemSK(t *testing.T) {
	tests := []struct {
		name    string
		sk      string
		want    CatalogItemKey
		wantErr error
	}{
		{
			name: "plan",
			sk:   "PLAN#0002#basico",
			want: CatalogItemKey{Scope: ScopePlan, Order: 2, ID: "basico"},
		},
		{
			name: "temporada con orden de dos dígitos",
			sk:   "SEASON#0010#alta",
			want: CatalogItemKey{Scope: ScopeSeason, Order: 10, ID: "alta"},
		},
		{
			name: "destino en el orden máximo",
			sk:   "DESTINATION#9999#BRF",
			want: CatalogItemKey{Scope: ScopeDestination, Order: OrderMax, ID: "BRF"},
		},
		{
			name: "identificador con el separador: el corte deja el resto intacto",
			sk:   "PLAN#0001#con#separador",
			want: CatalogItemKey{Scope: ScopePlan, Order: 1, ID: "con#separador"},
		},
		{
			name:    "clave del ítem de parámetros de política",
			sk:      SettingsSK,
			wantErr: ErrMalformedSK,
		},
		{
			name:    "sin el tercer componente",
			sk:      "PLAN#0001",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "identificador vacío",
			sk:      "PLAN#0001#",
			wantErr: ErrEmptyID,
		},
		{
			name:    "scope desconocido",
			sk:      "HOTEL#0001#x",
			wantErr: ErrInvalidScope,
		},
		{
			name:    "orden sin relleno",
			sk:      "PLAN#5#basico",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "orden con más dígitos que el relleno",
			sk:      "PLAN#00005#basico",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "orden con signo: cuatro caracteres pero no cuatro dígitos",
			sk:      "PLAN#-005#basico",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "orden no numérico",
			sk:      "PLAN#abcd#basico",
			wantErr: ErrMalformedSK,
		},
		{
			name:    "clave vacía",
			sk:      "",
			wantErr: ErrMalformedSK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCatalogItemSK(tc.sk)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParseCatalogItemSK(%q) error = %v, want %v", tc.sk, err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseCatalogItemSK(%q) error = %v, want nil", tc.sk, err)
			}
			if got != tc.want {
				t.Errorf("ParseCatalogItemSK(%q) = %+v, want %+v", tc.sk, got, tc.want)
			}
		})
	}
}

// TestCatalogItemKeyRoundTrip confirma que construir y volver a parsear devuelve
// los mismos componentes, para todos los scopes y en los bordes del relleno.
func TestCatalogItemKeyRoundTrip(t *testing.T) {
	orders := []int{OrderMin, 1, 9, 10, 99, 100, 999, 1000, OrderMax}

	for _, scope := range Scopes() {
		for _, order := range orders {
			key := CatalogItemKey{Scope: scope, Order: order, ID: "id-de-prueba"}

			sk, err := key.SK()
			if err != nil {
				t.Fatalf("SK() error = %v para %+v", err, key)
			}

			got, err := ParseCatalogItemSK(sk)
			if err != nil {
				t.Fatalf("ParseCatalogItemSK(%q) error = %v", sk, err)
			}
			if got != key {
				t.Errorf("round-trip de %q = %+v, want %+v", sk, got, key)
			}
		}
	}
}

// TestSKLexicographicOrderMatchesNumeric es la razón de existir del relleno de
// ceros: ordenar las claves como texto —que es lo que hace DynamoDB al entregar
// el resultado de un Query— tiene que producir el orden numérico de
// presentación. Sin el relleno, 10 iría antes que 2 y el handler tendría que
// reordenar.
func TestSKLexicographicOrderMatchesNumeric(t *testing.T) {
	orders := []int{100, 2, 10, 0, 9999, 1, 20, 3}

	sks := make([]string, 0, len(orders))
	for _, order := range orders {
		sk, err := CatalogItemKey{Scope: ScopePlan, Order: order, ID: "x"}.SK()
		if err != nil {
			t.Fatalf("SK() error = %v para el orden %d", err, order)
		}
		sks = append(sks, sk)
	}

	sort.Strings(sks)

	sortedOrders := append([]int(nil), orders...)
	sort.Ints(sortedOrders)

	for i, sk := range sks {
		key, err := ParseCatalogItemSK(sk)
		if err != nil {
			t.Fatalf("ParseCatalogItemSK(%q) error = %v", sk, err)
		}
		if key.Order != sortedOrders[i] {
			t.Errorf("posición %d del orden lexicográfico = %d, want %d (claves: %v)",
				i, key.Order, sortedOrders[i], sks)
		}
	}
}

// TestSettingsSKCannotCollideWithOptions comprueba la invariante que permite al
// ítem SETTINGS compartir la partición CATALOG con las opciones: su clave no
// contiene el separador, así que ninguna clave de opción puede igualarla ni
// confundirse con ella.
func TestSettingsSKCannotCollideWithOptions(t *testing.T) {
	if strings.Contains(SettingsSK, keySeparator) {
		t.Fatalf("SettingsSK = %q, no debe contener %q", SettingsSK, keySeparator)
	}

	if _, err := ParseCatalogItemSK(SettingsSK); err == nil {
		t.Errorf("ParseCatalogItemSK(%q) error = nil, want error: SETTINGS no es una opción", SettingsSK)
	}

	for _, scope := range Scopes() {
		if strings.HasPrefix(SettingsSK, scope.Prefix()) {
			t.Errorf("SettingsSK = %q no debe empezar con el prefijo %q", SettingsSK, scope.Prefix())
		}
	}
}

// TestIsSettingsSK confirma la discriminación que hace el handler sobre cada
// ítem del único Query.
func TestIsSettingsSK(t *testing.T) {
	tests := []struct {
		sk   string
		want bool
	}{
		{sk: SettingsSK, want: true},
		{sk: "SETTINGS", want: true},
		{sk: "PLAN#0001#basico", want: false},
		{sk: "settings", want: false},
		{sk: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.sk, func(t *testing.T) {
			if got := IsSettingsSK(tc.sk); got != tc.want {
				t.Errorf("IsSettingsSK(%q) = %t, want %t", tc.sk, got, tc.want)
			}
		})
	}
}

// TestScopeValidAndPrefix cubre los scopes conocidos y su prefijo de clave.
func TestScopeValidAndPrefix(t *testing.T) {
	tests := []struct {
		scope      Scope
		wantValid  bool
		wantPrefix string
	}{
		{scope: ScopePlan, wantValid: true, wantPrefix: "PLAN#"},
		{scope: ScopeSeason, wantValid: true, wantPrefix: "SEASON#"},
		{scope: ScopeDestination, wantValid: true, wantPrefix: "DESTINATION#"},
		{scope: Scope("PLAN#"), wantValid: false, wantPrefix: "PLAN##"},
		{scope: Scope("plan"), wantValid: false, wantPrefix: "plan#"},
		{scope: Scope(""), wantValid: false, wantPrefix: "#"},
	}

	for _, tc := range tests {
		t.Run(string(tc.scope), func(t *testing.T) {
			if got := tc.scope.Valid(); got != tc.wantValid {
				t.Errorf("Valid() = %t, want %t", got, tc.wantValid)
			}
			if got := tc.scope.Prefix(); got != tc.wantPrefix {
				t.Errorf("Prefix() = %q, want %q", got, tc.wantPrefix)
			}
		})
	}

	if len(Scopes()) != 3 {
		t.Errorf("Scopes() devolvió %d scopes, want 3", len(Scopes()))
	}
}

// TestFixedKeys fija las claves constantes de la tabla. Cambiarlas invalida los
// datos ya sembrados, así que el test está para que la ruptura aparezca acá y no
// en una consulta que devuelve vacío.
func TestFixedKeys(t *testing.T) {
	if got := SettingsKey(); got != (Key{PK: "CATALOG", SK: "SETTINGS"}) {
		t.Errorf("SettingsKey() = %+v, want {CATALOG SETTINGS}", got)
	}

	if got := RatesSnapshotKey(); got != (Key{PK: "RATES", SK: "LATEST"}) {
		t.Errorf("RatesSnapshotKey() = %+v, want {RATES LATEST}", got)
	}

	if RatesPK == CatalogPK {
		t.Error("RatesPK y CatalogPK deben ser particiones distintas")
	}
}
