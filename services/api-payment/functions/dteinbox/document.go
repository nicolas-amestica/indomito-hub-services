package dteinbox

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
	"golang.org/x/net/html/charset"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

// Document conserva identidad tributaria, origen y resultados oficiales separados.
type Document struct {
	ID            string              `json:"id"`
	IssuerRUT     string              `json:"issuerRut"`
	IssuerName    string              `json:"issuerName"`
	ReceiverRUT   string              `json:"receiverRut"`
	Kind          int                 `json:"kind"`
	PaymentForm   int                 `json:"paymentForm,omitempty"`
	Folio         int64               `json:"folio"`
	IssueDate     string              `json:"issueDate"`
	Total         int64               `json:"total"`
	ReceivedAt    string              `json:"receivedAt"`
	MessageID     string              `json:"messageId"`
	SHA256        string              `json:"sha256"`
	XMLKey        string              `json:"xmlKey"`
	MailKey       string              `json:"mailKey"`
	Registry      *sii.ReceivedResult `json:"registry,omitempty"`
	LastCheckedAt string              `json:"lastCheckedAt,omitempty"`
}

// Attachment contiene únicamente XML, con límites de tamaño y profundidad MIME.
type Attachment struct {
	Name string
	Raw  []byte
}

var ErrAlias = errors.New("destinatario no corresponde o es ambiguo")
var ErrAmbiguousAlias = errors.New("correo dirigido a ambos ambientes")

// Extract valida el alias antes de permitir el procesamiento de los adjuntos.
func Extract(raw []byte, alias, other string) ([]Attachment, error) {
	if len(raw) > maxMessageBytes {
		return nil, errors.New("correo demasiado grande")
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("MIME inválido")
	}
	found, conflict := false, false
	for _, key := range []string{"Delivered-To", "X-Original-To", "X-Gm-Original-To", "To", "Cc"} {
		for _, value := range message.Header[key] {
			addresses, e := mail.ParseAddressList(value)
			if e != nil {
				continue
			}
			for _, address := range addresses {
				if strings.EqualFold(address.Address, alias) {
					found = true
				}
				if strings.EqualFold(address.Address, other) {
					conflict = true
				}
			}
		}
	}
	if found && conflict {
		return nil, ErrAmbiguousAlias
	}
	if !found {
		return nil, ErrAlias
	}
	var attachments []Attachment
	var walk func(mail.Header, io.Reader, int) error
	walk = func(header mail.Header, body io.Reader, depth int) error {
		if depth > 8 {
			return errors.New("MIME demasiado profundo")
		}
		kind, params, e := mime.ParseMediaType(header.Get("Content-Type"))
		if e != nil {
			return e
		}
		if strings.HasPrefix(kind, "multipart/") {
			reader := multipart.NewReader(body, params["boundary"])
			for {
				part, e := reader.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					return e
				}
				if e = walk(mail.Header(part.Header), part, depth+1); e != nil {
					return e
				}
			}
			return nil
		}
		name := params["name"]
		if _, p, e := mime.ParseMediaType(header.Get("Content-Disposition")); e == nil && p["filename"] != "" {
			name = p["filename"]
		}
		if !strings.HasSuffix(strings.ToLower(name), ".xml") {
			return nil
		}
		if len(attachments) >= 20 {
			return errors.New("demasiados XML")
		}
		switch strings.ToLower(header.Get("Content-Transfer-Encoding")) {
		case "base64":
			body = base64.NewDecoder(base64.StdEncoding, body)
		case "quoted-printable":
			body = quotedprintable.NewReader(body)
		}
		contents, e := io.ReadAll(io.LimitReader(body, 2*1024*1024+1))
		if e != nil || len(contents) > 2*1024*1024 {
			return errors.New("XML demasiado grande o inválido")
		}
		attachments = append(attachments, Attachment{Name: name, Raw: contents})
		return nil
	}
	if err = walk(message.Header, message.Body, 0); err != nil {
		return nil, err
	}
	return attachments, nil
}

func canonicalRUT(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), ".", ""))
}

// ErrSIIResponse identifica respuestas de envío del SII que no contienen DTE.
var ErrSIIResponse = errors.New("respuesta RESULTADO_ENVIO del SII sin DTE")

// ErrHonorariumXML distingue una BHE recibida del formato DTE soportado.
var ErrHonorariumXML = errors.New("boleta de honorarios: formato no soportado")

// ErrConsumptionResponse distingue el resultado de consumo de folios de un DTE.
var ErrConsumptionResponse = errors.New("respuesta SII de consumo de folios")

