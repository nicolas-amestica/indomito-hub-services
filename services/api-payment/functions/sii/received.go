package sii

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ReceivedEvent representa un evento oficial del registro de aceptación/reclamo.
type ReceivedEvent struct {
	Code        string `xml:"codEvento" json:"code"`
	Description string `xml:"descEvento" json:"description"`
	RUT         string `xml:"rutResponsable" json:"rut"`
	DV          string `xml:"dvResponsable" json:"dv"`
	At          string `xml:"fechaEvento" json:"at"`
}

// ReceivedResult conserva el código de negocio; HTTP 200 no acredita aceptación.
type ReceivedResult struct {
	Code        *int            `xml:"codResp" json:"code"`
	Description string          `xml:"descResp" json:"description"`
	Events      []ReceivedEvent `xml:"listaEventosDoc" json:"events"`
}

// ReceivedRegistry consulta o registra eventos usando autenticación DTE tradicional.
func (c Client) ReceivedRegistry(ctx context.Context, credentials Credentials, issuer string, kind int, folio int64, action string) (ReceivedResult, error) {
	if c.HTTP == nil || c.Now == nil || (kind != 33 && kind != 34 && kind != 43) || folio < 1 || folio > 9999999999 {
		return ReceivedResult{}, ErrInvalidConfiguration
	}
	rut, dv, ok := splitRUT(issuer)
	if !ok {
		return ReceivedResult{}, ErrInvalidConfiguration
	}
	method := "listarEventosHistDoc"
	if action != "" {
		switch action {
		case "ACD", "ERM", "RCD", "RFP", "RFT":
		default:
			return ReceivedResult{}, ErrInvalidConfiguration
		}
		method = "ingresarAceptacionReclamoDoc"
	}
	host, registry := "maullin.sii.cl", "https://ws2.sii.cl/WSREGISTRORECLAMODTECERT/registroreclamodteservice"
	if credentials.Environment == EnvironmentProduction {
		host, registry = "palena.sii.cl", "https://ws1.sii.cl/WSREGISTRORECLAMODTE/registroreclamodteservice"
	}
	seedBody := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><n:getSeed xmlns:n="http://DefaultNamespace"/></s:Body></s:Envelope>`
	raw, err := c.soap(ctx, "https://"+host+"/DTEWS/CrSeed.jws", []byte(seedBody))
	if err != nil {
		return ReceivedResult{}, err
	}
	seed, err := nestedValue(raw, "SEMILLA")
	if err != nil {
		return ReceivedResult{}, err
	}
	signed, err := signedSeedXML(credentials, seed)
	if err != nil {
		return ReceivedResult{}, err
	}
	escape := func(v string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(v)); return b.String() }
	tokenBody := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><n:getToken xmlns:n="http://DefaultNamespace"><pszXml>` + escape(string(signed)) + `</pszXml></n:getToken></s:Body></s:Envelope>`
	raw, err = c.soap(ctx, "https://"+host+"/DTEWS/GetTokenFromSeed.jws", []byte(tokenBody))
	if err != nil {
		return ReceivedResult{}, err
	}
	token, err := tokenFromResponse(raw)
	if err != nil {
		return ReceivedResult{}, err
	}
	body := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><n:` + method + ` xmlns:n="http://ws.registroreclamodte.diii.sdi.sii.cl"><rutEmisor>` + escape(rut) + `</rutEmisor><dvEmisor>` + escape(dv) + `</dvEmisor><tipoDoc>` + strconv.Itoa(kind) + `</tipoDoc><folio>` + strconv.FormatInt(folio, 10) + `</folio>`
	if action != "" {
		body += `<accionDoc>` + action + `</accionDoc>`
	}
	body += `</n:` + method + `></s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registry, strings.NewReader(body))
	if err != nil {
		return ReceivedResult{}, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `""`)
	req.Header.Set("Cookie", "TOKEN="+token)
	raw, err = c.do(req)
	if err != nil {
		return ReceivedResult{}, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	for {
		t, e := decoder.Token()
		if e != nil {
			return ReceivedResult{}, errors.New("respuesta de registro SII incompleta")
		}
		if start, yes := t.(xml.StartElement); yes {
			if start.Name.Local == "Fault" {
				return ReceivedResult{}, errors.New("SOAP fault de registro SII")
			}
			if start.Name.Local == "return" {
				var result ReceivedResult
				if e = decoder.DecodeElement(&result, &start); e != nil || result.Code == nil {
					return ReceivedResult{}, fmt.Errorf("respuesta de registro SII inválida")
				}
				if result.Events == nil {
					result.Events = []ReceivedEvent{}
				}
				return result, nil
			}
		}
	}
}
