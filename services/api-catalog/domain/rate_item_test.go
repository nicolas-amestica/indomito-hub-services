package domain

import (
	"reflect"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
)

// TestNewRateItem cubre la construcción del snapshot de respaldo
// (Requirement 14.4): clave fija en su propia partición, fecha de la fuente
// conservada tal cual, y marca de respaldo del snapshot de entrada ignorada,
// porque lo que se persiste es siempre un dato fresco.
func TestNewRateItem(t *testing.T) {
	fetchedAt := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		snapshot program.ExchangeSnapshot
		want     RateItem
	}{
		{
			name: "tasas frescas",
			snapshot: program.ExchangeSnapshot{
				Date: "2026-03-14", UsdToClp: 950, BrlToClp: 165, IsFallback: false,
			},
			want: RateItem{
				PK: RatesPK, SK: RatesLatestSK,
				Date: "2026-03-14", UsdToClp: 950, BrlToClp: 165, FetchedAt: fetchedAt,
			},
		},
		{
			name: "la marca de respaldo del snapshot de entrada no se persiste",
			snapshot: program.ExchangeSnapshot{
				Date: "2026-03-11", UsdToClp: 940, BrlToClp: 160, IsFallback: true,
			},
			want: RateItem{
				PK: RatesPK, SK: RatesLatestSK,
				Date: "2026-03-11", UsdToClp: 940, BrlToClp: 160, FetchedAt: fetchedAt,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewRateItem(tc.snapshot, fetchedAt)
			if got != tc.want {
				t.Errorf("NewRateItem() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestNewRateItemNormalizesFetchedAtToUTC confirma que el instante de la
// consulta se guarda en UTC: el reloj del entorno de ejecución no debe cambiar
// el valor persistido.
func TestNewRateItemNormalizesFetchedAtToUTC(t *testing.T) {
	santiago := time.FixedZone("-03", -3*60*60)
	local := time.Date(2026, 3, 14, 6, 30, 0, 0, santiago)

	item := NewRateItem(program.ExchangeSnapshot{Date: "2026-03-14"}, local)

	if got := item.FetchedAt.Location(); got != time.UTC {
		t.Errorf("FetchedAt.Location() = %v, want UTC", got)
	}
	if !item.FetchedAt.Equal(local) {
		t.Errorf("FetchedAt = %v, want el mismo instante que %v", item.FetchedAt, local)
	}
}

// TestRateItemFallbackSnapshot confirma que leer el ítem produce una respuesta
// con la fecha original y la marca de respaldo activada (Requirement 14.8).
func TestRateItemFallbackSnapshot(t *testing.T) {
	item := RateItem{
		PK: RatesPK, SK: RatesLatestSK,
		Date: "2026-03-11", UsdToClp: 940, BrlToClp: 160,
		FetchedAt: time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC),
	}

	want := program.ExchangeSnapshot{
		Date: "2026-03-11", UsdToClp: 940, BrlToClp: 160, IsFallback: true,
	}

	if got := item.FallbackSnapshot(); got != want {
		t.Errorf("FallbackSnapshot() = %+v, want %+v", got, want)
	}
}

// TestRateItemAttributeNames fija la forma persistida del snapshot. Los nombres
// son el contrato entre la escritura del endpoint de tasas y su lectura de
// respaldo, y una tabla ya escrita con otros nombres queda inalcanzable.
func TestRateItemAttributeNames(t *testing.T) {
	item := NewRateItem(
		program.ExchangeSnapshot{Date: "2026-03-14", UsdToClp: 950, BrlToClp: 165},
		time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC),
	)

	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatalf("MarshalMap() error = %v", err)
	}

	want := []string{"brlToClp", "date", "fetchedAt", "pk", "sk", "usdToClp"}
	if got := attributeNames(av); !reflect.DeepEqual(got, want) {
		t.Fatalf("atributos = %v, want %v", got, want)
	}

	var roundTrip RateItem
	if err := attributevalue.UnmarshalMap(av, &roundTrip); err != nil {
		t.Fatalf("UnmarshalMap() error = %v", err)
	}

	if roundTrip.Date != item.Date || roundTrip.UsdToClp != item.UsdToClp || roundTrip.BrlToClp != item.BrlToClp {
		t.Errorf("round-trip = %+v, want %+v", roundTrip, item)
	}
	if !roundTrip.FetchedAt.Equal(item.FetchedAt) {
		t.Errorf("round-trip FetchedAt = %v, want %v", roundTrip.FetchedAt, item.FetchedAt)
	}
}
