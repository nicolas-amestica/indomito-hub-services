package sii

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- el formato XMLDSIG del SII exige SHA-1.
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

func TestBuildDocumentUsesReservedFolioAndExactReconciledAmount(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	credentials.CAF.AddChild(etree.NewCharData("\n  "))
	document, err := BuildDocument(credentials, Sale{RequestID: "01K8R8TV0J6V3N3V5W9Z6B8A7C", Folio: 7, Amount: 287500, IssueDate: now, Description: "Cuota de gira de estudios"}, now)
	if err != nil {
		t.Fatalf("build document: %v", err)
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(document.XML); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	want := map[string]string{"//TipoDTE": "39", "//Folio": "7", "//IndServicio": "3", "//RUTRecep": "66666666-6", "//MntNeto": "241597", "//IVA": "45903", "//MntTotal": "287500", "//MontoItem": "287500", "//DD/MNT": "287500", "//TSTED": "2026-10-29T18:30:00", "//TmstFirma": "2026-10-29T18:30:00"}
	for path, expected := range want {
		element := parsed.FindElement(path)
		if element == nil || element.Text() != expected {
			t.Fatalf("%s = %v, want %s", path, element, expected)
		}
	}
	if parsed.FindElement("//TED/FRMT") == nil || parsed.FindElement("//Signature") == nil || strings.Contains(string(document.XML), "certificatePassword") {
		t.Fatal("missing tax signatures or leaked configuration")
	}
	if parsed.FindElement("//Signature/KeyInfo/KeyValue/RSAKeyValue") == nil {
		t.Fatal("DTE signature is missing RSAKeyValue")
	}
	if !strings.HasPrefix(string(document.XML), `<?xml version="1.0" encoding="ISO-8859-1"?>`) {
		t.Fatal("DTE XML declaration must be the first node")
	}
	if !strings.Contains(string(document.XML), "\n") {
		t.Fatal("DTE must contain line breaks for the legacy SII parser")
	}
	children := parsed.Root().ChildElements()
	if len(children) != 2 || children[0].Tag != "Documento" || children[1].Tag != "Signature" || children[0].FindElement("Signature") != nil {
		t.Fatal("DTE signature must be a sibling of Documento")
	}
	digestValue := parsed.FindElement("//Signature/SignedInfo/Reference/DigestValue")
	canonical, err := dsig.MakeC14N10RecCanonicalizer().Canonicalize(parsed.FindElement("//Documento"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(canonical) // #nosec G401 -- el formato XMLDSIG del SII exige SHA-1.
	if digestValue == nil || digestValue.Text() != base64.StdEncoding.EncodeToString(digest[:]) {
		t.Fatal("serialized DTE changed after its digest was signed")
	}
	assertXMLSignatureBase64Wrapped(t, parsed.FindElement("//Signature"))
	verifyXMLSignatureValue(t, credentials, parsed.FindElement("//Signature"))
	verifyTEDSignature(t, credentials, parsed.FindElement("//TED"))
}

func verifyTEDSignature(t *testing.T, credentials Credentials, ted *etree.Element) {
	t.Helper()
	if ted == nil {
		t.Fatal("missing TED")
	}
	dd := ted.FindElement("DD")
	frmt := ted.FindElement("FRMT")
	if dd == nil || frmt == nil || frmt.SelectAttrValue("algoritmo", "") != "SHA1withRSA" {
		t.Fatal("incomplete TED signature")
	}
	serialized, err := elementBytes(dd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(serialized), "\r\n\t") || strings.Contains(string(serialized), "> <") {
		t.Fatalf("DD must remain compact as required by SII: %q", serialized)
	}
	serialized, err = latin1(serialized)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(serialized) // #nosec G401 -- el formato TED del SII exige SHA-1.
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(frmt.Text()))
	if err != nil {
		t.Fatal(err)
	}
	if err = rsa.VerifyPKCS1v15(&credentials.CAFPrivateKey.PublicKey, crypto.SHA1, digest[:], signature); err != nil {
		t.Fatalf("serialized TED signature is invalid: %v", err)
	}
}

func verifyXMLSignatureValue(t *testing.T, credentials Credentials, signature *etree.Element) {
	t.Helper()
	signedInfo := signature.FindElement("SignedInfo")
	signatureValue := signature.FindElement("SignatureValue")
	if signedInfo == nil || signatureValue == nil {
		t.Fatal("incomplete XML signature")
	}
	canonical, err := dsig.MakeC14N10RecCanonicalizer().Canonicalize(signedInfo)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.Join(strings.Fields(signatureValue.Text()), "")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum(canonical) // #nosec G401 -- el formato XMLDSIG del SII exige SHA-1.
	publicKey, ok := credentials.Certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("certificate does not contain an RSA public key")
	}
	if err = rsa.VerifyPKCS1v15(publicKey, crypto.SHA1, digest[:], decoded); err != nil {
		t.Fatalf("serialized XML signature value is invalid: %v", err)
	}
}

func assertXMLSignatureBase64Wrapped(t *testing.T, signature *etree.Element) {
	t.Helper()
	if signature == nil {
		t.Fatal("missing XML signature")
	}
	signatureValue := signature.FindElement("SignatureValue")
	if signatureValue == nil || strings.ContainsAny(signatureValue.Text(), "\r\n") {
		t.Fatal("SignatureValue must remain on one line for the SII legacy parser")
	}
	certificate := signature.FindElement("KeyInfo/X509Data/X509Certificate")
	if certificate == nil || strings.HasPrefix(certificate.Text(), "\n") || strings.HasSuffix(certificate.Text(), "\n") {
		t.Fatal("X509Certificate must not have leading or trailing line breaks")
	}
	lines := strings.Split(certificate.Text(), "\n")
	if len(lines) < 2 {
		t.Fatal("X509Certificate must be folded across lines")
	}
	for _, line := range lines {
		if len(line) > 76 {
			t.Fatalf("X509Certificate line exceeds 76 characters: %d", len(line))
		}
	}
}

func TestBuildDocumentSupportsCertificationSetItems(t *testing.T) {
	now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
	document, err := BuildDocument(testCredentials(t, now), Sale{
		RequestID: "01K8R8TV0J6V3N3V5W9Z6B8A7D", Folio: 8, Amount: 14720,
		IssueDate: now, Reference: "CASO-4",
		Items: []SaleItem{
			{Description: "item afecto 1", Quantity: 8, UnitPrice: 1590},
			{Description: "item exento 2", Quantity: 2, UnitPrice: 1000, Exempt: true},
		},
	}, now)
	if err != nil {
		t.Fatalf("build certification document: %v", err)
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(document.XML); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"//MntNeto": "10689", "//IVA": "2031", "//MntExe": "2000", "//MntTotal": "14720", "//Referencia/CodRef": "SET", "//Referencia/RazonRef": "CASO-4"}
	for path, expected := range want {
		if element := parsed.FindElement(path); element == nil || element.Text() != expected {
			t.Fatalf("%s = %v, want %s", path, element, expected)
		}
	}
	details := parsed.FindElements("//Detalle")
	if len(details) != 2 || details[1].FindElement("IndExe") == nil {
		t.Fatalf("unexpected details: %d", len(details))
	}
	totals := parsed.FindElement("//Totales").ChildElements()
	if len(totals) != 4 || totals[0].Tag != "MntNeto" || totals[1].Tag != "MntExe" || totals[2].Tag != "IVA" || totals[3].Tag != "MntTotal" {
		t.Fatalf("unexpected totals order: %v", totals)
	}
}

func TestBuildDocumentSupportsLegacySII512BitCAFKey(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=0")
	now := time.Date(2026, 10, 29, 21, 30, 0, 0, time.UTC)
	credentials := testCredentials(t, now)
	legacyKey, err := rsa.GenerateKey(rand.Reader, 512) // #nosec G401 -- tamaño fijado por el CAF de certificacion del SII.
	if err != nil {
		t.Fatal(err)
	}
	credentials.CAFPrivateKey = legacyKey
	if _, err = BuildDocument(credentials, Sale{RequestID: "01K8R8TV0J6V3N3V5W9Z6B8A7E", Folio: 9, Amount: 1000, IssueDate: now, Description: "Caso SII"}, now); err != nil {
		t.Fatalf("legacy SII CAF key was rejected: %v", err)
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
