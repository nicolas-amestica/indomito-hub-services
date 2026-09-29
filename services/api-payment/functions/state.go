// Package functions implementa el entorno local de pagos con datos ficticios.
package functions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/domain"
)

type localState struct {
	Installments []domain.Installment `json:"installments"`
	Payments     []domain.Payment     `json:"payments"`
	Receipts     []domain.Receipt     `json:"receipts"`
	Expenses     []domain.Expense     `json:"expenses"`
	Events       []domain.Event       `json:"events"`
	Mail         []domain.Mail        `json:"mail"`
	Keys         map[string]string    `json:"keys"`
}
type challenge struct {
	Code, Passenger string
	Expires         time.Time
	Attempts        int
}
type session struct {
	Passenger string
	Expires   time.Time
}
type rateWindow struct {
	Start time.Time
	Count int
}

// App mantiene el estado serializado y capacidades locales, sin acceso a AWS.
type App struct {
	mu         sync.Mutex
	state      localState
	file       string
	adminKey   string
	challenges map[string]challenge
	sessions   map[string]session
	rates      map[string]rateWindow
}

// Open abre o inicializa exclusivamente datos ficticios locales.
func Open(directory string, adminKey string) (*App, error) {
	if len(adminKey) < 32 {
		return nil, errors.New("se requiere clave administrativa local")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	a := &App{file: filepath.Join(directory, "state.json"), adminKey: adminKey,
		challenges: map[string]challenge{}, sessions: map[string]session{}, rates: map[string]rateWindow{}}
	data, err := os.ReadFile(a.file)
	if err == nil {
		err = json.Unmarshal(data, &a.state)
		return a, err
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	a.state = localState{Installments: []domain.Installment{}, Payments: []domain.Payment{}, Receipts: []domain.Receipt{},
		Expenses: []domain.Expense{}, Events: []domain.Event{}, Mail: []domain.Mail{}, Keys: map[string]string{}}
	start := time.Now().UTC()
	for n := 0; n < 5; n++ {
		a.state.Installments = append(a.state.Installments, domain.Installment{ID: ulid.Make().String(), TripID: "gira-demo", TripName: "Gira al sur · Colegio de demostración", PassengerID: "passenger-demo", Label: "Cuota " + string(rune('1'+n)) + " de 5", DueDate: time.Date(start.Year(), start.Month()+time.Month(n), 5, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Amount: 20000})
	}
	a.state.Expenses = append(a.state.Expenses, domain.Expense{ID: ulid.Make().String(), TripID: "gira-demo", Category: "Alojamiento", Supplier: "Hotel de demostración", DueDate: start.AddDate(0, 1, 0).Format(time.DateOnly), Amount: 60000})
	return a, a.persist()
}

func (a *App) persist() error {
	data, err := json.MarshalIndent(a.state, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.file), "payment-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	// Tras Rename el temporal ya no existe; su limpieza no cambia la transacción.
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err = f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, a.file)
}

// mutate hace rollback en memoria si falla la persistencia; se invoca bajo mutex.
func (a *App) mutate(fn func() error) error {
	before, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	if err = fn(); err == nil {
		err = a.persist()
	}
	if err != nil {
		if rollbackErr := json.Unmarshal(before, &a.state); rollbackErr != nil {
			return rollbackErr
		}
	}
	return err
}

func (a *App) event(kind, aggregate, trip string, amount int64) {
	a.state.Events = append(a.state.Events, domain.Event{ID: ulid.Make().String(), Version: 1, Type: kind, AggregateID: aggregate, TripID: trip, Amount: amount, Currency: "CLP", OccurredAt: time.Now().UTC()})
}
