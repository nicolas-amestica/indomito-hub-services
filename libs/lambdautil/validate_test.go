package lambdautil

import (
	"testing"

	"ind-hub-api-gox-sls-pri-gh/libs/domain/program"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// TestIsValidDocumentID_AcceptsTheThreeFormats verifica que el validador acepte
// RUT chileno, DNI argentino y CPF brasileño, con y sin formato.
func TestIsValidDocumentID_AcceptsTheThreeFormats(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "RUT con puntos y guion", value: "12.345.678-5"},
		{name: "RUT sin formato", value: "123456785"},
		{name: "RUT con digito verificador K", value: "20.000.003-K"},
		{name: "RUT con digito verificador k minuscula", value: "20000003k"},
		{name: "RUT con digito verificador 0", value: "30.000.001-0"},
		{name: "RUT de excepcion", value: "1-9"},
		{name: "RUT de excepcion con espacios", value: " 1-9 "},
		{name: "DNI argentino de 8 digitos", value: "12345678"},
		{name: "DNI argentino de 7 digitos", value: "1234567"},
		{name: "DNI argentino con puntos", value: "12.345.678"},
		{name: "CPF con formato", value: "111.444.777-35"},
		{name: "CPF sin formato", value: "11144477735"},
		{name: "documento con espacios alrededor", value: "  12.345.678-5  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !IsValidDocumentID(tt.value) {
				t.Errorf("IsValidDocumentID(%q) = false, want true", tt.value)
			}
		})
	}
}

// TestIsValidDocumentID_RejectsInvalidDocuments verifica que un documento con
// dígito verificador equivocado, con largo imposible o con caracteres ajenos
// al formato se rechace.
func TestIsValidDocumentID_RejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "cadena vacia", value: ""},
		{name: "solo espacios", value: "   "},
		{name: "letras", value: "no-es-un-documento"},
		{name: "RUT con digito verificador equivocado", value: "12.345.678-9"},
		{name: "RUT con K en el cuerpo", value: "1234K678-5"},
		{name: "valor parecido a excepcion", value: "1-8"},
		{name: "largo menor que un DNI", value: "123456"},
		{name: "DNI que empieza en cero", value: "0123456"},
		{name: "CPF con segundo digito equivocado", value: "111.444.777-36"},
		{name: "CPF de digitos repetidos", value: "11111111111"},
		{name: "largo entre un RUT y un CPF", value: "1234567890"},
		{name: "largo mayor que un CPF", value: "111444777351"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsValidDocumentID(tt.value) {
				t.Errorf("IsValidDocumentID(%q) = true, want false", tt.value)
			}
		})
	}
}

type validateTarget struct {
	Name  string `json:"name"  validate:"required"`
	Total int    `json:"total" validate:"min=1,max=100"`
}

type nestedTarget struct {
	Crew program.CrewMember `json:"crew" validate:"required"`
}

// TestValidateStruct_AcceptsValidStruct verifica que un struct que cumple sus
// etiquetas no produzca error.
func TestValidateStruct_AcceptsValidStruct(t *testing.T) {
	if err := ValidateStruct(&validateTarget{Name: "gira", Total: 10}); err != nil {
		t.Fatalf("ValidateStruct devolvio error: %v", err)
	}
}

// TestValidateStruct_DistinguishesMissingFromInvalid verifica la distinción que
// el frontend necesita: un campo obligatorio ausente es
// REQUIRED_FIELD_MISSING y un valor fuera de rango es VALIDATION_ERROR. En
// ambos casos el campo del detalle es el nombre del contrato JSON, no el
// identificador Go.
func TestValidateStruct_DistinguishesMissingFromInvalid(t *testing.T) {
	tests := []struct {
		name      string
		target    validateTarget
		wantCode  string
		wantField string
		wantRule  string
	}{
		{
			name:      "campo obligatorio ausente",
			target:    validateTarget{Name: "", Total: 10},
			wantCode:  apperr.CodeRequiredFieldMissing,
			wantField: "name",
			wantRule:  "required",
		},
		{
			name:      "valor bajo el minimo",
			target:    validateTarget{Name: "gira", Total: 0},
			wantCode:  apperr.CodeValidationError,
			wantField: "total",
			wantRule:  "min",
		},
		{
			name:      "valor sobre el maximo",
			target:    validateTarget{Name: "gira", Total: 101},
			wantCode:  apperr.CodeValidationError,
			wantField: "total",
			wantRule:  "max",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStruct(&tt.target)
			if err == nil {
				t.Fatal("ValidateStruct acepto un struct invalido")
			}

			appErr := apperr.From(err)
			if appErr.Code() != tt.wantCode {
				t.Errorf("code = %q, want %q", appErr.Code(), tt.wantCode)
			}
			if got := appErr.Details()["field"]; got != tt.wantField {
				t.Errorf("details.field = %v, want %q", got, tt.wantField)
			}
			if got := appErr.Details()["rule"]; got != tt.wantRule {
				t.Errorf("details.rule = %v, want %q", got, tt.wantRule)
			}
		})
	}
}

// TestValidateStruct_AppliesDocumentIDRule verifica que la regla documentid
// quede registrada y se aplique a los tipos de dominio que la declaran, con la
// ruta del campo anidada según el contrato JSON.
func TestValidateStruct_AppliesDocumentIDRule(t *testing.T) {
	valid := &nestedTarget{Crew: program.CrewMember{
		Name:       "Ana Rojas",
		DocumentID: "12.345.678-5",
		DailyPrice: 25000,
		Currency:   program.CurrencyCLP,
	}}
	if err := ValidateStruct(valid); err != nil {
		t.Fatalf("ValidateStruct rechazo un tripulante valido: %v", err)
	}

	invalid := &nestedTarget{Crew: program.CrewMember{
		Name:       "Ana Rojas",
		DocumentID: "12.345.678-9",
		DailyPrice: 25000,
		Currency:   program.CurrencyCLP,
	}}

	err := ValidateStruct(invalid)
	if err == nil {
		t.Fatal("ValidateStruct acepto un documento invalido")
	}

	appErr := apperr.From(err)
	if appErr.Code() != apperr.CodeValidationError {
		t.Errorf("code = %q, want %q", appErr.Code(), apperr.CodeValidationError)
	}
	if got := appErr.Details()["field"]; got != "crew.documentId" {
		t.Errorf("details.field = %v, want crew.documentId", got)
	}
	if got := appErr.Details()["rule"]; got != TagDocumentID {
		t.Errorf("details.rule = %v, want %q", got, TagDocumentID)
	}
}

// TestValidateStruct_NonStructIsInternalError verifica que pasar algo que no es
// un struct se trate como error de programacion (500) y no como culpa del
// cliente.
func TestValidateStruct_NonStructIsInternalError(t *testing.T) {
	err := ValidateStruct("no soy un struct")
	if err == nil {
		t.Fatal("ValidateStruct acepto un valor que no es struct")
	}
	if code := apperr.From(err).Code(); code != apperr.CodeInternalError {
		t.Errorf("code = %q, want %q", code, apperr.CodeInternalError)
	}
}
