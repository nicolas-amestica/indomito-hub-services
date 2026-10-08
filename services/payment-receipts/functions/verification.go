package functions

import (
	"fmt"
	"os"
	"strings"

	"github.com/signintech/gopdf"
	qrcode "github.com/skip2/go-qrcode"
)

// receiptVerificationURL devuelve la única base permitida para cualquier
// comprobante verificable, sin distinguir su origen financiero.
func receiptVerificationURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("RECEIPT_VERIFICATION_URL")), "/")
}

// drawReceiptVerification compone el bloque común QR+código de los
// comprobantes v3. Cuotas, abonos individuales y grupales reutilizan esta
// función y solo cambian concepto e identidad en el modelo principal.
func drawReceiptVerification(pdf *gopdf.GoPdf, baseURL, receiptID string) error {
	writePDFText(pdf, "regular", 8, 97, 97, 97, 48, 610, "Verifica la autenticidad y el estado actual mediante el código QR.")
	qr, err := qrcode.Encode(baseURL+"#"+receiptID, qrcode.Medium, 256)
	if err != nil {
		return fmt.Errorf("generar QR: %w", err)
	}
	holder, err := gopdf.ImageHolderByBytes(qr)
	if err != nil {
		return fmt.Errorf("cargar QR: %w", err)
	}
	if err = pdf.ImageByHolder(holder, 256, 632, &gopdf.Rect{W: 82, H: 82}); err != nil {
		return fmt.Errorf("dibujar QR: %w", err)
	}
	writePDFText(pdf, "regular", 7, 97, 97, 97, 226, 720, "Código de verificación")
	writePDFText(pdf, "bold", 8, 28, 28, 28, 224, 733, receiptID)
	return nil
}
