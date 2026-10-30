package sii

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
)

func TestBuildDocumentUsesReservedFolioAndExactReconciledAmount(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	document, err := BuildDocument(credentials, Sale{RequestID: "01K8R8TV0J6V3N3V5W9Z6B8A7C", Folio: 7, Amount: 287500, IssueDate: now, Description: "Cuota de gira de estudios"}, now)
	if err != nil {
		t.Fatalf("build document: %v", err)
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(document.XML); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	want := map[string]string{"//TipoDTE": "39", "//Folio": "7", "//MedioPago": "3", "//RUTRecep": "66666666-6", "//MntNeto": "241597", "//IVA": "45903", "//MntTotal": "287500", "//MontoItem": "287500", "//DD/MNT": "287500"}
	for path, expected := range want {
		element := parsed.FindElement(path)
		if element == nil || element.Text() != expected {
			t.Fatalf("%s = %v, want %s", path, element, expected)
		}
	}
	if parsed.FindElement("//TED/FRMT") == nil || parsed.FindElement("//Signature") == nil || strings.Contains(string(document.XML), "certificatePassword") {
		t.Fatal("missing tax signatures or leaked configuration")
	}
}

func testCredentials(t *testing.T, now time.Time) Credentials {
	t.Helper()
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test signer"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &signingKey.PublicKey, signingKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	cafKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	cafDoc := etree.NewDocument()
	caf := cafDoc.CreateElement("CAF")
	caf.CreateAttr("version", "1.0")
	da := caf.CreateElement("DA")
	add(da, "RE", "76000000-0")
	add(da, "RS", "EMISOR DE PRUEBA")
	add(da, "TD", "39")
	rangeElement := da.CreateElement("RNG")
	add(rangeElement, "D", "1")
	add(rangeElement, "H", "30")
	add(da, "FA", "2026-10-24")
	add(caf, "FRMA", "FIRMA-CAF-DE-PRUEBA")
	return Credentials{Config: Config{Environment: EnvironmentCertification, IssuerActivity: "Servicios de turismo", IssuerAddress: "Direccion de prueba 123", IssuerCommune: "Santiago", SenderRUT: "76000000-0", ResolutionDate: "2026-10-24", ResolutionNumber: 0}, IssuerRUT: "76000000-0", IssuerName: "EMISOR DE PRUEBA", FolioFrom: 1, FolioTo: 30, AuthorizationAt: "2026-10-24", CAF: caf, CAFPrivateKey: cafKey, SigningKey: signingKey, Certificate: certificate}
}
