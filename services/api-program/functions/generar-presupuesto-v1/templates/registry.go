// Package templates define el registro y los bloques declarativos de las
// plantillas de presupuesto. El registro es la única autoridad sobre los
// budgetTemplateId que el endpoint acepta.
package templates

import (
	"time"

	"github.com/signintech/gopdf"

	"ind-hub-api-gox-sls-pri-gh/services/api-program/domain"
)

const (
	// BrochureDefaultID es la plantilla inicial compartida por todos los
	// destinos sembrados por api-catalog.
	BrochureDefaultID = "brochure-default"

	pageMargin = 48.0
	pageBottom = 793.0
)

// Block es una sección declarativa del folleto. Height permite al compositor
// decidir si inicia otra página antes de renderizarla.
type Block interface {
	Height(canvas *Canvas) float64
	Render(canvas *Canvas) error
}

// Template declara la identidad y la secuencia de bloques de un presupuesto.
type Template struct {
	id    string
	build func(domain.BudgetRequest, time.Time) []Block
}

// ID devuelve el identificador que llega desde el catálogo de destinos.
func (t Template) ID() string {
	return t.id
}

// Blocks construye una secuencia nueva para una solicitud.
func (t Template) Blocks(request domain.BudgetRequest, generatedAt time.Time) []Block {
	return t.build(request, generatedAt)
}

// Registry resuelve una plantilla por budgetTemplateId. Después de construido
// es inmutable y puede reutilizarse entre invocaciones de una Lambda.
type Registry struct {
	byID map[string]Template
}

// NewRegistry construye un registro con las plantillas indicadas.
func NewRegistry(registered ...Template) Registry {
	byID := make(map[string]Template, len(registered))
	for _, template := range registered {
		byID[template.ID()] = template
	}
	return Registry{byID: byID}
}

// DefaultRegistry devuelve el registro productivo inicial.
func DefaultRegistry() Registry {
	return NewRegistry(BrochureDefault())
}

// Lookup busca una plantilla sin aplicar respaldo: un identificador desconocido
// es un error de validación y nunca debe seleccionar otra plantilla en silencio.
func (r Registry) Lookup(id string) (Template, bool) {
	template, ok := r.byID[id]
	return template, ok
}

// Canvas mantiene el documento y el cursor del compositor. Los bloques conocen
// el flujo vertical, pero no administran páginas ni márgenes por su cuenta.
type Canvas struct {
	PDF         *gopdf.GoPdf
	X           float64
	Y           float64
	Width       float64
	regularFont string
	boldFont    string
}

// NewCanvas arma el lienzo A4 con las familias ya cargadas en gopdf.
func NewCanvas(pdf *gopdf.GoPdf, regularFont, boldFont string) *Canvas {
	return &Canvas{
		PDF:         pdf,
		X:           pageMargin,
		Y:           pageMargin,
		Width:       gopdf.PageSizeA4.W - 2*pageMargin,
		regularFont: regularFont,
		boldFont:    boldFont,
	}
}

// AddPage agrega una página y reinicia el cursor en el margen superior.
func (c *Canvas) AddPage() {
	c.PDF.AddPage()
	c.X = pageMargin
	c.Y = pageMargin
}

// EnsureSpace inicia otra página cuando el bloque indicado no cabe completo.
func (c *Canvas) EnsureSpace(height float64) {
	if c.Y+height > pageBottom {
		c.AddPage()
	}
}

// Advance desplaza el cursor vertical después de un bloque o una fila.
func (c *Canvas) Advance(height float64) {
	c.Y += height
}

// ContentHeight devuelve el alto utilizable de una página.
func (c *Canvas) ContentHeight() float64 {
	return pageBottom - pageMargin
}

func (c *Canvas) setFont(bold bool, size float64) error {
	family := c.regularFont
	if bold {
		family = c.boldFont
	}
	return c.PDF.SetFont(family, "", size)
}

func (c *Canvas) cellAt(x, y, width, height float64, text string, align int) error {
	c.PDF.SetXY(x, y)
	return c.PDF.CellWithOption(
		&gopdf.Rect{W: width, H: height},
		text,
		gopdf.CellOption{Align: align | gopdf.Middle},
	)
}