// ParseDocuments verifica XMLDSIG y extrae los encabezados firmados, nunca XML arbitrario.
func ParseDocuments(raw []byte, receiver string, now time.Time) ([]Document, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.CharsetReader = charset.NewReaderLabel
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("XML inválido")
		}
		if _, ok := token.(xml.Directive); ok {
			return nil, errors.New("directivas XML no permitidas")
		}
	}
	doc := etree.NewDocument()
	doc.ReadSettings.CharsetReader = charset.NewReaderLabel
	if err := doc.ReadFromBytes(raw); err != nil {
		return nil, err
	}
	nodes := doc.FindElements("//DTE")
	if len(nodes) == 0 && doc.Root() != nil {
		root := doc.Root()
		if root.Tag == "ResultadoConsumoFolios" && root.NamespaceURI() == "http://www.sii.cl/SiiDte" {
			return nil, ErrConsumptionResponse
		}
		if root.Tag == "datos" && root.FindElement("numeroBoleta") != nil && root.FindElement("totalHonorarios") != nil && root.FindElement("tipodoc") != nil && strings.TrimSpace(root.FindElement("tipodoc").Text()) == "66" {
			return nil, ErrHonorariumXML
		}
	}
	if len(nodes) == 0 && doc.Root() != nil && doc.Root().Tag == "RESULTADO_ENVIO" {
		return nil, ErrSIIResponse
	}
	if len(nodes) == 0 || len(nodes) > 100 {
		return nil, errors.New("sobre sin DTE o excede límite")
	}
	documents := make([]Document, 0, len(nodes))
	for _, node := range nodes {
		if len(node.FindElements("Documento")) != 1 || len(node.FindElements("Signature")) != 1 {
			return nil, errors.New("DTE con estructura o firma ambigua")
		}
		certNode := node.FindElement("Signature/KeyInfo/X509Data/X509Certificate")
		if certNode == nil {
			return nil, errors.New("certificado de emisor ausente")
		}
		der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(certNode.Text()), ""))
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		if err = verifySignature(node, cert); err != nil {
			return nil, err
		}
		value := func(path string) string {
			n := node.FindElement("Documento/Encabezado/" + path)
			if n == nil {
				return ""
			}
			return strings.TrimSpace(n.Text())
		}
		item := Document{IssuerRUT: canonicalRUT(value("Emisor/RUTEmisor")), IssuerName: value("Emisor/RznSoc"), ReceiverRUT: canonicalRUT(value("Receptor/RUTRecep")), IssueDate: value("IdDoc/FchEmis")}
		item.Kind, err = strconv.Atoi(value("IdDoc/TipoDTE"))
		if err != nil {
			return nil, err
		}
		item.PaymentForm = 2 // El formato SII asume crédito cuando FmaPago está ausente.
		if payment := value("IdDoc/FmaPago"); payment != "" {
			item.PaymentForm, err = strconv.Atoi(payment)
			if err != nil || item.PaymentForm < 1 || item.PaymentForm > 3 {
				return nil, errors.New("forma de pago inválida")
			}
		}
		item.Folio, err = strconv.ParseInt(value("IdDoc/Folio"), 10, 64)
		if err != nil || item.Folio < 1 {
			return nil, errors.New("folio inválido")
		}
		item.Total, err = strconv.ParseInt(value("Totales/MntTotal"), 10, 64)
		if err != nil || item.Total < 0 {
			return nil, errors.New("monto inválido")
		}
		if item.ReceiverRUT != canonicalRUT(receiver) || item.IssuerRUT == "" {
			return nil, errors.New("RUT receptor no autorizado")
		}
		if _, err = time.Parse(time.DateOnly, item.IssueDate); err != nil {
			return nil, err
		}
		identity := fmt.Sprintf("%s|%d|%d|%s", item.IssuerRUT, item.Kind, item.Folio, item.ReceiverRUT)
		digest := sha256.Sum256([]byte(identity))
		item.ID = hex.EncodeToString(digest[:])
		canonical, err := dsig.MakeC14N10RecCanonicalizer().Canonicalize(node.FindElement("Documento"))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(canonical)
		item.SHA256 = hex.EncodeToString(sum[:])
		item.ReceivedAt = now.UTC().Format(time.RFC3339)
		documents = append(documents, item)
	}
	return documents, nil
}

