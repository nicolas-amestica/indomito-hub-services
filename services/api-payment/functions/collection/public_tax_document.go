package collection

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

type PublicTaxDocumentItem struct {
	Description string `json:"description" dynamodbav:"description"`
	Quantity    int64  `json:"quantity" dynamodbav:"quantity"`
	UnitPrice   int64  `json:"unitPrice" dynamodbav:"unitPrice"`
	Exempt      bool   `json:"exempt" dynamodbav:"exempt"`
	Unit        string `json:"unit,omitempty" dynamodbav:"unit,omitempty"`
}

// PublicTaxDocument es una proyeccion de solo lectura, separada del pago y sin datos administrativos.
type PublicTaxDocument struct {
	Authentic        bool                    `json:"authentic" dynamodbav:"-"`
	DocumentType     int                     `json:"documentType" dynamodbav:"documentType"`
	Folio            int64                   `json:"folio" dynamodbav:"folio"`
	IssueDate        string                  `json:"issueDate" dynamodbav:"issueDate"`
	IssuerRUT        string                  `json:"issuerRut" dynamodbav:"issuerRut"`
	IssuerName       string                  `json:"issuerName" dynamodbav:"issuerName"`
	IssuerActivity   string                  `json:"issuerActivity" dynamodbav:"issuerActivity"`
	IssuerAddress    string                  `json:"issuerAddress" dynamodbav:"issuerAddress"`
	IssuerCommune    string                  `json:"issuerCommune" dynamodbav:"issuerCommune"`
	ResolutionDate   string                  `json:"resolutionDate" dynamodbav:"resolutionDate"`
	ResolutionNumber int64                   `json:"resolutionNumber" dynamodbav:"resolutionNumber"`
	NetAmount        int64                   `json:"netAmount" dynamodbav:"netAmount"`
	VATAmount        int64                   `json:"vatAmount" dynamodbav:"vatAmount"`
	ExemptAmount     int64                   `json:"exemptAmount" dynamodbav:"exemptAmount"`
	TotalAmount      int64                   `json:"totalAmount" dynamodbav:"totalAmount"`
	Items            []PublicTaxDocumentItem `json:"items" dynamodbav:"items"`
	TED              string                  `json:"ted" dynamodbav:"ted"`
	SignedXMLBase64  string                  `json:"-" dynamodbav:"signedXmlBase64"`
	SignedXMLSHA256  string                  `json:"-" dynamodbav:"signedXmlSha256"`
}

func buildPublicTaxDocument(credentials sii.Credentials, request TaxDocumentRequest, document sii.Document) (PublicTaxDocument, error) {
	data, err := sii.ExtractPublicDocumentData(document)
	if err != nil || request.SIIIssuance == nil || document.Folio != request.SIIIssuance.Folio {
		return PublicTaxDocument{}, sii.ErrInvalidConfiguration
	}
	items := make([]PublicTaxDocumentItem, 0, len(data.Items))
	for _, item := range data.Items {
		items = append(items, PublicTaxDocumentItem{Description: item.Description, Quantity: item.Quantity, UnitPrice: item.UnitPrice, Exempt: item.Exempt, Unit: item.Unit})
	}
	digest := sha256.Sum256(document.XML)
	return PublicTaxDocument{Authentic: true, DocumentType: 39, Folio: data.Folio, IssueDate: data.IssueDate, IssuerRUT: data.IssuerRUT, IssuerName: data.IssuerName, IssuerActivity: data.IssuerActivity, IssuerAddress: data.IssuerAddress, IssuerCommune: data.IssuerCommune, ResolutionDate: credentials.ResolutionDate, ResolutionNumber: credentials.ResolutionNumber, NetAmount: data.NetAmount, VATAmount: data.VATAmount, ExemptAmount: data.ExemptAmount, TotalAmount: data.TotalAmount, Items: items, TED: data.TED, SignedXMLBase64: base64.StdEncoding.EncodeToString(document.XML), SignedXMLSHA256: hex.EncodeToString(digest[:])}, nil
}

type PublicTaxDocumentQuery struct {
	Folio     string `json:"folio"`
	IssueDate string `json:"issueDate"`
	Amount    int64  `json:"amount"`
}

func (a PublicApp) HandleTaxDocumentVerification(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return portalResponse(405, PublicTaxDocument{Authentic: false}), nil
	}
	ip, err := networkIdentity(req.RequestContext.HTTP.SourceIP)
	if err != nil || a.consumeAttempt(ctx, "tax-document-verification-ip:v1", ip, 20, time.Now().UTC()) != nil {
		return portalResponse(429, PublicTaxDocument{Authentic: false}), nil
	}
	var body PublicTaxDocumentQuery
	if len(req.Body) > 512 || decodeAdmin(req, &body) != nil || len(body.Folio) > 10 || body.Amount <= 0 {
		return portalResponse(200, PublicTaxDocument{Authentic: false}), nil
	}
	folio, parseErr := strconv.ParseInt(strings.TrimSpace(body.Folio), 10, 64)
	if parseErr != nil || folio < 1 {
		return portalResponse(200, PublicTaxDocument{Authentic: false}), nil
	}
	if _, dateErr := time.Parse(time.DateOnly, body.IssueDate); dateErr != nil {
		return portalResponse(200, PublicTaxDocument{Authentic: false}), nil
	}
	row, err := a.Accounts.read(ctx, "TAX_PUBLIC#39#"+strconv.FormatInt(folio, 10), "META")
	if errors.Is(err, ErrNotFound) || (err == nil && (row.PublicTaxDocument == nil || row.PublicTaxDocument.IssueDate != body.IssueDate || row.PublicTaxDocument.TotalAmount != body.Amount)) {
		return portalResponse(200, PublicTaxDocument{Authentic: false}), nil
	}
	if err != nil || row.PublicTaxDocument == nil {
		return portalResponse(503, PublicTaxDocument{Authentic: false}), nil
	}
	result := *row.PublicTaxDocument
	result.Authentic = true
	return lambdautil.SuccessResponseWithHeaders(200, result, map[string]string{"cache-control": "no-store", "referrer-policy": "no-referrer", "x-content-type-options": "nosniff"})
}

func TaxDocumentVerificationHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getReceiptVerificationApp(ctx)
	if err != nil {
		return portalResponse(503, PublicTaxDocument{Authentic: false}), nil
	}
	return app.HandleTaxDocumentVerification(ctx, req)
}
