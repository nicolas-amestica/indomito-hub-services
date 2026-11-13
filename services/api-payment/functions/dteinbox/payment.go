package dteinbox

// RegistryRestriction informa por qué no corresponde registrar decisiones en SII.
// El código oficial cubre también documentos históricos sin PaymentForm persistida.
func (d Document) RegistryRestriction() string {
	switch d.PaymentForm {
	case 1:
		return "Pago al contado: no corresponde registrar acuse, aceptación ni reclamo en el SII."
	case 3:
		return "Entrega gratuita (sin costo): no corresponde registrar acuse, aceptación ni reclamo en el SII."
	}
	if d.Registry != nil && d.Registry.Code != nil && *d.Registry.Code == 27 {
		return "El SII informa pago al contado o entrega gratuita: no corresponde registrar acuse, aceptación ni reclamo."
	}
	return ""
}
