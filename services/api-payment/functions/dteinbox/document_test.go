package dteinbox

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
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

func signedFixture(t *testing.T) []byte {
	return signedFixtureProfile(t, false)
}

func signedFixtureProfile(t *testing.T, standalone bool) []byte {
	return signedFixtureContext(t, standalone, false)
}

func signedFixtureContext(t *testing.T, standalone, envelope bool) []byte {
	return signedFixturePayment(t, standalone, envelope, "")
}

func signedFixturePayment(t *testing.T, standalone, envelope bool, payment string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "prueba"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	doc := etree.NewDocument()
	if err = doc.ReadFromString(`<DTE xmlns="http://www.sii.cl/SiiDte" version="1.0"><Documento ID="F123T33"><Encabezado><IdDoc><TipoDTE>33</TipoDTE><Folio>123</Folio><FchEmis>2026-11-01</FchEmis></IdDoc><Emisor><RUTEmisor>76000000-0</RUTEmisor><RznSoc>Proveedor Ágil</RznSoc></Emisor><Receptor><RUTRecep>76111111-1</RUTRecep></Receptor><Totales><MntTotal>11900</MntTotal></Totales></Encabezado></Documento></DTE>`); err != nil {
		t.Fatal(err)
	}
	signer, err := dsig.NewSigningContext(key, [][]byte{der})
	if payment != "" {
		doc.Root().FindElement("Documento/Encabezado/IdDoc").CreateElement("FmaPago").SetText(payment)
	}
	if err != nil {
		t.Fatal(err)
	}
	signer.Hash = crypto.SHA1
	signer.IdAttribute = "ID"
	signer.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = signer.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		t.Fatal(err)
	}
	target := doc.Root().FindElement("Documento")
	if envelope {
		signer.Prefix = ""
	}
	if standalone {
		target = target.Copy()
		signer.Prefix = ""
	}
	signature, err := signer.ConstructSignature(target, false)
	if err != nil {
		t.Fatal(err)
	}
	doc.Root().AddChild(signature)
	if standalone || envelope {
		envelope := etree.NewDocument()
		root := envelope.CreateElement("EnvioDTE")
		root.CreateAttr("xmlns", "http://www.sii.cl/SiiDte")
		root.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
		root.CreateElement("SetDTE").AddChild(doc.Root().Copy())
		doc = envelope
	}
	raw, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSignedPaymentForms(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		expected    int
		valid       bool
	}{
		{"ausente es credito", "", 2, true},
		{"contado", "1", 1, true},
		{"credito", "2", 2, true},
		{"gratuito", "3", 3, true},
		{"invalido", "4", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs, err := ParseDocuments(signedFixturePayment(t, false, false, tc.value), "76111111-1", time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && docs[0].PaymentForm != tc.expected {
				t.Fatal("forma de pago incorrecta")
			}
		})
	}
}

func TestNamespacedSignatureBeforeEnvelope(t *testing.T) {
	original := signedFixtureContext(t, false, true)
	for _, test := range []struct {
		name  string
		raw   []byte
		valid bool
	}{
		{"namespace SII previo al sobre xsi", original, true},
		{"monto alterado", []byte(strings.Replace(string(original), "11900", "11901", 1)), false},
		{"receptor alterado", []byte(strings.Replace(string(original), "76111111-1", "76111111-2", 1)), false},
		{"namespace alterado", []byte(strings.Replace(string(original), `<Documento ID=`, `<Documento xmlns="urn:other" ID=`, 1)), false},
		{"firma alterada", []byte(strings.Replace(string(original), "<SignatureValue>", "<SignatureValue>AAAA", 1)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDocuments(test.raw, "76111111-1", time.Now())
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestStandaloneProviderSignatureAndTampering(t *testing.T) {
	original := signedFixtureProfile(t, true)
	for _, test := range []struct {
		name  string
		raw   []byte
		valid bool
	}{
		{"standalone inside envelope", original, true},
		{"tampered amount", []byte(strings.Replace(string(original), "11900", "11901", 1)), false},
		{"tampered receiver", []byte(strings.Replace(string(original), "76111111-1", "76111111-2", 1)), false},
		{"changed signed namespace", []byte(strings.Replace(string(original), `<Documento ID=`, `<Documento xmlns="urn:other" ID=`, 1)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDocuments(test.raw, "76111111-1", time.Now())
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestSignedDocumentsRejectTamperingAndWrongReceiver(t *testing.T) {
	original := signedFixture(t)
	for _, test := range []struct {
		name     string
		raw      []byte
		receiver string
		valid    bool
	}{
		{"valid", original, "76.111.111-1", true},
		{"wrong receiver", original, "99999999-9", false},
		{"tampered amount", []byte(strings.Replace(string(original), "11900", "99900", 1)), "76111111-1", false},
		{"DTD", append([]byte(`<!DOCTYPE DTE [<!ENTITY xx SYSTEM "file:///etc/passwd">]>`), original...), "76111111-1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			docs, err := ParseDocuments(test.raw, test.receiver, time.Now())
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
			if test.valid && (len(docs) != 1 || docs[0].Total != 11900 || docs[0].IssuerName != "Proveedor Ágil") {
				t.Fatalf("unexpected document %+v", docs)
			}
		})
	}
}

func TestAliasAndMIME(t *testing.T) {
	for _, test := range []struct {
		name, headers string
		valid         bool
	}{
		{"primary delivery", "Delivered-To: sii@work.girasindomito.cl\r\n", true},
		{"archive and read irrelevant", "To: sii@work.girasindomito.cl\r\nStatus: R\r\n", true},
		{"other alias", "To: sii-dev@work.girasindomito.cl\r\n", false},
		{"both aliases", "To: sii@work.girasindomito.cl, sii-dev@work.girasindomito.cl\r\n", false},
		{"substring spoof", "To: fakesii@work.girasindomito.cl\r\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := test.headers + "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: application/xml; name=dte.xml\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte("<DTE/>")) + "\r\n--x--\r\n"
			files, err := Extract([]byte(raw), "sii@work.girasindomito.cl", "sii-dev@work.girasindomito.cl")
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
			if test.valid && (len(files) != 1 || string(files[0].Raw) != "<DTE/>") {
				t.Fatalf("unexpected files %+v", files)
			}
		})
	}
}
