package sii

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- algoritmo exigido por el formato TED del SII.
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

type Sale struct {
	RequestID   string
	Folio       int64
	Amount      int64
	IssueDate   time.Time
	CustomerRUT string
	Description string
}

type Document struct {
	XML       []byte
	Folio     int64
	IssuerRUT string
	Amount    int64
}

func BuildDocument(credentials Credentials, sale Sale, now time.Time) (Document, error) {
	if sale.RequestID == "" || sale.Folio < credentials.FolioFrom || sale.Folio > credentials.FolioTo || sale.Amount <= 0 || sale.IssueDate.IsZero() || strings.TrimSpace(sale.Description) == "" || now.IsZero() {
		return Document{}, ErrInvalidConfiguration
	}
	documentID := "B39F" + strconv.FormatInt(sale.Folio, 10)
	doc := etree.NewDocument()
	dte := doc.CreateElement("DTE")
	dte.CreateAttr("version", "1.0")
	dte.CreateAttr("xmlns", "http://www.sii.cl/SiiDte")
	document := dte.CreateElement("Documento")
	document.CreateAttr("ID", documentID)
	header := document.CreateElement("Encabezado")
	idDoc := header.CreateElement("IdDoc")
	add(idDoc, "TipoDTE", "39")
	add(idDoc, "Folio", strconv.FormatInt(sale.Folio, 10))
	add(idDoc, "FchEmis", sale.IssueDate.Format(time.DateOnly))
	add(idDoc, "IndServicio", "3")
	add(idDoc, "MedioPago", "3")
	emitter := header.CreateElement("Emisor")
	add(emitter, "RUTEmisor", credentials.IssuerRUT)
	add(emitter, "RznSocEmisor", credentials.IssuerName)
	add(emitter, "GiroEmisor", credentials.IssuerActivity)
	add(emitter, "DirOrigen", credentials.IssuerAddress)
	add(emitter, "CmnaOrigen", credentials.IssuerCommune)
	add(emitter, "RutProvSW", credentials.IssuerRUT)
	receiver := header.CreateElement("Receptor")
	add(receiver, "RUTRecep", receiverRUT(sale.CustomerRUT))
	add(receiver, "RznSocRecep", "CONSUMIDOR FINAL")
	totals := header.CreateElement("Totales")
	netAmount := (sale.Amount*100 + 59) / 119
	add(totals, "MntNeto", strconv.FormatInt(netAmount, 10))
	add(totals, "IVA", strconv.FormatInt(sale.Amount-netAmount, 10))
	add(totals, "MntTotal", strconv.FormatInt(sale.Amount, 10))
	detail := document.CreateElement("Detalle")
	add(detail, "NroLinDet", "1")
	add(detail, "NmbItem", strings.TrimSpace(sale.Description))
	add(detail, "QtyItem", "1")
	add(detail, "PrcItem", strconv.FormatInt(sale.Amount, 10))
	add(detail, "MontoItem", strconv.FormatInt(sale.Amount, 10))

	timestamp := now.UTC().Format("2006-01-02T15:04:05")
	dd := etree.NewElement("DD")
	add(dd, "RE", credentials.IssuerRUT)
	add(dd, "TD", "39")
	add(dd, "F", strconv.FormatInt(sale.Folio, 10))
	add(dd, "FE", sale.IssueDate.Format(time.DateOnly))
	add(dd, "RR", receiverRUT(sale.CustomerRUT))
	add(dd, "RSR", "CONSUMIDOR FINAL")
	add(dd, "MNT", strconv.FormatInt(sale.Amount, 10))
	add(dd, "IT1", truncate(strings.TrimSpace(sale.Description), 40))
	dd.AddChild(credentials.CAF.Copy())
	add(dd, "TSTED", timestamp)
	ddBytes, err := elementBytes(dd)
	if err != nil {
		return Document{}, err
	}
	ddBytes, err = latin1(ddBytes)
	if err != nil {
		return Document{}, fmt.Errorf("encode TED: %w", err)
	}
	hash := sha1.Sum(ddBytes)
	signature, err := rsa.SignPKCS1v15(rand.Reader, credentials.CAFPrivateKey, crypto.SHA1, hash[:])
	if err != nil {
		return Document{}, fmt.Errorf("sign TED: %w", err)
	}
	ted := document.CreateElement("TED")
	ted.CreateAttr("version", "1.0")
	ted.AddChild(dd)
	frmt := add(ted, "FRMT", base64.StdEncoding.EncodeToString(signature))
	frmt.CreateAttr("algoritmo", "SHA1withRSA")
	add(document, "TmstFirma", timestamp)

	context, err := dsig.NewSigningContext(credentials.SigningKey, [][]byte{credentials.Certificate.Raw})
	if err != nil {
		return Document{}, err
	}
	context.IdAttribute = "ID"
	context.Hash = crypto.SHA1 // #nosec G505 -- compatibilidad XMLDSIG exigida por SII.
	context.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = context.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		return Document{}, err
	}
	signed, err := context.SignEnveloped(document)
	if err != nil {
		return Document{}, fmt.Errorf("sign DTE: %w", err)
	}
	dte.RemoveChild(document)
	dte.AddChild(signed)
	doc.CreateProcInst("xml", `version="1.0" encoding="ISO-8859-1"`)
	doc.Indent(2)
	contents, err := doc.WriteToBytes()
	if err != nil {
		return Document{}, err
	}
	contents, err = latin1(contents)
	if err != nil {
		return Document{}, fmt.Errorf("encode DTE: %w", err)
	}
	return Document{XML: contents, Folio: sale.Folio, IssuerRUT: credentials.IssuerRUT, Amount: sale.Amount}, nil
}

func latin1(input []byte) ([]byte, error) {
	output, _, err := transform.Bytes(charmap.ISO8859_1.NewEncoder(), input)
	return output, err
}

func add(parent *etree.Element, name, value string) *etree.Element {
	element := parent.CreateElement(name)
	element.SetText(value)
	return element
}

func elementBytes(element *etree.Element) ([]byte, error) {
	document := etree.NewDocument()
	document.SetRoot(element.Copy())
	return document.WriteToBytes()
}

func receiverRUT(rut string) string {
	if strings.TrimSpace(rut) == "" {
		return "66666666-6"
	}
	return strings.TrimSpace(rut)
}

func truncate(value string, maximum int) string {
	characters := []rune(value)
	if len(characters) <= maximum {
		return value
	}
	return string(characters[:maximum])
}
