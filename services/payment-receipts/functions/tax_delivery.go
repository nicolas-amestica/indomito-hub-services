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
	"ind-hub-api-gox-sls-pri-gh/libs/pdfbrand"
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
	mail    mailer
	now     func() time.Time
}

type taxPublicItem struct {
	Description string `dynamodbav:"description"`
	Quantity    int64  `dynamodbav:"quantity"`
	UnitPrice   int64  `dynamodbav:"unitPrice"`
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
	ReceiptID string `dynamodbav:"receiptId"`
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
	_, err = deps.objects.PutObject(ctx, &s3.PutObjectInput{Bucket: &deps.bucket, Key: &key, Body: bytes.NewReader(document), ContentType: aws.String("application/pdf"), ContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="boleta-%d.pdf"`, job.Folio)), ServerSideEncryption: "AES256", IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": sha, "document-type": "39", "folio": strconv.FormatInt(job.Folio, 10)}})
	if err != nil {
		head, headErr := deps.objects.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &deps.bucket, Key: &key})
		if headErr != nil || head.Metadata["sha256"] != sha {
			return fmt.Errorf("store tax document: %w", err)
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
	if requestRow.TaxRequest.ReceiptID != "" {
		receiptItem, getErr := deps.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &deps.table, Key: taxKey("RECEIPT#"+requestRow.TaxRequest.ReceiptID, "META"), ConsistentRead: aws.Bool(true)})
		if getErr != nil {
			return getErr
		}
		var receipt receiptRow
		if attributevalue.UnmarshalMap(receiptItem.Item, &receipt) != nil {
			return errors.New("INVALID_RECEIPT")
		}
		address, addressErr := mail.ParseAddress(receipt.ReceiptEmail)
		if addressErr != nil || address.Address != receipt.ReceiptEmail {
			return errors.New("RECEIPT_EMAIL_MISSING")
		}
		if err = deps.mail.sendTax(ctx, receipt.ReceiptEmail, job.Folio, document); err != nil {
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
	pdfbrand.DrawHeader(pdf)
	pdfbrand.DrawFooter(pdf)
	writePDFText(pdf, "bold", 13, 255, 255, 255, 48, 88, model.IssuerName)
	writePDFText(pdf, "regular", 9, 255, 255, 255, 48, 108, "RUT "+model.IssuerRUT+" · "+model.IssuerActivity)
	writePDFText(pdf, "regular", 9, 255, 255, 255, 48, 124, model.IssuerAddress+", "+model.IssuerCommune)
	pdf.SetStrokeColor(180, 20, 20)
	pdf.SetLineWidth(1.5)
	pdf.RectFromUpperLeftWithStyle(350, 82, 197, 82, "D")
	writeCenteredPDFText(pdf, "bold", 11, 180, 20, 20, 448, 96, "RUT "+model.IssuerRUT)
	writeCenteredPDFText(pdf, "bold", 12, 180, 20, 20, 448, 116, "BOLETA ELECTRÓNICA")
	writeCenteredPDFText(pdf, "bold", 12, 180, 20, 20, 448, 136, "Nº "+strconv.FormatInt(model.Folio, 10))
	writeCenteredPDFText(pdf, "bold", 10, 180, 20, 20, 448, 151, "S.I.I.")
	writePDFText(pdf, "regular", 10, 28, 28, 28, 48, 188, "Fecha de emisión: "+model.IssueDate)
	y := 220.0
	for _, item := range model.Items {
		writePDFText(pdf, "regular", 9, 28, 28, 28, 48, y, item.Description)
		writePDFText(pdf, "regular", 9, 28, 28, 28, 340, y, strconv.FormatInt(item.Quantity, 10))
		writePDFText(pdf, "regular", 9, 28, 28, 28, 430, y, formatCLP(item.Quantity*item.UnitPrice))
		y += 20
	}
	writePDFText(pdf, "regular", 10, 28, 28, 28, 390, 430, "Neto: "+formatCLP(model.NetAmount))
	writePDFText(pdf, "regular", 10, 28, 28, 28, 390, 450, "IVA 19%: "+formatCLP(model.VATAmount))
	writePDFText(pdf, "bold", 12, 28, 28, 28, 390, 474, "Total: "+formatCLP(model.TotalAmount))
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
	if err = pdf.ImageByHolder(holder, 68, 520, &gopdf.Rect{W: 460, H: 130}); err != nil {
		return nil, err
	}
	writeCenteredPDFText(pdf, "regular", 9, 28, 28, 28, receiptPageWidth/2, 658, "Timbre electrónico SII")
	writeCenteredPDFText(pdf, "regular", 8, 28, 28, 28, receiptPageWidth/2, 676, fmt.Sprintf("Resolución SII Nº %d de %s", model.ResolutionNumber, model.ResolutionDate))
	writeCenteredPDFText(pdf, "regular", 8, 28, 28, 28, receiptPageWidth/2, 694, "Verifique en https://pagos.girasindomito.cl/consultar-boleta")
	return pdf.GetBytesPdfReturnErr()
}

func (m mailer) sendTax(ctx context.Context, to string, folio int64, document []byte) error {
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
	message.SetBodyString(gomail.TypeTextPlain, "Adjuntamos la representación PDF de tu boleta electrónica emitida y enviada al SII. Su validación se encuentra pendiente; puedes consultar posteriormente el folio en nuestro portal.")
	if err = message.AttachReader(fmt.Sprintf("boleta-%d.pdf", folio), bytes.NewReader(document), gomail.WithFileContentType(gomail.ContentType("application/pdf"))); err != nil {
		return err
	}
	return m.sendMessage(ctx, message)
}
