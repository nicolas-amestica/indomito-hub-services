package sii

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLocalSIIConfiguration(t *testing.T) {
	configPath := os.Getenv("SII_LOCAL_CONFIG_PATH")
	pfxPath := os.Getenv("SII_LOCAL_PFX_PATH")
	cafPath := os.Getenv("SII_LOCAL_CAF_PATH")
	if configPath == "" || pfxPath == "" || cafPath == "" {
		t.Skip("local SII configuration paths are not configured")
	}

	labeled, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(labeled), "\n") {
		label, value, found := strings.Cut(line, ":")
		if found {
			values[strings.TrimSpace(label)] = strings.TrimSpace(value)
		}
	}
	pfx, err := os.ReadFile(pfxPath)
	if err != nil {
		t.Fatal(err)
	}
	caf, err := os.ReadFile(cafPath)
	if err != nil {
		t.Fatal(err)
	}
	resolutionNumber, err := strconv.ParseInt(values["Nro resolución"], 10, 64)
	if err != nil {
		t.Fatal("invalid resolution number")
	}
	resolutionDate := values["Fecha resolución"]
	if parsed, parseErr := time.Parse("02-01-2006", resolutionDate); parseErr == nil {
		resolutionDate = parsed.Format(time.DateOnly)
	}
	raw, err := json.Marshal(Config{
		Enabled:              true,
		Environment:          EnvironmentCertification,
		IssuerActivity:       values["Giro"],
		IssuerAddress:        values["Dirección"],
		IssuerCommune:        values["Comuna"],
		SenderRUT:            values["Rut del firmante"],
		ResolutionDate:       resolutionDate,
		ResolutionNumber:     resolutionNumber,
		CertificatePFXBase64: base64.StdEncoding.EncodeToString(pfx),
		CertificatePassword:  values["Clave pfx"],
		CAFXMLBase64:         base64.StdEncoding.EncodeToString(caf),
	})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := ParseConfig(raw, time.Now())
	if err != nil {
		t.Fatalf("local SII configuration is invalid: %v", err)
	}
	if credentials.FolioFrom < 1 || credentials.FolioTo < credentials.FolioFrom {
		t.Fatal("invalid CAF folio range")
	}
}
