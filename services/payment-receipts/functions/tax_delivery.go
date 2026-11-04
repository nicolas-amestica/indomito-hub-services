package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/signintech/gopdf"
	barcode "github.com/speedata/barcode"
	"github.com/speedata/barcode/pdf417"
	gomail "github.com/wneessen/go-mail"
)

type taxDeliveryJob struct {
	PK, SK, RequestID string
	Folio             int64
}

type taxDeliveryDependencies struct {
	db      dynamoAPI
	table   string
	objects documentAPI
	bucket  string
	mail    taxMailer
	now     func() time.Time
}

type taxMailer interface {
	sendTax(context.Context, string, int64, string, []byte) error
}

type taxPublicItem struct {
	Description string `dynamodbav:"description"`
	Quantity    int64  `dynamodbav:"quantity"`
	UnitPrice   int64  `dynamodbav:"unitPrice"`
	Exempt      bool   `dynamodbav:"exempt"`
	Unit        string `dynamodbav:"unit"`
}

type taxPublicDocument struct {
	Folio            int64           `dynamodbav:"folio"`
	IssueDate        string          `dynamodbav:"issueDate"`
	IssuerRUT        string          `dynamodbav:"issuerRut"`
	IssuerName       string          `dynamodbav:"issuerName"`
	IssuerActivity   string          `dynamodbav:"issuerActivity"`
	IssuerAddress    string          `dynamodbav:"issuerAddress"`
	IssuerCommune    string          `dynamodbav:"issuerCommune"`
	ResolutionDate   string          `dynamodbav:"resolutionDate"`
	ResolutionNumber int64           `dynamodbav:"resolutionNumber"`
	NetAmount        int64           `dynamodbav:"netAmount"`
	VATAmount        int64           `dynamodbav:"vatAmount"`
	TotalAmount      int64           `dynamodbav:"totalAmount"`
	TED              string          `dynamodbav:"ted"`
	Items            []taxPublicItem `dynamodbav:"items"`
}

type taxRequestProjection struct {
	ReceiptID     string `dynamodbav:"receiptId"`
	DeliveryEmail string `dynamodbav:"deliveryEmail"`
}
type taxRequestRow struct {
	TaxRequest taxRequestProjection `dynamodbav:"taxRequest"`
}
type taxPublicRow struct {
	PublicTaxDocument taxPublicDocument `dynamodbav:"publicTaxDocument"`
}