// verifySignature comprueba integridad con el certificado adjunto. La autorización
// tributaria se consulta separadamente en el SII; no implica confianza en su CA.
func verifySignature(node *etree.Element, cert *x509.Certificate) error {
	signature := node.FindElement("Signature")
	info := signature.FindElement("SignedInfo")
	document := node.FindElement("Documento")
	if info == nil || document == nil {
		return errors.New("firma incompleta")
	}
	references := info.FindElements("Reference")
	if len(references) != 1 || document.SelectAttrValue("ID", "") == "" || references[0].SelectAttrValue("URI", "") != "#"+document.SelectAttrValue("ID", "") {
		return errors.New("referencia de firma inválida")
	}
	read := func(parent *etree.Element, path string) *etree.Element { return parent.FindElement(path) }
	algorithm := read(info, "SignatureMethod")
	canon := read(info, "CanonicalizationMethod")
	digestMethod := read(references[0], "DigestMethod")
	digestValue := read(references[0], "DigestValue")
	signatureValue := read(signature, "SignatureValue")
	if algorithm == nil || canon == nil || digestMethod == nil || digestValue == nil || signatureValue == nil || algorithm.SelectAttrValue("Algorithm", "") != dsig.RSASHA1SignatureMethod || canon.SelectAttrValue("Algorithm", "") != "http://www.w3.org/TR/2001/REC-xml-c14n-20010315" || digestMethod.SelectAttrValue("Algorithm", "") != "http://www.w3.org/2000/09/xmldsig#sha1" {
		return errors.New("algoritmo XMLDSIG no soportado")
	}
	for _, transform := range references[0].FindElements("Transforms/Transform") {
		if transform.SelectAttrValue("Algorithm", "") != "http://www.w3.org/TR/2001/REC-xml-c14n-20010315" {
			return errors.New("transformación XML no soportada")
		}
	}
	canonicalizer := dsig.MakeC14N10RecCanonicalizer()
	expected, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(digestValue.Text()), ""))
	if err != nil {
		return errors.New("digest XML inválido")
	}
	value, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(signatureValue.Text()), ""))
	if err != nil {
		return err
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("certificado no RSA")
	}
	verify := func(target, signedInfo *etree.Element) error {
		canonical, e := canonicalizer.Canonicalize(target)
		if e != nil {
			return e
		}
		digest := sha1.Sum(canonical) // #nosec G401 -- contrato XMLDSIG del SII.
		if !bytes.Equal(expected, digest[:]) {
			return errors.New("digest XML inválido")
		}
		canonical, e = canonicalizer.Canonicalize(signedInfo)
		if e != nil {
			return e
		}
		signedDigest := sha1.Sum(canonical) // #nosec G401 -- contrato XMLDSIG del SII.
		if e = rsa.VerifyPKCS1v15(key, crypto.SHA1, signedDigest[:], value); e != nil {
			return errors.New("firma XML inválida")
		}
		return nil
	}
	standardErr := verify(document, info)
	if standardErr == nil {
		return nil
	}
	if namespace := document.NamespaceURI(); namespace != "" && namespace != "http://www.sii.cl/SiiDte" {
		return standardErr
	}
	// Algunos proveedores firman el Documento independiente antes de crear el
	// sobre. Se comprueban ambos valores en ese contexto original, sin cambiar
	// los bytes recibidos ni quitar atributos o transformaciones del Documento.
	// No admitir prefijos ni declaraciones locales que alteren su interpretación.
	var standaloneCompatible func(*etree.Element) bool
	standaloneCompatible = func(element *etree.Element) bool {
		if element.Space != "" {
			return false
		}
		for _, attr := range element.Attr {
			if attr.Space != "" || attr.Key == "xmlns" {
				return false
			}
		}
		for _, child := range element.ChildElements() {
			if !standaloneCompatible(child) {
				return false
			}
		}
		return true
	}
	if !standaloneCompatible(document) {
		return standardErr
	}
	signatureCopy := signature.Copy()
	standaloneInfo := signatureCopy.FindElement("SignedInfo")
	if standaloneInfo == nil {
		return standardErr
	}
	// El Portal MiPyme firma con el namespace SII del DTE, antes de incorporar
	// el sobre que declara xsi. Reconstruir ese contexto conserva el namespace
	// del documento y el de XMLDSIG, sin heredar declaraciones del transporte.
	// La restricción anterior impide retirar namespaces usados por el contenido.
	if document.NamespaceURI() == "http://www.sii.cl/SiiDte" {
		standaloneDocument := document.Copy()
		standaloneDocument.CreateAttr("xmlns", "http://www.sii.cl/SiiDte")
		if err = verify(standaloneDocument, standaloneInfo); err == nil {
			return nil
		}
	}
	if err = verify(document.Copy(), standaloneInfo); err != nil {
		return standardErr
	}
	return nil
}
