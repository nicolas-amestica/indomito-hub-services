package generarpresupuestov1

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"ind-hub-api-gox-sls-pri-gh/services/api-program/functions/generar-presupuesto-v1/templates"
)

func TestComposePDFUsesRegisteredTemplate(t *testing.T) {
	registry := templates.DefaultRegistry()
	template, ok := registry.Lookup(templates.BrochureDefaultID)
	if !ok {
		t.Fatalf("la plantilla %q no está registrada", templates.BrochureDefaultID)
	}
	if template.ID() != templates.BrochureDefaultID {
		t.Errorf("Template.ID() = %q, want %q", template.ID(), templates.BrochureDefaultID)
	}

	document, err := ComposePDF(
		validBudgetRequest(),
		template,
		time.Date(2027, time.March, 4, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("ComposePDF() error = %v", err)
	}
	if len(document) == 0 {
		t.Fatal("ComposePDF() generó un documento vacío")
	}
	if !bytes.HasPrefix(document, []byte("%PDF-")) {
		t.Errorf("el documento no comienza con la firma PDF: %q", document[:min(8, len(document))])
	}
}

func TestComposePDFPaginatesLongServiceLists(t *testing.T) {
	registry := templates.DefaultRegistry()
	template, _ := registry.Lookup(templates.BrochureDefaultID)
	request := validBudgetRequest()
	request.ServiceNames = make([]string, 100)
	for index := range request.ServiceNames {
		request.ServiceNames[index] = fmt.Sprintf("Servicio incluido número %d", index+1)
	}

	document, err := ComposePDF(request, template, time.Now().UTC())
	if err != nil {
		t.Fatalf("ComposePDF() con 100 servicios error = %v", err)
	}
	if len(document) == 0 {
		t.Fatal("ComposePDF() con 100 servicios generó un documento vacío")
	}
}

func TestRegistryRejectsUnknownTemplate(t *testing.T) {
	registry := templates.DefaultRegistry()
	if _, ok := registry.Lookup("no-registrada"); ok {
		t.Error("Lookup aceptó una plantilla desconocida")
	}
}
