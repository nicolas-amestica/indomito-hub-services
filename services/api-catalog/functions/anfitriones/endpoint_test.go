package meetinghosts

import (
	"testing"
	"time"
)

func TestHostFromInputUsesConfigurationKeys(t *testing.T) {
	input := hostInput{Name: "Ana", Description: "Ventas", Color: "#112233", Active: true, IsDefault: true}
	host := hostFromInput("01K7D7AQ000000000000000001", input, "created", "updated")
	if host.PK != configurationPK || host.SK != hostPrefix+host.ID {
		t.Fatalf("claves = %s/%s", host.PK, host.SK)
	}
	if host.IsDefault {
		t.Fatal("isDefault no debe persistirse en el item del anfitrion")
	}
}

func TestValidatedID(t *testing.T) {
	valid := "01K7D7AQ000000000000000001"
	if got, err := validatedID(valid); err != nil || got != valid {
		t.Fatalf("validatedID(%q) = %q, %v", valid, got, err)
	}
	if _, err := validatedID("host-1"); err == nil {
		t.Fatal("se esperaba error para un identificador invalido")
	}
}

func TestHostTimestampsRemainStableOnUpdate(t *testing.T) {
	created := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	updated := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC).Format(time.RFC3339)
	host := hostFromInput("01K7D7AQ000000000000000001", hostInput{Name: "Ana", Color: "#112233", Active: true}, created, updated)
	if host.CreatedAt != created || host.UpdatedAt != updated {
		t.Fatalf("timestamps = %s/%s", host.CreatedAt, host.UpdatedAt)
	}
}
