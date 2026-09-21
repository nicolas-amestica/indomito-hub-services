package generarpresupuestov1

import (
	"fmt"
	"time"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1/templates"
)

const (
	regularFontName = "GoRegular"
	boldFontName    = "GoBold"
)

// ComposePDF compone un presupuesto A4 en memoria a partir de la secuencia de
// bloques de la plantilla. No escribe archivos ni consulta servicios externos.
func ComposePDF(
	request domain.BudgetRequest,
	template templates.Template,
	generatedAt time.Time,
) ([]byte, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	pdf.SetMargins(0, 0, 0, 0)

	if err := pdf.AddTTFFontData(regularFontName, goregular.TTF); err != nil {
		return nil, fmt.Errorf("cargar fuente regular: %w", err)
	}
	if err := pdf.AddTTFFontData(boldFontName, gobold.TTF); err != nil {
		return nil, fmt.Errorf("cargar fuente negrita: %w", err)
	}

	canvas := templates.NewCanvas(pdf, regularFontName, boldFontName)
	canvas.AddPage()
	for _, block := range template.Blocks(request, generatedAt) {
		height := block.Height(canvas)
		if height <= canvas.ContentHeight() {
			canvas.EnsureSpace(height)
		}
		if err := block.Render(canvas); err != nil {
			return nil, fmt.Errorf("renderizar bloque %T: %w", block, err)
		}
	}

	contents, err := pdf.GetBytesPdfReturnErr()
	if err != nil {
		return nil, fmt.Errorf("serializar PDF: %w", err)
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("serializar PDF: documento vacío")
	}
	return contents, nil
}
