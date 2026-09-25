package lambdautil

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
)

// TagDocumentID es el nombre de la regla de validación de documento de
// identidad que este paquete registra. Los tipos de dominio la usan como
// `validate:"required,documentid"` (ver
// libs/domain/program.CrewMember.DocumentID).
const TagDocumentID = "documentid"

var (
	validatorOnce     sync.Once
	validatorInstance *validator.Validate
)

// sharedValidator devuelve el validador del proceso, construido una sola vez.
// Un *validator.Validate cachea la reflexión de cada struct que valida, así
// que compartirlo entre invocaciones de una misma Lambda tibia evita repetir
// ese trabajo.
func sharedValidator() *validator.Validate {
	validatorOnce.Do(func() {
		instance := validator.New(validator.WithRequiredStructEnabled())

		// El nombre del campo que viaja en un error de validación debe ser el
		// del contrato JSON (`totalPassengers`), no el del identificador Go
		// (`TotalPassengers`): es el que el cliente envió y el que el
		// frontend usa para marcar el control.
		instance.RegisterTagNameFunc(jsonFieldName)

		if err := instance.RegisterValidation(TagDocumentID, documentIDRule); err != nil {
			// Un fallo al registrar la regla deja al validador aceptando
			// documentos inválidos en silencio. Es un error de programación
			// en el arranque, no una condición de runtime.
			panic(fmt.Sprintf("lambdautil: no se pudo registrar el validador %q: %v", TagDocumentID, err))
		}

		validatorInstance = instance
	})

	return validatorInstance
}

// ValidateStruct valida target contra las etiquetas `validate` de sus campos y
// traduce el primer incumplimiento a un *apperr.AppError.
//
// El código depende de la regla incumplida: un campo obligatorio ausente
// produce REQUIRED_FIELD_MISSING y cualquier otro incumplimiento produce
// VALIDATION_ERROR. Esa distinción es la que permite al frontend diferenciar
// "falta completar" de "el valor no sirve".
//
// Los detalles del error llevan el campo y la regla incumplida, ambos datos
// que el cliente ya conoce. Nunca llevan el valor recibido si es un dato
// personal: el mensaje describe el campo, no su contenido.
func ValidateStruct(target any) error {
	err := sharedValidator().Struct(target)
	if err == nil {
		return nil
	}

	var fieldErrors validator.ValidationErrors
	if errors.As(err, &fieldErrors) && len(fieldErrors) > 0 {
		return validationError(fieldErrors[0])
	}

	// Un InvalidValidationError significa que se pasó algo que no es un
	// struct: es un error de programación del handler, no del cliente.
	return apperr.Internal(err)
}

// validationError traduce un incumplimiento concreto al AppError que le
// corresponde.
func validationError(fieldErr validator.FieldError) *apperr.AppError {
	field := fieldPath(fieldErr)
	details := map[string]any{
		"field": field,
		"rule":  fieldErr.Tag(),
	}
	if param := fieldErr.Param(); param != "" {
		details["param"] = param
	}

	if fieldErr.Tag() == "required" {
		return apperr.RequiredFieldMissingf("El campo %s es obligatorio", field).
			WithDetails(details)
	}

	return apperr.Validation(validationMessage(field, fieldErr)).WithDetails(details)
}

// validationMessage arma el mensaje en español que llega al usuario final
// según la regla incumplida.
func validationMessage(field string, fieldErr validator.FieldError) string {
	switch fieldErr.Tag() {
	case TagDocumentID:
		return fmt.Sprintf("El campo %s no es un RUT, DNI ni CPF válido", field)
	case "min", "gte":
		return fmt.Sprintf("El campo %s no puede ser menor que %s", field, fieldErr.Param())
	case "max", "lte":
		return fmt.Sprintf("El campo %s no puede ser mayor que %s", field, fieldErr.Param())
	case "oneof":
		return fmt.Sprintf("El campo %s tiene un valor no permitido", field)
	case "datetime":
		return fmt.Sprintf("El campo %s no tiene el formato de fecha esperado", field)
	default:
		return fmt.Sprintf("El campo %s no es válido", field)
	}
}

// fieldPath devuelve la ruta del campo dentro del cuerpo recibido, sin el
// nombre del struct raíz: `content.schedule.totalPassengers` en vez de
// `FavoriteContent.content.schedule.totalPassengers`.
func fieldPath(fieldErr validator.FieldError) string {
	namespace := fieldErr.Namespace()
	if index := strings.Index(namespace, "."); index >= 0 {
		return namespace[index+1:]
	}
	return namespace
}

// jsonFieldName extrae el nombre del campo de su etiqueta json. Un campo con
// `json:"-"` queda sin nombre y el validador cae de vuelta al identificador
// Go.
func jsonFieldName(field reflect.StructField) string {
	name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
	if name == "-" {
		return ""
	}
	return name
}

