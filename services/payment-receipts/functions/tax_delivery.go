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
	"unicode"

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
	drawTaxPageHeader(pdf, model, false)
	y := 214.0
	for _, item := range model.Items {
		description := item.Description
		if item.Exempt {
			description += " (Exento)"
		}
		lines := wrapPDFText(pdf, "regular", 9, description, 270)
		rowHeight := max(22, float64(len(lines))*12+8)
		if y+rowHeight > 690 {
			drawTaxPageFooter(pdf)
			pdf.AddPage()
			drawTaxPageHeader(pdf, model, true)
			y = 214
		}
		quantity := strconv.FormatInt(item.Quantity, 10)
		if strings.TrimSpace(item.Unit) != "" {
			quantity += " " + strings.TrimSpace(item.Unit)
		}
		for index, line := range lines {
			writePDFText(pdf, "regular", 9, 28, 28, 28, 48, y+float64(index)*12, line)
		}
		writeRightAlignedPDFText(pdf, "regular", 9, 28, 28, 28, 382, y, quantity)
		writeRightAlignedPDFText(pdf, "regular", 9, 28, 28, 28, 466, y, formatCLP(item.UnitPrice))
		writeRightAlignedPDFText(pdf, "regular", 9, 28, 28, 28, 547, y, formatCLP(item.Quantity*item.UnitPrice))
		pdf.SetStrokeColor(214, 218, 223)
		pdf.Line(48, y+rowHeight-4, 547, y+rowHeight-4)
		y += rowHeight
	}
	totalsHeight := 52.0
	if model.NetAmount > 0 {
		totalsHeight += 18
	}
	if model.VATAmount > 0 {
		totalsHeight += 18
	}
	if y+totalsHeight+170 > 790 {
		drawTaxPageFooter(pdf)
		pdf.AddPage()
		drawTaxPageHeader(pdf, model, true)
		y = 214
	}
	totalsY := y + 16
	if model.NetAmount > 0 {
		writePDFText(pdf, "regular", 9, 75, 85, 99, 405, totalsY, "Neto")
		writeRightAlignedPDFText(pdf, "regular", 9, 28, 28, 28, 547, totalsY, formatCLP(model.NetAmount))
		totalsY += 18
	}
	if model.VATAmount > 0 {
		writePDFText(pdf, "regular", 9, 75, 85, 99, 405, totalsY, "IVA 19%")
		writeRightAlignedPDFText(pdf, "regular", 9, 28, 28, 28, 547, totalsY, formatCLP(model.VATAmount))
		totalsY += 18
	}
	pdf.SetStrokeColor(28, 28, 28)
	pdf.Line(405, totalsY-6, 547, totalsY-6)
	writePDFText(pdf, "bold", 12, 28, 28, 28, 405, totalsY+4, "Total")
	writeRightAlignedPDFText(pdf, "bold", 14, 28, 28, 28, 547, totalsY+2, formatCLP(model.TotalAmount))
	stampY := totalsY + 34
	code, err := pdf417.Encode(model.TED, 5)
	if err != nil {
		return nil, err
	}
	// Rasterizar con módulos enteros; el tamaño en puntos se ajusta después.
	// Un TED real puede superar los 360 píxeles de ancho.
	scaled, err := barcode.Scale(code, code.Bounds().Dx()*2, code.Bounds().Dy()*2)
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
	stampWidth, stampHeight := taxStampSize(scaled.Bounds().Dx(), scaled.Bounds().Dy())
	if err = pdf.ImageByHolder(holder, (receiptPageWidth-stampWidth)/2, stampY, &gopdf.Rect{W: stampWidth, H: stampHeight}); err != nil {
		return nil, err
	}
	stampBottom := stampY + stampHeight
	writeCenteredPDFText(pdf, "bold", 9, 28, 28, 28, receiptPageWidth/2, stampBottom+8, "Timbre electrónico SII")
	writeCenteredPDFText(pdf, "regular", 8, 75, 85, 99, receiptPageWidth/2, stampBottom+22, fmt.Sprintf("Resolución SII Nº %d de %s", model.ResolutionNumber, formatTaxDate(model.ResolutionDate)))
	writeCenteredPDFText(pdf, "regular", 8, 75, 85, 99, receiptPageWidth/2, stampBottom+36, "Verifique este documento en www.sii.cl")
	writeCenteredPDFText(pdf, "regular", 8, 75, 85, 99, receiptPageWidth/2, stampBottom+50, "Consulta adicional: https://pagos.girasindomito.cl/consultar-boleta")
	drawTaxPageFooter(pdf)
	return pdf.GetBytesPdfReturnErr()
}

