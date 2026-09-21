package main

import (
	"os"
	"path/filepath"
	"testing"
)

const validManifestJSON = `{
  "plans":[{"id":"tour","display":"Gira","order":1,"active":true}],
  "seasons":[{"id":"2027","display":"2027","order":1,"active":true}],
  "destinations":[{"id":"BRF","display":"Bariloche","order":1,"active":true,"budgetTemplateId":"brochure-brf"}],
  "settings":{"defaultPlanId":"tour","margin":{"usdIncreaseCLP":50,"brlIncreaseCLP":10,"utilityRate":20,"rechargeRate":5,"minUtilityRate":10},"scenarioOffsets":[-10,-5,0,5]}
}`

func TestPrepare(t *testing.T) {
	manifestPath := writeManifest(t, validManifestJSON)

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "dry-run válido", args: []string{"-file", manifestPath}},
		{
			name: "apply válido",
			args: []string{
				"-file", manifestPath,
				"-table", "ind-dev-catalogos-ddb-dev-pri-use1",
				"-profile", "pa-dev",
				"-apply",
			},
		},
		{name: "sin archivo", args: nil, wantErr: true},
		{name: "apply sin tabla", args: []string{"-file", manifestPath, "-profile", "pa-dev", "-apply"}, wantErr: true},
		{name: "apply sin perfil", args: []string{"-file", manifestPath, "-table", "catalogos", "-apply"}, wantErr: true},
		{name: "perfil no corresponde al stage", args: []string{"-file", manifestPath, "-table", "catalogos", "-stage", "prd", "-profile", "pa-dev", "-apply"}, wantErr: true},
		{name: "stage desconocido", args: []string{"-file", manifestPath, "-stage", "qa"}, wantErr: true},
		{name: "región incorrecta", args: []string{"-file", manifestPath, "-region", "us-west-2"}, wantErr: true},
		{name: "argumento posicional", args: []string{"-file", manifestPath, "extra"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := prepare(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("prepare() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestPrepareRejectsOversizedManifest(t *testing.T) {
	manifestPath := writeManifest(t, validManifestJSON+string(make([]byte, maxManifestBytes)))

	if _, _, err := prepare([]string{"-file", manifestPath}); err == nil {
		t.Fatal("prepare() error = nil, want error")
	}
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "catalog-seed.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el manifiesto de prueba: %v", err)
	}
	return path
}
