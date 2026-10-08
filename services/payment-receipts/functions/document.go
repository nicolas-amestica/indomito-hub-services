package functions

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/signintech/gopdf"
)

//go:embed assets/CourierNew.ttf
var receiptFont []byte

//go:embed assets/CourierNew-Bold.ttf
var receiptBoldFont []byte

var receiptIDPattern = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func buildReceiptModel(row receiptRow) (receiptModel, error) {
	if row.Event == nil || !receiptIDPattern.MatchString(row.ReceiptID) {
		return receiptModel{}, errors.New("INVALID_RECEIPT")
	}
	event := row.Event
	group := event.Type == "GROUP_DEPOSIT_RECEIVED"
	allowed := event.Type == "PAYMENT_RECEIVED" || event.Type == "PAYMENT_REQUIRES_REVIEW" || event.Type == "DEPOSIT_RECEIVED" || group
	version := row.DocumentVersion
	if version == 0 {
		version = 1
	}
	_, dateErr := time.Parse(time.DateOnly, event.EffectiveDate)
	if !allowed || !receiptIDPattern.MatchString(event.TripID) || (!group && !receiptIDPattern.MatchString(event.AccountID)) || event.Amount <= 0 || event.Amount > 1_000_000_000_000 || dateErr != nil || event.RecordedAt.IsZero() || (version != 1 && version != 2 && version != 3) {
		return receiptModel{}, errors.New("INVALID_RECEIPT")
	}
	name, document := strings.TrimSpace(row.PassengerName), strings.TrimSpace(row.PassengerDocument)
	if version >= 2 && !group && (name == "" || document == "") {
		return receiptModel{}, errors.New("INVALID_RECEIPT")
	}
	verificationURL := ""
	if version == 3 {
		verificationURL = receiptVerificationURL()
		if verificationURL == "" {
			return receiptModel{}, errors.New("INVALID_RECEIPT_VERIFICATION_URL")
		}
	}
	return receiptModel{ID: row.ReceiptID, AccountID: event.AccountID, TripID: event.TripID, Amount: event.Amount, EffectiveDate: event.EffectiveDate, RecordedAt: event.RecordedAt.UTC(), Review: event.Type == "PAYMENT_REQUIRES_REVIEW", Deposit: event.Type == "DEPOSIT_RECEIVED", GroupDeposit: group, Version: version, PassengerName: name, PassengerDocument: document, VerificationURL: verificationURL}, nil
}

func renderReceipt(model receiptModel) (renderedDocument, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("regular", receiptFont); err != nil {
		return renderedDocument{}, fmt.Errorf("cargar fuente: %w", err)
	}
	if err := pdf.AddTTFFontData("bold", receiptBoldFont); err != nil {
		return renderedDocument{}, fmt.Errorf("cargar fuente bold: %w", err)
	}
	pdf.AddPage()
	pdf.SetFillColor(28, 28, 28)
	pdf.RectFromUpperLeftWithStyle(0, 0, 595, 112, "F")
	writePDFText(pdf, "bold", 21, 235, 255, 0, 48, 36, "GIRAS INDÓMITO")
	writePDFText(pdf, "regular", 11, 255, 255, 255, 48, 72, "Comprobante de registro de pago")
	writePDFText(pdf, "bold", 11, 28, 28, 28, 48, 140, "AMBIENTE DE DESARROLLO")
	writePDFText(pdf, "regular", 10, 28, 28, 28, 48, 161, "Este documento no es una boleta ni una factura emitida por el SII.")

	identity := model.AccountID
	label := "Cuenta del pasajero"
	if model.GroupDeposit {
		label, identity = "Alcance", "Grupo de pasajeros pagantes"
	} else if model.Version >= 2 {
		label, identity = "Pasajero", model.PassengerName+" · RUT "+model.PassengerDocument
	}
	concept := "Pago de cuota recibido"
	if model.GroupDeposit {
		concept = "Abono grupal recibido y asignado"
	} else if model.Deposit {
		concept = "Abono recibido"
	} else if model.Review {
		concept = "Dinero recibido pendiente de revisión"
	}
	rows := [][2]string{{"Comprobante", model.ID}, {"Viaje / gira", model.TripID}, {label, identity}, {"Fecha efectiva", model.EffectiveDate}, {"Concepto", concept}}
	for index, row := range rows {
		y := 225 + float64(index*41)
		writePDFText(pdf, "regular", 9, 97, 97, 97, 48, y, row[0])
		writePDFText(pdf, "bold", 11, 28, 28, 28, 48, y+14, row[1])
	}
	pdf.SetFillColor(243, 244, 246)
	pdf.RectFromUpperLeftWithStyle(48, 448, 499, 68, "F")
	writePDFText(pdf, "regular", 10, 28, 28, 28, 64, 461, "Monto recibido")
	writePDFText(pdf, "bold", 24, 28, 28, 28, 64, 479, formatCLP(model.Amount))
	noteLine1 := "Este comprobante acredita el registro del ingreso indicado."
	noteLine2 := "No certifica que todas las obligaciones del viaje estén pagadas."
	if model.Review {
		noteLine1 = "El dinero está registrado, pero todavía no se aplicó a una cuota."
		noteLine2 = "No realices otro pago sin verificar el estado con Giras Indómito."
	}
	writePDFText(pdf, "regular", 9, 28, 28, 28, 48, 542, noteLine1)
	writePDFText(pdf, "regular", 9, 28, 28, 28, 48, 558, noteLine2)
	if model.Version == 3 {
		writePDFText(pdf, "regular", 8, 97, 97, 97, 48, 596, "Las devoluciones se registran por separado y no eliminan el ingreso original.")
		if err := drawReceiptVerification(pdf, model.VerificationURL, model.ID); err != nil {
			return renderedDocument{}, err
		}
	} else {
		writeWrapped(pdf, "regular", 9, 97, 97, 97, 48, 658, 499, "Conserva el número de comprobante para consultas. Las devoluciones se registran por separado y no eliminan el ingreso original.")
	}
	writePDFText(pdf, "regular", 8, 97, 97, 97, 48, 785, fmt.Sprintf("Giras Indómito · Registro financiero · DEV · Documento v%d", model.Version))
	bytes, err := pdf.GetBytesPdfReturnErr()
	if err != nil {
		return renderedDocument{}, fmt.Errorf("generar comprobante: %w", err)
	}
	digest := sha256.Sum256(bytes)
	owner := model.AccountID
	if model.GroupDeposit {
		owner = "groups/" + model.TripID
	}
	return renderedDocument{Bytes: bytes, SHA256: hex.EncodeToString(digest[:]), Key: fmt.Sprintf("receipts/%s/%s/v%d.pdf", owner, model.ID, model.Version)}, nil
}

func writePDFText(pdf *gopdf.GoPdf, font string, size int, red, green, blue uint8, x, y float64, value string) {
	_ = pdf.SetFont(font, "", size)
	pdf.SetTextColor(red, green, blue)
	pdf.SetX(x)
	pdf.SetY(y)
	_ = pdf.Cell(nil, value)
}

func writeWrapped(pdf *gopdf.GoPdf, font string, size int, red, green, blue uint8, x, y, width float64, value string) {
	_ = pdf.SetFont(font, "", size)
	pdf.SetTextColor(red, green, blue)
	pdf.SetX(x)
	pdf.SetY(y)
	_ = pdf.MultiCell(&gopdf.Rect{W: width, H: 14}, value)
}

func formatCLP(amount int64) string {
	digits := fmt.Sprintf("%d", amount)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "." + digits[index:]
	}
	return "$" + digits
}