func taxStampSize(width, height int) (float64, float64) {
	factor := min(460/float64(width), 102/float64(height))
	return float64(width) * factor, float64(height) * factor
}

func drawTaxPageHeader(pdf *gopdf.GoPdf, model taxPublicDocument, continuation bool) {
	writePDFText(pdf, "bold", 15, 28, 28, 28, 48, 44, model.IssuerName)
	writePDFText(pdf, "regular", 9, 75, 85, 99, 48, 68, model.IssuerActivity)
	address := strings.Trim(strings.Join(strings.Fields(strings.TrimSpace(model.IssuerAddress)), " "), " ,")
	commune := strings.Trim(strings.Join(strings.Fields(strings.TrimSpace(model.IssuerCommune)), " "), " ,")
	if commune != "" && !strings.EqualFold(address, commune) {
		address = strings.TrimSpace(address + ", " + commune)
	}
	writeWrapped(pdf, "regular", 9, 75, 85, 99, 48, 84, 270, address)
	pdf.SetStrokeColor(180, 20, 20)
	pdf.SetLineWidth(1.5)
	pdf.RectFromUpperLeftWithStyle(350, 38, 197, 100, "D")
	writeCenteredPDFText(pdf, "bold", 11, 180, 20, 20, 448, 52, "RUT "+model.IssuerRUT)
	writeCenteredPDFText(pdf, "bold", 12, 180, 20, 20, 448, 75, "BOLETA ELECTRÓNICA")
	writeCenteredPDFText(pdf, "bold", 12, 180, 20, 20, 448, 98, "Nº "+strconv.FormatInt(model.Folio, 10))
	writeCenteredPDFText(pdf, "bold", 10, 180, 20, 20, 448, 120, "S.I.I.")
	pdf.SetStrokeColor(214, 218, 223)
	pdf.SetLineWidth(0.5)
	pdf.Line(48, 154, 547, 154)
	dateLabel := "Fecha de emisión: " + formatTaxDate(model.IssueDate)
	if continuation {
		dateLabel += " · Continuación"
	}
	writeRightAlignedPDFText(pdf, "regular", 9, 75, 85, 99, 547, 169, dateLabel)
	pdf.SetFillColor(243, 244, 246)
	pdf.RectFromUpperLeftWithStyle(48, 190, 499, 24, "F")
	writePDFText(pdf, "bold", 9, 28, 28, 28, 54, 197, "Descripción")
	writeRightAlignedPDFText(pdf, "bold", 9, 28, 28, 28, 382, 197, "Cantidad")
	writeRightAlignedPDFText(pdf, "bold", 9, 28, 28, 28, 466, 197, "Precio")
	writeRightAlignedPDFText(pdf, "bold", 9, 28, 28, 28, 541, 197, "Total")
}

func drawTaxPageFooter(pdf *gopdf.GoPdf) {
	pdf.SetStrokeColor(214, 218, 223)
	pdf.Line(48, 812, 547, 812)
	writeCenteredPDFText(pdf, "regular", 7, 107, 114, 128, receiptPageWidth/2, 818, "Representación impresa de boleta electrónica")
}

func formatTaxDate(value string) string {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return parsed.Format("02-01-2006")
}

func taxItemDescription(value string) string {
	return value
}

func wrapPDFText(pdf *gopdf.GoPdf, font string, size int, value string, width float64) []string {
	_ = pdf.SetFont(font, "", size)
	words := strings.FieldsFunc(value, unicode.IsSpace)
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		candidate := lines[len(lines)-1] + " " + word
		measured, err := pdf.MeasureTextWidth(candidate)
		if err == nil && measured <= width {
			lines[len(lines)-1] = candidate
			continue
		}
		lines = append(lines, word)
	}
	return lines
}

func writeRightAlignedPDFText(pdf *gopdf.GoPdf, font string, size int, red, green, blue uint8, rightX, y float64, value string) {
	_ = pdf.SetFont(font, "", size)
	width, err := pdf.MeasureTextWidth(value)
	if err != nil {
		width = 0
	}
	writePDFText(pdf, font, size, red, green, blue, rightX-width, y, value)
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
