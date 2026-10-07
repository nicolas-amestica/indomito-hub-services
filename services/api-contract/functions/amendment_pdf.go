package functions

import (
	"fmt"
	"strings"

	"github.com/signintech/gopdf"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

const amendmentPDFGeneratorVersion = "contract-amendment-pdf-go-v1"

// ComposeAmendmentPDF genera un documento separado del contrato original. Las
// condiciones económicas no forman parte de este tipo de anexo.
func ComposeAmendmentPDF(content domain.Content, amendment domain.ContractAmendment) ([]byte, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeLetter})
	if err := p.AddTTFFontData("courier", courierTTF); err != nil {
		return nil, fmt.Errorf("cargar fuente regular: %w", err)
	}
	if err := p.AddTTFFontData("courier-bold", courierBoldTTF); err != nil {
		return nil, fmt.Errorf("cargar fuente bold: %w", err)
	}
	d := &contractPDF{pdf: p}
	d.addPage()
	d.center("ANEXO AL CONTRATO DE PRESTACIÓN DE SERVICIOS TURÍSTICOS", fontSizeTitle, true)
	d.y += 12
	d.center(strings.ToUpper(fallback(content.Institution.Name)), fontSizeClauseTitle, true)
	d.y += 20
	d.justifiedParagraph(fmt.Sprintf("Las partes individualizadas en el contrato %s, aprobado en su versión %d, acuerdan el presente anexo por el siguiente motivo: %s.", amendment.ContractID, amendment.BaseContractVersion, amendment.Reason), fontSizeBody)
	d.y += clauseSpacing
	d.heading("PRIMERO: FECHAS DEL VIAJE", fontSizeClauseTitle)
	d.justifiedParagraph(fmt.Sprintf("Las condiciones anteriores indicaban %s. Desde la aprobación de este anexo, las fechas vigentes serán %s, correspondientes a %d días y %d noches.", amendmentDates(amendment.Before), amendmentDates(amendment.After), amendment.After.Days, amendment.After.Nights), fontSizeBody)
	d.y += clauseSpacing
	d.heading("SEGUNDO: SERVICIOS INCLUIDOS", fontSizeClauseTitle)
	d.justifiedParagraph("Los servicios vigentes después de este anexo serán exclusivamente los siguientes:", fontSizeBody)
	for _, service := range amendment.After.Services {
		d.serviceItem("• " + service.Description)
	}
	d.y += clauseSpacing
	d.heading("TERCERO: CONDICIONES ECONÓMICAS", fontSizeClauseTitle)
	d.justifiedParagraph("Este anexo no modifica el precio total, el abono inicial, el número o valor de las cuotas, los pagos ya registrados, los descuentos, las devoluciones ni las obligaciones individuales de los pasajeros. Cualquier modificación económica requerirá un instrumento separado, expreso y aprobado por las partes.", fontSizeBody)
	d.y += clauseSpacing
	d.heading("CUARTO: VIGENCIA", fontSizeClauseTitle)
	d.justifiedParagraph("El contrato original permanece vigente en todo lo que no haya sido modificado expresamente por este anexo. Las condiciones anteriores se conservan como antecedente histórico y no son sobrescritas.", fontSizeBody)
	d.addPage()
	d.justifiedParagraph("En comprobante, previa lectura, firman y ratifican el presente anexo.", fontSizeBody)
	d.signatures(content)
	result, err := p.GetBytesPdfReturnErr()
	if err != nil {
		return nil, fmt.Errorf("generar PDF del anexo: %w", err)
	}
	return result, nil
}

func amendmentDates(snapshot domain.ContractTermsSnapshot) string {
	if snapshot.DepartureDate == "" && snapshot.ReturnDate == "" {
		return "que la fecha se encontraba por definir"
	}
	return fmt.Sprintf("salida el %s y retorno el %s", displayDateFull(snapshot.DepartureDate), displayDateFull(snapshot.ReturnDate))
}
