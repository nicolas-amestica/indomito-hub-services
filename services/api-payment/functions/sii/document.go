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
	Items       []SaleItem
	Reference   string
}

type SaleItem struct {
	Description string
	Quantity    int64
	UnitPrice   int64
	Exempt      bool
	Unit        string
}

type Document struct {
	XML       []byte
	Folio     int64
	IssuerRUT string
	Amount    int64
}

// PublicDocumentData contiene solo los datos tributarios publicables de un DTE firmado.
type PublicDocumentData struct {
	NetAmount    int64
	VATAmount    int64
	ExemptAmount int64
	TED          string
}

// ExtractPublicDocumentData obtiene totales y TED sin exponer llaves ni configuracion.
func ExtractPublicDocumentData(document Document) (PublicDocumentData, error) {
	decoded, err := decodeLatin1(document.XML)
	if err != nil {
		return PublicDocumentData{}, err
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(decoded); err != nil {
		return PublicDocumentData{}, ErrInvalidConfiguration
	}
	readAmount := func(path string) (int64, error) {
		element := parsed.FindElement(path)
		if element == nil {
			return 0, nil
		}
		return strconv.ParseInt(strings.TrimSpace(element.Text()), 10, 64)
	}
	netAmount, netErr := readAmount("//Totales/MntNeto")
	vatAmount, vatErr := readAmount("//Totales/IVA")
	exemptAmount, exemptErr := readAmount("//Totales/MntExe")
	ted := parsed.FindElement("//TED")
	if netErr != nil || vatErr != nil || exemptErr != nil || ted == nil {
		return PublicDocumentData{}, ErrInvalidConfiguration
	}
	tedBytes, err := elementBytes(ted)
	if err != nil {
		return PublicDocumentData{}, err
	}
	return PublicDocumentData{NetAmount: netAmount, VATAmount: vatAmount, ExemptAmount: exemptAmount, TED: string(tedBytes)}, nil
}

func BuildDocument(credentials Credentials, sale Sale, now time.Time) (Document, error) {
	if sale.RequestID == "" || sale.Folio < credentials.FolioFrom || sale.Folio > credentials.FolioTo || sale.Amount <= 0 || sale.IssueDate.IsZero() || now.IsZero() {
		return Document{}, ErrInvalidConfiguration
	}
	items := sale.Items
	if len(items) == 0 {
		items = []SaleItem{{Description: sale.Description, Quantity: 1, UnitPrice: sale.Amount}}
	}
	var netAmount, vatAmount, exemptAmount, total int64
	for _, item := range items {
		if strings.TrimSpace(item.Description) == "" || item.Quantity <= 0 || item.UnitPrice <= 0 {
			return Document{}, ErrInvalidConfiguration
		}
		lineTotal := item.Quantity * item.UnitPrice
		total += lineTotal
		if item.Exempt {
			exemptAmount += lineTotal
		} else {
			lineNet := (lineTotal*100 + 59) / 119
			netAmount += lineNet
			vatAmount += lineTotal - lineNet
		}
	}
	if total != sale.Amount {
		return Document{}, ErrInvalidConfiguration
	}
	documentID := "B39F" + strconv.FormatInt(sale.Folio, 10)
	doc := etree.NewDocument()
	doc.CreateProcInst("xml", `version="1.0" encoding="ISO-8859-1"`)
	dte := doc.CreateElement("DTE")
	dte.CreateAttr("version", "1.0")
	dte.CreateAttr("xmlns", "http://www.sii.cl/SiiDte")
	// EnvioBOLETA declara xsi en su raíz. La canonicalización inclusiva del SII
	// hereda ese namespace al Documento; debe existir también al firmar el DTE
	// aislado para que el digest no cambie al insertarlo en el sobre.
	dte.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
	document := dte.CreateElement("Documento")
	document.CreateAttr("ID", documentID)
	header := document.CreateElement("Encabezado")
	idDoc := header.CreateElement("IdDoc")
	add(idDoc, "TipoDTE", "39")
	add(idDoc, "Folio", strconv.FormatInt(sale.Folio, 10))
	add(idDoc, "FchEmis", sale.IssueDate.Format(time.DateOnly))
	add(idDoc, "IndServicio", "3")
	emitter := header.CreateElement("Emisor")
	add(emitter, "RUTEmisor", credentials.IssuerRUT)
	add(emitter, "RznSocEmisor", credentials.IssuerName)
	add(emitter, "GiroEmisor", credentials.IssuerActivity)
	add(emitter, "DirOrigen", credentials.IssuerAddress)
	add(emitter, "CmnaOrigen", credentials.IssuerCommune)
	receiver := header.CreateElement("Receptor")
	add(receiver, "RUTRecep", receiverRUT(sale.CustomerRUT))
	add(receiver, "RznSocRecep", "CONSUMIDOR FINAL")
	totals := header.CreateElement("Totales")
	if netAmount > 0 {
		add(totals, "MntNeto", strconv.FormatInt(netAmount, 10))
	}
	if exemptAmount > 0 {
		add(totals, "MntExe", strconv.FormatInt(exemptAmount, 10))
	}
	if netAmount > 0 {
		add(totals, "IVA", strconv.FormatInt(vatAmount, 10))
	}
	add(totals, "MntTotal", strconv.FormatInt(sale.Amount, 10))
	for index, item := range items {
		detail := document.CreateElement("Detalle")
		add(detail, "NroLinDet", strconv.Itoa(index+1))
		if item.Exempt {
			add(detail, "IndExe", "1")
		}
		add(detail, "NmbItem", strings.TrimSpace(item.Description))
		add(detail, "QtyItem", strconv.FormatInt(item.Quantity, 10))
		if strings.TrimSpace(item.Unit) != "" {
			add(detail, "UnmdItem", strings.TrimSpace(item.Unit))
		}
		add(detail, "PrcItem", strconv.FormatInt(item.UnitPrice, 10))
		add(detail, "MontoItem", strconv.FormatInt(item.Quantity*item.UnitPrice, 10))
	}
	if strings.TrimSpace(sale.Reference) != "" {
		reference := document.CreateElement("Referencia")
		add(reference, "NroLinRef", "1")
		add(reference, "CodRef", "SET")
		add(reference, "RazonRef", strings.TrimSpace(sale.Reference))
	}
	// El SII exige que el string firmado del TED sea DD compacto. Se puede
	// indentar el resto del DTE, pero debe hacerse antes de incorporar DD.
	doc.Indent(2)

	location, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return Document{}, fmt.Errorf("load Chile timezone: %w", err)
	}
	timestamp := now.In(location).Format("2006-01-02T15:04:05")
	dd := etree.NewElement("DD")
	add(dd, "RE", credentials.IssuerRUT)
	add(dd, "TD", "39")
	add(dd, "F", strconv.FormatInt(sale.Folio, 10))
	add(dd, "FE", sale.IssueDate.Format(time.DateOnly))
	add(dd, "RR", receiverRUT(sale.CustomerRUT))
	add(dd, "RSR", "CONSUMIDOR FINAL")
	add(dd, "MNT", strconv.FormatInt(sale.Amount, 10))
	add(dd, "IT1", truncate(strings.TrimSpace(items[0].Description), 40))
	caf := credentials.CAF.Copy()
	removeFormattingWhitespace(caf)
	dd.AddChild(caf)
	add(dd, "TSTED", timestamp)
	ted := document.CreateElement("TED")
	ted.CreateAttr("version", "1.0")
	ted.AddChild(dd)
	frmt := add(ted, "FRMT", "")
	frmt.CreateAttr("algoritmo", "SHA1withRSA")
	add(document, "TmstFirma", timestamp)
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
	frmt.SetText(base64.StdEncoding.EncodeToString(signature))

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
	signatureElement, err := context.ConstructSignature(document, false)
	if err != nil {
		return Document{}, fmt.Errorf("sign DTE: %w", err)
	}
	if err = addRSAKeyValue(signatureElement, credentials.SigningKey); err != nil {
		return Document{}, err
	}
	dte.AddChild(signatureElement)
	wrapXMLSignatureBase64(signatureElement)
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

func removeFormattingWhitespace(element *etree.Element) {
	children := element.Child[:0]
	for _, token := range element.Child {
		if text, ok := token.(*etree.CharData); ok && strings.TrimSpace(text.Data) == "" {
			continue
		}
		if child, ok := token.(*etree.Element); ok {
			removeFormattingWhitespace(child)
		}
		children = append(children, token)
	}
	element.Child = children
}

func wrapXMLSignatureBase64(signature *etree.Element) {
	// Los ejemplos oficiales de SII mantienen SignatureValue en una sola línea
	// y pliegan únicamente el certificado, sin saltos iniciales ni finales.
	// El parser legado de Maullín rechaza otras representaciones equivalentes.
	element := signature.FindElement("KeyInfo/X509Data/X509Certificate")
	if element == nil {
		return
	}
	value := strings.Join(strings.Fields(element.Text()), "")
	if value == "" {
		return
	}
	lines := make([]string, 0, (len(value)+75)/76)
	for len(value) > 76 {
		lines = append(lines, value[:76])
		value = value[76:]
	}
	lines = append(lines, value)
	element.SetText(strings.Join(lines, "\n"))
}

func latin1(input []byte) ([]byte, error) {
	output, _, err := transform.Bytes(charmap.ISO8859_1.NewEncoder(), input)
	return output, err
}

func decodeLatin1(input []byte) ([]byte, error) {
	output, _, err := transform.Bytes(charmap.ISO8859_1.NewDecoder(), input)
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
