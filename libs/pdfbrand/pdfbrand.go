// Package pdfbrand centraliza el arte de marca (header y footer ondeados) que
// comparten los PDF de Giras Indómito, como contratos y comprobantes.
package pdfbrand

import (
	_ "embed"

	"github.com/signintech/gopdf"
)

//go:embed header.png
var headerPNG []byte

//go:embed footer.png
var footerPNG []byte

// Coordenadas del arte sobre una página A4 (595 x 842 pt).
const (
	// HeaderHeight es la altura en puntos que ocupa el header desde el borde superior.
	HeaderHeight = 163.0
	// FooterTop es la coordenada Y donde comienza el footer.
	FooterTop = 718.0
)

// DrawHeader dibuja la barra ondeada con el logo en la parte superior de la página actual.
func DrawHeader(pdf *gopdf.GoPdf) {
	if holder, err := gopdf.ImageHolderByBytes(headerPNG); err == nil {
		_ = pdf.ImageByHolder(holder, -8, 0, &gopdf.Rect{W: 632, H: HeaderHeight})
	}
}

// DrawFooter dibuja la barra ondeada inferior de la página actual.
func DrawFooter(pdf *gopdf.GoPdf) {
	if holder, err := gopdf.ImageHolderByBytes(footerPNG); err == nil {
		_ = pdf.ImageByHolder(holder, -15, FooterTop, &gopdf.Rect{W: 642, H: 74})
	}
}
