package functions

import (
	"testing"

	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

func TestValidInteractive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value *domain.MessageInteractive
		want  bool
	}{
		{name: "buttons", value: &domain.MessageInteractive{Kind: "button", Body: "Elige", Options: []domain.MessageInteractiveOption{{ID: "one", Title: "Uno"}}}, want: true},
		{name: "list", value: &domain.MessageInteractive{Kind: "list", Body: "Elige", Button: "Ver opciones", Options: []domain.MessageInteractiveOption{{ID: "one", Title: "Uno"}}}, want: true},
		{name: "too many buttons", value: &domain.MessageInteractive{Kind: "button", Body: "Elige", Options: []domain.MessageInteractiveOption{{ID: "1", Title: "1"}, {ID: "2", Title: "2"}, {ID: "3", Title: "3"}, {ID: "4", Title: "4"}}}, want: false},
		{name: "duplicate id", value: &domain.MessageInteractive{Kind: "list", Body: "Elige", Button: "Ver", Options: []domain.MessageInteractiveOption{{ID: "same", Title: "1"}, {ID: "same", Title: "2"}}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validInteractive(test.value); got != test.want {
				t.Fatalf("validInteractive() = %v, want %v", got, test.want)
			}
		})
	}
}
