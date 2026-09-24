package functions

import (
	"testing"

	"ind-hub-api-gox-sls-pri-gh/services/api-identity/domain"
)

func TestEffectivePermissionsRequiresReadableParent(t *testing.T) {
	raw := []permissionView{
		{Module: domain.Module{Code: "PRG", Level: "LV1"}, Allowances: []string{}},
		{Module: domain.Module{Code: "PROGRAMS", Level: "LV2", ParentCode: "PRG"}, Allowances: []string{"c", "r", "u", "d"}},
		{Module: domain.Module{Code: "ADM", Level: "LV1"}, Allowances: []string{"r"}},
		{Module: domain.Module{Code: "IAM", Level: "LV2", ParentCode: "ADM"}, Allowances: []string{"r"}},
	}

	effective := effectivePermissions(raw)

	if len(effective) != 3 {
		t.Fatalf("se esperaban 3 permisos efectivos y se obtuvieron %d", len(effective))
	}
	for _, permission := range effective {
		if permission.Module.Code == "PROGRAMS" {
			t.Fatal("PROGRAMS no debe ser efectivo cuando PRG no tiene lectura")
		}
	}
}

func TestEffectivePermissionsKeepsChildWhenParentIsReadable(t *testing.T) {
	raw := []permissionView{
		{Module: domain.Module{Code: "PRG", Level: "LV1"}, Allowances: []string{"r"}},
		{Module: domain.Module{Code: "PROGRAMS", Level: "LV2", ParentCode: "PRG"}, Allowances: []string{"r"}},
	}

	if effective := effectivePermissions(raw); len(effective) != 2 {
		t.Fatalf("se esperaban 2 permisos efectivos y se obtuvieron %d", len(effective))
	}
}
