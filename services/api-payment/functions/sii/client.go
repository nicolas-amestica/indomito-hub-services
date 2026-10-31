package sii

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

const maxSIIResponseBytes = 2 * 1024 * 1024

type Submission struct {
	TrackID string
	Status  string
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	HTTP HTTPClient
	Now  func() time.Time
}

func (c Client) Submit(ctx context.Context, credentials Credentials, document Document) (Submission, error) {
	if c.HTTP == nil || c.Now == nil || len(document.XML) == 0 || document.IssuerRUT != credentials.IssuerRUT {
		return Submission{}, ErrInvalidConfiguration
	}
	seed, err := c.seed(ctx, credentials.Environment)
	if err != nil {
		return Submission{}, err
	}
	token, err := c.token(ctx, credentials, seed)
	if err != nil {
		return Submission{}, err
	}
	envelope, err := buildEnvelope(credentials, document.XML, c.Now())
	if err != nil {
		return Submission{}, fmt.Errorf("build SII envelope: %w", err)
	}
	submission, err := c.upload(ctx, credentials, token, envelope)
	if err != nil {
		return Submission{}, fmt.Errorf("upload SII envelope: %w", err)
	}
	return submission, nil
}

func endpoints(environment Environment) (string, string, string) {
	if environment == EnvironmentProduction {
		return "https://api.sii.cl/recursos/v1/boleta.electronica.semilla", "https://api.sii.cl/recursos/v1/boleta.electronica.token", "https://rahue.sii.cl/recursos/v1/boleta.electronica.envio"
	}
	return "https://apicert.sii.cl/recursos/v1/boleta.electronica.semilla", "https://apicert.sii.cl/recursos/v1/boleta.electronica.token", "https://pangal.sii.cl/recursos/v1/boleta.electronica.envio"
}

func (c Client) seed(ctx context.Context, environment Environment) (string, error) {
	if environment == EnvironmentCertification {
		return c.soapSeed(ctx)
	}
	seedURL, _, _ := endpoints(environment)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, seedURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/xml")
	response, err := c.do(request)
	if err != nil {
		return "", err
	}
	return nestedValue(response, "SEMILLA")
}

func (c Client) token(ctx context.Context, credentials Credentials, seed string) (string, error) {
	signedXML, err := signedSeedXML(credentials, seed)
	if err != nil {
		return "", err
	}
	if credentials.Environment == EnvironmentCertification {
		return c.soapToken(ctx, signedXML)
	}
	_, tokenURL, _ := endpoints(credentials.Environment)
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(signedXML))
	if err != nil {
		return "", err
	}
	tokenRequest.Header.Set("Content-Type", "application/xml")
	tokenRequest.Header.Set("Accept", "application/xml")
	response, err := c.do(tokenRequest)
	if err != nil {
		return "", err
	}
	return tokenFromResponse(response)
}

func signedSeedXML(credentials Credentials, seed string) ([]byte, error) {
	document := etree.NewDocument()
	request := document.CreateElement("getToken")
	item := request.CreateElement("item")
	add(item, "Semilla", strings.TrimSpace(seed))
	context, err := dsig.NewSigningContext(credentials.SigningKey, [][]byte{credentials.Certificate.Raw})
	if err != nil {
		return nil, err
	}
	context.Hash = crypto.SHA1 // #nosec G505 -- servicio legado de autenticacion SII.
	context.Prefix = ""
	context.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = context.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		return nil, err
	}
	signed, err := context.SignEnveloped(request)
	if err != nil {
		return nil, fmt.Errorf("sign seed: %w", err)
	}
	if err = addRSAKeyValue(signed, credentials.SigningKey); err != nil {
		return nil, err
	}
	signedXML, err := elementBytes(signed)
	if err != nil {
		return nil, err
	}
	signedXML = append([]byte(`<?xml version="1.0" encoding="UTF-8"?>`), signedXML...)
	return signedXML, nil
}

func tokenFromResponse(response []byte) (string, error) {
	tokenValue, err := nestedValue(response, "TOKEN")
	if err == nil && tokenValue != "" {
		return tokenValue, nil
	}
	status, _ := nestedValue(response, "ESTADO")
	detail, _ := nestedValue(response, "GLOSA")
	if detail == "" {
		detail, _ = nestedValue(response, "MENSAJE")
	}
	return "", fmt.Errorf("SII token rejected: estado=%q detalle=%q: %w", truncateDiagnostic(status, 20), truncateDiagnostic(detail, 160), err)
}

func (c Client) soapSeed(ctx context.Context) (string, error) {
	const body = `<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><ns1:getSeed soapenv:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/" xmlns:ns1="http://DefaultNamespace"/></soapenv:Body></soapenv:Envelope>`
	response, err := c.soap(ctx, "https://maullin.sii.cl/DTEWS/CrSeed.jws", []byte(body))
	if err != nil {
		return "", err
	}
	return nestedValue(response, "SEMILLA")
}