// documentIDRule adapta [IsValidDocumentID] a la interfaz de validator. Un
// campo que no es una cadena falla: la regla solo aplica a documentos de
// identidad declarados como string.
func documentIDRule(fieldLevel validator.FieldLevel) bool {
	value := fieldLevel.Field()
	if value.Kind() != reflect.String {
		return false
	}
	return IsValidDocumentID(value.String())
}

// IsValidDocumentID indica si value corresponde a un RUT chileno, un DNI
// argentino o un CPF brasileño válido, con o sin puntos, guiones y espacios
// (Requirement 5.9). Es el gemelo en Go de
// `shared/validators/document-id.validator.ts` del frontend: una divergencia
// entre ambos es un defecto.
//
// Basta que uno de los tres formatos acepte el valor. Como el DNI argentino no
// tiene dígito verificador, una cadena de 7 u 8 dígitos se acepta por ser un
// DNI plausible aunque no sea un RUT válido; eso es inherente al formato y no
// una laxitud de esta implementación.
func IsValidDocumentID(value string) bool {
	if strings.TrimSpace(value) == "1-9" {
		return true
	}

	normalized := normalizeDocumentID(value)
	if normalized == "" {
		return false
	}

	return isValidRUT(normalized) || isValidDNI(normalized) || isValidCPF(normalized)
}

// normalizeDocumentID quita puntos, guiones y espacios, y pasa la K del
// dígito verificador chileno a mayúscula.
func normalizeDocumentID(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToUpper(strings.TrimSpace(value)) {
		switch {
		case char >= '0' && char <= '9', char == 'K':
			builder.WriteRune(char)
		case char == '.', char == '-', char == ' ':
			// Separadores de formato: se descartan.
		default:
			// Cualquier otro carácter invalida el documento completo.
			return ""
		}
	}
	return builder.String()
}

// isValidRUT valida un RUT chileno: entre 7 y 8 dígitos de cuerpo más el
// dígito verificador de módulo 11, que puede ser un dígito o la letra K.
func isValidRUT(normalized string) bool {
	if len(normalized) < 8 || len(normalized) > 9 {
		return false
	}

	body := normalized[:len(normalized)-1]
	checkDigit := normalized[len(normalized)-1]

	if !isAllDigits(body) {
		return false
	}

	return checkDigit == rutCheckDigit(body)
}

// rutCheckDigit calcula el dígito verificador de módulo 11 del cuerpo de un
// RUT, con la serie de multiplicadores 2..7 recorrida de derecha a izquierda.
func rutCheckDigit(body string) byte {
	sum := 0
	multiplier := 2

	for index := len(body) - 1; index >= 0; index-- {
		sum += int(body[index]-'0') * multiplier
		multiplier++
		if multiplier > 7 {
			multiplier = 2
		}
	}

	switch remainder := 11 - (sum % 11); remainder {
	case 11:
		return '0'
	case 10:
		return 'K'
	default:
		return byte('0' + remainder)
	}
}

// isValidDNI valida un DNI argentino: 7 u 8 dígitos, sin dígito verificador.
// Se descarta el 0 como primer dígito porque no existe un DNI que empiece en
// cero.
func isValidDNI(normalized string) bool {
	if len(normalized) < 7 || len(normalized) > 8 {
		return false
	}
	if normalized[0] == '0' {
		return false
	}
	return isAllDigits(normalized)
}

// isValidCPF valida un CPF brasileño: 11 dígitos y sus dos dígitos
// verificadores de módulo 11. Los once dígitos repetidos se rechazan: pasan la
// aritmética pero ninguno es un CPF emitido.
func isValidCPF(normalized string) bool {
	const cpfLength = 11

	if len(normalized) != cpfLength || !isAllDigits(normalized) {
		return false
	}
	if strings.Count(normalized, normalized[:1]) == cpfLength {
		return false
	}

	first := cpfCheckDigit(normalized[:9], 10)
	if normalized[9] != first {
		return false
	}

	second := cpfCheckDigit(normalized[:10], 11)
	return normalized[10] == second
}

// cpfCheckDigit calcula un dígito verificador de CPF sobre digits, con la
// serie de multiplicadores que arranca en weight y decrece.
func cpfCheckDigit(digits string, weight int) byte {
	sum := 0
	for index := 0; index < len(digits); index++ {
		sum += int(digits[index]-'0') * (weight - index)
	}

	remainder := (sum * 10) % 11
	if remainder == 10 {
		return '0'
	}
	return byte('0' + remainder)
}

// isAllDigits indica si value está compuesto solo por dígitos y no está
// vacío.
func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}