func processTaxDelivery(ctx context.Context, job taxDeliveryJob, deps taxDeliveryDependencies) error {
	if deps.db == nil || deps.objects == nil || deps.now == nil || job.RequestID == "" || job.Folio < 1 || job.SK != "PENDING#"+job.RequestID || !strings.HasPrefix(job.PK, "TAX_JOB#") {
		return errors.New("INVALID_TAX_DELIVERY_JOB")
	}
	publicItem, err := deps.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &deps.table, Key: taxKey("TAX_PUBLIC#39#"+strconv.FormatInt(job.Folio, 10), "META"), ConsistentRead: aws.Bool(true)})
	if err != nil || len(publicItem.Item) == 0 {
		return errors.New("TAX_DOCUMENT_NOT_FOUND")
	}
	var publicRow taxPublicRow
	if attributevalue.UnmarshalMap(publicItem.Item, &publicRow) != nil || publicRow.PublicTaxDocument.Folio != job.Folio {
		return errors.New("INVALID_TAX_DOCUMENT")
	}
	document, err := renderTaxDocument(publicRow.PublicTaxDocument)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("tax-documents/39/%d.pdf", job.Folio)
	digest := sha256.Sum256(document)
	sha := hex.EncodeToString(digest[:])
	object := &s3.PutObjectInput{Bucket: &deps.bucket, Key: &key, Body: bytes.NewReader(document), ContentType: aws.String("application/pdf"), ContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="boleta-%d-sii.pdf"`, job.Folio)), ServerSideEncryption: "AES256", IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": sha, "document-type": "39", "folio": strconv.FormatInt(job.Folio, 10), "render-version": "2"}}
	_, err = deps.objects.PutObject(ctx, object)
	if err != nil {
		head, headErr := deps.objects.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &deps.bucket, Key: &key})
		if headErr != nil {
			return fmt.Errorf("store tax document: %w", err)
		}
		if head.Metadata["sha256"] != sha {
			if head.Metadata["document-type"] != "39" || head.Metadata["folio"] != strconv.FormatInt(job.Folio, 10) || head.ETag == nil {
				return fmt.Errorf("store tax document: %w", err)
			}
			object.Body = bytes.NewReader(document)
			object.IfNoneMatch = nil
			object.IfMatch = head.ETag
			if _, replaceErr := deps.objects.PutObject(ctx, object); replaceErr != nil {
				return fmt.Errorf("replace legacy tax document: %w", replaceErr)
			}
		}
	}
	requestItem, err := deps.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &deps.table, Key: taxKey("TAX_REQUEST#"+job.RequestID, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return err
	}
	var requestRow taxRequestRow
	if attributevalue.UnmarshalMap(requestItem.Item, &requestRow) != nil {
		return errors.New("INVALID_TAX_REQUEST")
	}
	deliveryEmail := requestRow.TaxRequest.DeliveryEmail
	if deliveryEmail == "" && requestRow.TaxRequest.ReceiptID != "" {
		receiptItem, getErr := deps.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &deps.table, Key: taxKey("RECEIPT#"+requestRow.TaxRequest.ReceiptID, "META"), ConsistentRead: aws.Bool(true)})
		if getErr != nil {
			return getErr
		}
		var receipt receiptRow
		if attributevalue.UnmarshalMap(receiptItem.Item, &receipt) != nil {
			return errors.New("INVALID_RECEIPT")
		}
		deliveryEmail = receipt.ReceiptEmail
	}
	if deliveryEmail != "" {
		address, addressErr := mail.ParseAddress(deliveryEmail)
		if addressErr != nil || address.Address != deliveryEmail || len(deliveryEmail) > 254 {
			return errors.New("RECEIPT_EMAIL_MISSING")
		}
		concept := "Boleta electrónica"
		if len(publicRow.PublicTaxDocument.Items) > 0 && strings.TrimSpace(publicRow.PublicTaxDocument.Items[0].Description) != "" {
			concept = strings.TrimSpace(publicRow.PublicTaxDocument.Items[0].Description)
		}
		if err = deps.mail.sendTax(ctx, deliveryEmail, job.Folio, concept, document); err != nil {
			return err
		}
	}
	_, err = deps.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: &deps.table, Key: taxKey(job.PK, job.SK), ConditionExpression: aws.String("#status = :pending"), UpdateExpression: aws.String("SET #status = :sent, documentKey = :key, documentSha256 = :sha, sentAt = :at"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": &types.AttributeValueMemberS{Value: "PENDING"}, ":sent": &types.AttributeValueMemberS{Value: "SENT"}, ":key": &types.AttributeValueMemberS{Value: key}, ":sha": &types.AttributeValueMemberS{Value: sha}, ":at": &types.AttributeValueMemberS{Value: deps.now().UTC().Format(time.RFC3339Nano)}}})
	return err
}

func taxKey(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}

func renderTaxDocument(model taxPublicDocument) ([]byte, error) {
	if model.Folio < 1 || model.TotalAmount < 1 || model.TED == "" {
		return nil, errors.New("INVALID_TAX_DOCUMENT")
	}
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("regular", receiptFont); err != nil {
		return nil, err
	}
	if err := pdf.AddTTFFontData("bold", receiptBoldFont); err != nil {
		return nil, err
	}
	pdf.AddPage()
	writePDFText(pdf, "bold", 14, 28, 28, 28, 48, 48, model.IssuerName)
	writePDFText(pdf, "regular", 9, 28, 28, 28, 48, 70, "RUT "+model.IssuerRUT)
	writePDFText(pdf, "regular", 9, 28, 28, 28, 48, 86, model.IssuerActivity)
	writePDFText(pdf, "regular", 9, 28, 28, 28, 48, 102, model.IssuerAddress+", "+model.IssuerCommune)
	pdf.SetStrokeColor(180, 20, 20)
	pdf.SetLineWidth(1.5)
	pdf.RectFromUpperLeftWithStyle(350, 42, 197, 105, "D")
	writeCenteredPDFText(pdf, "bold", 11, 180, 20, 20, 448, 58, "RUT "+model.IssuerRUT)
	writeCenteredPDFText(pdf, "bold", 12, 180, 20, 20, 448, 82, "BOLETA ELECTRÓNICA")
	writeCenteredPDFText(pdf, "bold", 12, 180, 20, 20, 448, 106, "Nº "+strconv.FormatInt(model.Folio, 10))
	writeCenteredPDFText(pdf, "bold", 10, 180, 20, 20, 448, 130, "S.I.I.")
	pdf.SetStrokeColor(28, 28, 28)
	pdf.SetLineWidth(0.5)
	pdf.Line(48, 166, 547, 166)
	writePDFText(pdf, "regular", 10, 28, 28, 28, 390, 188, "Fecha de emisión: "+model.IssueDate)
	writePDFText(pdf, "bold", 9, 28, 28, 28, 48, 222, "Descripción")
	writePDFText(pdf, "bold", 9, 28, 28, 28, 330, 222, "Cantidad")
	writePDFText(pdf, "bold", 9, 28, 28, 28, 405, 222, "Precio")
	writePDFText(pdf, "bold", 9, 28, 28, 28, 492, 222, "Total")
	pdf.Line(48, 232, 547, 232)
	y := 250.0
	for _, item := range model.Items {
		description := item.Description
		if item.Exempt {
			description += " (Exento)"
		}
		quantity := strconv.FormatInt(item.Quantity, 10)
		if strings.TrimSpace(item.Unit) != "" {
			quantity += " " + strings.TrimSpace(item.Unit)
		}
		writePDFText(pdf, "regular", 9, 28, 28, 28, 48, y, description)
		writePDFText(pdf, "regular", 9, 28, 28, 28, 340, y, quantity)
		writePDFText(pdf, "regular", 9, 28, 28, 28, 405, y, formatCLP(item.UnitPrice))
		writePDFText(pdf, "regular", 9, 28, 28, 28, 492, y, formatCLP(item.Quantity*item.UnitPrice))
		pdf.Line(48, y+10, 547, y+10)
		y += 22
	}
	totalsY := max(y+18, 360)
	if model.NetAmount > 0 {
		writePDFText(pdf, "regular", 10, 28, 28, 28, 390, totalsY, "Neto: "+formatCLP(model.NetAmount))
		totalsY += 20
	}
	if model.VATAmount > 0 {
		writePDFText(pdf, "regular", 10, 28, 28, 28, 390, totalsY, "IVA 19%: "+formatCLP(model.VATAmount))
		totalsY += 20
	}
	writePDFText(pdf, "bold", 12, 28, 28, 28, 390, totalsY, "Total: "+formatCLP(model.TotalAmount))
	code, err := pdf417.Encode(model.TED, 5)
	if err != nil {
		return nil, err
	}
	scaled, err := barcode.Scale(code, 460, 130)
	if err != nil {
		return nil, err
	}
	rgba := image.NewRGBA(scaled.Bounds())
	draw.Draw(rgba, rgba.Bounds(), scaled, scaled.Bounds().Min, draw.Src)
	var imageBytes bytes.Buffer
	if err = png.Encode(&imageBytes, rgba); err != nil {
		return nil, err
	}
	holder, err := gopdf.ImageHolderByBytes(imageBytes.Bytes())
	if err != nil {
		return nil, err
	}
	if err = pdf.ImageByHolder(holder, 68, 500, &gopdf.Rect{W: 460, H: 130}); err != nil {
		return nil, err
	}
	writeCenteredPDFText(pdf, "regular", 9, 28, 28, 28, receiptPageWidth/2, 638, "Timbre electrónico SII")
	writeCenteredPDFText(pdf, "regular", 8, 28, 28, 28, receiptPageWidth/2, 656, fmt.Sprintf("Resolución SII Nº %d de %s", model.ResolutionNumber, model.ResolutionDate))
	writeCenteredPDFText(pdf, "regular", 8, 28, 28, 28, receiptPageWidth/2, 674, "Verifique en www.sii.cl")
	writeCenteredPDFText(pdf, "regular", 8, 28, 28, 28, receiptPageWidth/2, 692, "Consulta adicional: https://pagos.girasindomito.cl/consultar-boleta")
	return pdf.GetBytesPdfReturnErr()
}

func (m mailer) sendTax(ctx context.Context, to string, folio int64, concept string, document []byte) error {
	toAddress, err := mail.ParseAddress(to)
	if err != nil || toAddress.Address != to {
		return errors.New("RECEIPT_EMAIL_MISSING")
	}
	fromAddress, err := mail.ParseAddress(m.config.From)
	if err != nil {
		return err
	}
	message := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	message.FromMailAddress(fromAddress)
	message.ToMailAddress(toAddress)
	message.Subject("Giras Indomito: boleta electrónica Nº " + strconv.FormatInt(folio, 10))
	message.SetMessageIDWithValue(fmt.Sprintf("tax-document-39-%d@girasindomito.cl", folio))
	lead := "Tu boleta electrónica fue emitida y enviada al SII. Adjuntamos su representación en PDF para que la conserves."
	notice := "El envío al SII no equivale a aceptación definitiva. El estado tributario puede procesarse posteriormente y el folio permite identificar el documento."
	plain := fmt.Sprintf("%s\n\nFolio: %d\nConcepto: %s\n\n%s", lead, folio, concept, notice)
	stage := ""
	if os.Getenv("APP_STAGE") == "dev" {
		stage = "Ambiente de desarrollo: este mensaje no acredita una operación productiva."
	}
	html, err := renderBrandedEmail(brandedEmail{Preheader: lead, Eyebrow: "Documento tributario", Title: "Tu boleta electrónica está lista", Lead: lead, Body: "Guarda este correo y el archivo adjunto como respaldo de la operación.", Notice: notice, Details: []emailDetail{{Label: "Documento", Value: "Boleta electrónica SII"}, {Label: "Folio", Value: strconv.FormatInt(folio, 10)}, {Label: "Concepto", Value: concept}}, Stage: stage})
	if err != nil {
		return fmt.Errorf("generar correo: %w", err)
	}
	message.SetBodyString(gomail.TypeTextPlain, plain)
	message.AddAlternativeString(gomail.TypeTextHTML, html)
	if err = message.AttachReader(fmt.Sprintf("boleta-%d-sii.pdf", folio), bytes.NewReader(document), gomail.WithFileContentType(gomail.ContentType("application/pdf"))); err != nil {
		return err
	}
	return m.sendMessage(ctx, message)
}