func (c Client) soapToken(ctx context.Context, signedXML []byte) (string, error) {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, signedXML); err != nil {
		return "", err
	}
	body := `<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><soapenv:Body><ns1:getToken soapenv:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/" xmlns:ns1="http://DefaultNamespace"><pszXml xsi:type="xsd:string">` + escaped.String() + `</pszXml></ns1:getToken></soapenv:Body></soapenv:Envelope>`
	response, err := c.soap(ctx, "https://maullin.sii.cl/DTEWS/GetTokenFromSeed.jws", []byte(body))
	if err != nil {
		return "", err
	}
	return tokenFromResponse(response)
}

func (c Client) soap(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "text/xml; charset=utf-8")
	request.Header.Set("SOAPAction", `""`)
	request.Header.Set("Accept", "text/xml")
	return c.do(request)
}

func addRSAKeyValue(signed *etree.Element, key *rsa.PrivateKey) error {
	if key == nil || key.N == nil || key.E <= 0 {
		return ErrInvalidConfiguration
	}
	publicKey := &key.PublicKey
	keyInfo := signed.FindElement("//*[local-name()='KeyInfo']")
	if keyInfo == nil {
		return fmt.Errorf("signed seed has no KeyInfo")
	}
	keyValue := etree.NewElement("KeyValue")
	keyValue.Space = keyInfo.Space
	rsaKeyValue := keyValue.CreateElement("RSAKeyValue")
	rsaKeyValue.Space = keyInfo.Space
	modulus := add(rsaKeyValue, "Modulus", base64.StdEncoding.EncodeToString(publicKey.N.Bytes()))
	modulus.Space = keyInfo.Space
	exponent := make([]byte, 8)
	binary.BigEndian.PutUint64(exponent, uint64(publicKey.E))
	exponent = bytes.TrimLeft(exponent, "\x00")
	exponentElement := add(rsaKeyValue, "Exponent", base64.StdEncoding.EncodeToString(exponent))
	exponentElement.Space = keyInfo.Space
	keyInfo.InsertChildAt(0, keyValue)
	return nil
}

