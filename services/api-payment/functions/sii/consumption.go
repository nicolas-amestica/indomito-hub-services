package sii

import (
	"context"
	"crypto"
	"fmt"
	"strconv"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

// ConsumptionResume contiene el resumen diario exigido durante la certificacion.
type ConsumptionResume struct {
	Date         time.Time
	Sequence     int64
	NetAmount    int64
	VATAmount    int64
	ExemptAmount int64
	TotalAmount  int64
	FolioFrom    int64
	FolioTo      int64
}

// BuildConsumption genera y firma un RCOF tipo 39 conforme a ConsumoFolio_v10.xsd.
func BuildConsumption(credentials Credentials, resume ConsumptionResume, now time.Time) ([]byte, error) {
	count := resume.FolioTo - resume.FolioFrom + 1
	if resume.Date.IsZero() || now.IsZero() || resume.Sequence < 1 || resume.Sequence > 999 || count < 1 ||
		resume.FolioFrom < credentials.FolioFrom || resume.FolioTo > credentials.FolioTo ||
		resume.NetAmount < 0 || resume.VATAmount < 0 || resume.ExemptAmount < 0 || resume.TotalAmount <= 0 ||
		resume.NetAmount+resume.VATAmount+resume.ExemptAmount != resume.TotalAmount {
		return nil, ErrInvalidConfiguration
	}
	location, err := time.LoadLocation("America/Santiago")
	if err != nil {
		return nil, fmt.Errorf("load Chile timezone: %w", err)
	}
	document := etree.NewDocument()
	document.CreateProcInst("xml", `version="1.0" encoding="ISO-8859-1"`)
	root := document.CreateElement("ConsumoFolios")
	root.CreateAttr("xmlns", "http://www.sii.cl/SiiDte")
	root.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
	root.CreateAttr("xsi:schemaLocation", "http://www.sii.cl/SiiDte ConsumoFolio_v10.xsd")
	root.CreateAttr("version", "1.0")
	signed := root.CreateElement("DocumentoConsumoFolios")
	signed.CreateAttr("ID", "ConsumoFolios")
	cover := signed.CreateElement("Caratula")
	cover.CreateAttr("version", "1.0")
	add(cover, "RutEmisor", credentials.IssuerRUT)
	add(cover, "RutEnvia", credentials.SenderRUT)
	add(cover, "FchResol", credentials.ResolutionDate)
	add(cover, "NroResol", strconv.FormatInt(credentials.ResolutionNumber, 10))
	date := resume.Date.In(location).Format(time.DateOnly)
	add(cover, "FchInicio", date)
	add(cover, "FchFinal", date)
	add(cover, "SecEnvio", strconv.FormatInt(resume.Sequence, 10))
	add(cover, "TmstFirmaEnv", now.In(location).Format("2006-01-02T15:04:05"))
	summary := signed.CreateElement("Resumen")
	add(summary, "TipoDocumento", strconv.Itoa(documentType))
	add(summary, "MntNeto", strconv.FormatInt(resume.NetAmount, 10))
	add(summary, "MntIva", strconv.FormatInt(resume.VATAmount, 10))
	add(summary, "TasaIVA", "19")
	add(summary, "MntExento", strconv.FormatInt(resume.ExemptAmount, 10))
	add(summary, "MntTotal", strconv.FormatInt(resume.TotalAmount, 10))
	add(summary, "FoliosEmitidos", strconv.FormatInt(count, 10))
	add(summary, "FoliosAnulados", "0")
	add(summary, "FoliosUtilizados", strconv.FormatInt(count, 10))
	rangeUsed := summary.CreateElement("RangoUtilizados")
	add(rangeUsed, "Inicial", strconv.FormatInt(resume.FolioFrom, 10))
	add(rangeUsed, "Final", strconv.FormatInt(resume.FolioTo, 10))
	document.Indent(2)
	context, err := dsig.NewSigningContext(credentials.SigningKey, [][]byte{credentials.Certificate.Raw})
	if err != nil {
		return nil, err
	}
	context.IdAttribute = "ID"
	context.Hash = crypto.SHA1 // #nosec G505 -- formato XMLDSIG exigido por el SII.
	context.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	if err = context.SetSignatureMethod(dsig.RSASHA1SignatureMethod); err != nil {
		return nil, err
	}
	signature, err := context.ConstructSignature(signed, false)
	if err != nil {
		return nil, fmt.Errorf("sign consumption: %w", err)
	}
	if err = addRSAKeyValue(signature, credentials.SigningKey); err != nil {
		return nil, err
	}
	root.AddChild(signature)
	wrapXMLSignatureBase64(signature)
	contents, err := document.WriteToBytes()
	if err != nil {
		return nil, err
	}
	return latin1(contents)
}

// SubmitConsumption autentica y envia el RCOF al upload del ambiente configurado.
func (c Client) SubmitConsumption(ctx context.Context, credentials Credentials, contents []byte) (Submission, error) {
	if c.HTTP == nil || c.Now == nil || len(contents) == 0 {
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
	return c.uploadNamed(ctx, credentials, token, contents, "ConsumoFolios")
}
