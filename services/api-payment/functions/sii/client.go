package sii

import (
	"bytes"
	"context"
	"crypto"
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
		return Submission{}, err
	}
	return c.upload(ctx, credentials, token, envelope)
}

func endpoints(environment Environment) (string, string, string) {
	if environment == EnvironmentProduction {
		return "https://api.sii.cl/recursos/v1/boleta.electronica.semilla", "https://api.sii.cl/recursos/v1/boleta.electronica.token", "https://rahue.sii.cl/recursos/v1/boleta.electronica.envio"
	}
	return "https://apicert.sii.cl/recursos/v1/boleta.electronica.semilla", "https://apicert.sii.cl/recursos/v1/boleta.electronica.token", "https://pangal.sii.cl/recursos/v1/boleta.electronica.envio"
}

func (c Client) seed(ctx context.Context, environment Environment) (string, error) {
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
	document := etree.NewDocument()
	request := document.CreateElement("getToken")
	item := request.CreateElement("item")
	add(item, "Semilla", strings.TrimSpace(seed))
	context, err := dsig.NewSigningContext(credentials.SigningKey, [][]byte{credentials.Certificate.Raw})
	if err != nil {
		return "", err
	}
	context.Hash = crypto.SHA1 // #nosec G505 -- servicio legado de autenticacion SII.
	context.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = context.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		return "", err
	}
	signed, err := context.SignEnveloped(request)
	if err != nil {
		return "", fmt.Errorf("sign seed: %w", err)
	}
	signedXML, err := elementBytes(signed)
	if err != nil {
		return "", err
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
	return nestedValue(response, "TOKEN")
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
	header.Set("Content-Disposition", `form-data; name="archivo"; filename="boleta.xml"`)
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
	request.Header.Set("User-Agent", "Mozilla/4.0 ( compatible; IndomitoPay 1.0; Windows NT)")
	request.Header.Set("Accept", "application/json")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return Submission{}, fmt.Errorf("upload SII: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxSIIResponseBytes+1))
	if err != nil || len(contents) > maxSIIResponseBytes || response.StatusCode < 200 || response.StatusCode >= 300 {
		return Submission{}, fmt.Errorf("SII upload status %d", response.StatusCode)
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
			return Submission{}, fmt.Errorf("SII rejected upload")
		}
		return Submission{TrackID: trackID, Status: "SUBMITTED"}, nil
	}
	return Submission{TrackID: payload.TrackID.String(), Status: "SUBMITTED"}, nil
}

func buildEnvelope(credentials Credentials, dteXML []byte, now time.Time) ([]byte, error) {
	dteDocument := etree.NewDocument()
	if err := dteDocument.ReadFromBytes(dteXML); err != nil || dteDocument.Root() == nil {
		return nil, ErrInvalidConfiguration
	}
	document := etree.NewDocument()
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
	add(cover, "FchResol", credentials.ResolutionDate)
	add(cover, "NroResol", strconv.FormatInt(credentials.ResolutionNumber, 10))
	add(cover, "TmstFirmaEnv", now.UTC().Format("2006-01-02T15:04:05"))
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
	context.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = context.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		return nil, err
	}
	signed, err := context.SignEnveloped(set)
	if err != nil {
		return nil, fmt.Errorf("sign envelope: %w", err)
	}
	envelope.RemoveChild(set)
	envelope.AddChild(signed)
	document.CreateProcInst("xml", `version="1.0" encoding="ISO-8859-1"`)
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