func truncateDiagnostic(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func (c Client) do(request *http.Request) ([]byte, error) {
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("SII request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSIIResponseBytes+1))
	if err != nil || len(body) > maxSIIResponseBytes || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("SII response status %d", response.StatusCode)
	}
	return body, nil
}

func (c Client) upload(ctx context.Context, credentials Credentials, token string, envelope []byte) (Submission, error) {
	_, _, endpoint := endpoints(credentials.Environment)
	if credentials.Environment == EnvironmentCertification {
		endpoint = "https://maullin.sii.cl/cgi_dte/UPL/DTEUpload"
	}
	issuerNumber, issuerDV, ok := splitRUT(credentials.IssuerRUT)
	senderNumber, senderDV, senderOK := splitRUT(credentials.SenderRUT)
	if !ok || !senderOK || token == "" {
		return Submission{}, ErrInvalidConfiguration
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{"rutSender": senderNumber, "dvSender": senderDV, "rutCompany": issuerNumber, "dvCompany": issuerDV} {
		if err := writer.WriteField(name, value); err != nil {
			return Submission{}, err
		}
	}
	header := textproto.MIMEHeader{}
	filename := "boleta.xml"
	if credentials.Environment == EnvironmentCertification {
		filename = "EnvioBOLETA.xml"
	}
	header.Set("Content-Disposition", `form-data; name="archivo"; filename="`+filename+`"`)
	header.Set("Content-Type", "text/xml")
	part, err := writer.CreatePart(header)
	if err != nil {
		return Submission{}, err
	}
	if _, err = part.Write(envelope); err != nil {
		return Submission{}, err
	}
	if err = writer.Close(); err != nil {
		return Submission{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return Submission{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Cookie", "TOKEN="+token)
	request.Header.Set("User-Agent", "Mozilla/4.0 ( compatible; PROG 1.0; Windows NT)")
	if credentials.Environment == EnvironmentCertification {
		request.Header.Set("Accept", "*/*")
		request.Header.Set("Connection", "keep-alive")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return Submission{}, fmt.Errorf("upload SII: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxSIIResponseBytes+1))
	if err != nil || len(contents) > maxSIIResponseBytes || response.StatusCode < 200 || response.StatusCode >= 300 {
		return Submission{}, fmt.Errorf("SII upload status %d: %s", response.StatusCode, responseDiagnostic(contents))
	}
	var payload struct {
		TrackID json.Number `json:"trackid"`
		Error   string      `json:"error"`
		Message string      `json:"mensaje"`
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	if err = decoder.Decode(&payload); err != nil || payload.TrackID.String() == "" {
		status, statusErr := nestedValue(contents, "STATUS")
		trackID, trackErr := nestedValue(contents, "TRACKID")
		if statusErr != nil || trackErr != nil || status != "0" || trackID == "" {
			detail, _ := nestedValue(contents, "GLOSA")
			if detail == "" {
				detail = responseDiagnostic(contents)
			}
			return Submission{}, fmt.Errorf("SII rejected upload: status=%q detail=%q", truncateDiagnostic(status, 20), truncateDiagnostic(detail, 1000))
		}
		return Submission{TrackID: trackID, Status: "SUBMITTED"}, nil
	}
	return Submission{TrackID: payload.TrackID.String(), Status: "SUBMITTED"}, nil
}

func responseDiagnostic(contents []byte) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"mensaje"`
	}
	if json.Unmarshal(contents, &payload) == nil {
		if payload.Error != "" {
			return truncateDiagnostic(payload.Error, 200)
		}
		if payload.Message != "" {
			return truncateDiagnostic(payload.Message, 200)
		}
	}
	return truncateDiagnostic(strings.Join(strings.Fields(string(contents)), " "), 1000)
}

func buildEnvelope(credentials Credentials, dteXML []byte, now time.Time) ([]byte, error) {
	decodedDTE, err := decodeLatin1(dteXML)
	if err != nil {
		return nil, fmt.Errorf("decode DTE XML: %w", err)
	}
	dteDocument := etree.NewDocument()
	if err = dteDocument.ReadFromBytes(decodedDTE); err != nil {
		return nil, fmt.Errorf("parse DTE XML: %w", err)
	}
	if dteDocument.Root() == nil {
		return nil, fmt.Errorf("parse DTE XML: %w", ErrInvalidConfiguration)
	}
	document := etree.NewDocument()
	document.CreateProcInst("xml", `version="1.0" encoding="ISO-8859-1"`)
	envelope := document.CreateElement("EnvioBOLETA")
	envelope.CreateAttr("xmlns", "http://www.sii.cl/SiiDte")
	envelope.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
	envelope.CreateAttr("xsi:schemaLocation", "http://www.sii.cl/SiiDte EnvioBOLETA_v11.xsd")
	envelope.CreateAttr("version", "1.0")
	set := envelope.CreateElement("SetDTE")
	set.CreateAttr("ID", "SetDoc")
	cover := set.CreateElement("Caratula")
	cover.CreateAttr("version", "1.0")
	add(cover, "RutEmisor", credentials.IssuerRUT)
	add(cover, "RutEnvia", credentials.SenderRUT)
	add(cover, "RutReceptor", "60803000-K")
	add(cover, "FchResol", credentials.ResolutionDate)
	add(cover, "NroResol", strconv.FormatInt(credentials.ResolutionNumber, 10))
	location, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return nil, fmt.Errorf("load Chile timezone: %w", err)
	}
	add(cover, "TmstFirmaEnv", now.In(location).Format("2006-01-02T15:04:05"))
	subtotal := cover.CreateElement("SubTotDTE")
	add(subtotal, "TpoDTE", strconv.Itoa(documentType))
	add(subtotal, "NroDTE", "1")
	set.AddChild(dteDocument.Root().Copy())
	context, err := dsig.NewSigningContext(credentials.SigningKey, [][]byte{credentials.Certificate.Raw})
	if err != nil {
		return nil, err
	}
	context.IdAttribute = "ID"
	context.Hash = crypto.SHA1 // #nosec G505 -- formato XMLDSIG del SII.
	context.Prefix = ""
	context.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = context.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		return nil, err
	}
	signatureElement, err := context.ConstructSignature(set, false)
	if err != nil {
		return nil, fmt.Errorf("sign envelope: %w", err)
	}
	if err = addRSAKeyValue(signatureElement, credentials.SigningKey); err != nil {
		return nil, err
	}
	envelope.AddChild(signatureElement)
	document.Indent(2)
	contents, err := document.WriteToBytes()
	if err != nil {
		return nil, err
	}
	return latin1(contents)
}

func nestedValue(payload []byte, field string) (string, error) {
	var value struct {
		Text string `xml:",chardata"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	nestedPayloads := make([]string, 0, 2)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			for _, nested := range nestedPayloads {
				if result, nestedErr := nestedValue([]byte(nested), field); nestedErr == nil {
					return result, nil
				}
			}
			return "", fmt.Errorf("missing SII field %s", field)
		}
		if err != nil {
			return "", err
		}
		start, ok := token.(xml.StartElement)
		if ok && strings.EqualFold(start.Name.Local, field) {
			if err = decoder.DecodeElement(&value, &start); err != nil {
				return "", err
			}
			return strings.TrimSpace(value.Text), nil
		}
		if characters, ok := token.(xml.CharData); ok {
			candidate := strings.TrimSpace(string(characters))
			if strings.HasPrefix(candidate, "<") && strings.HasSuffix(candidate, ">") {
				nestedPayloads = append(nestedPayloads, candidate)
			}
		}
	}
}

func splitRUT(value string) (string, string, bool) {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(value), ".", ""), "-")
	returnPart := ""
	if len(parts) == 2 {
		returnPart = strings.ToUpper(parts[1])
	}
	return func() (string, string, bool) {
		return parts[0], returnPart, len(parts) == 2 && parts[0] != "" && returnPart != ""
	}()
}
