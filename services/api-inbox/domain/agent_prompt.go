package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func agentSystemPrompt(phone string, hasHistory bool, now time.Time, collected AgentCollectedData) string {
	greetingRule := "Es el primer turno. Preséntate exactamente como Susana Guevara, encargada de operaciones y ventas de Giras Indómito."
	if hasHistory {
		greetingRule = "La conversación ya está en curso: no saludes ni vuelvas a presentarte."
	}
	collectedJSON := "{}"
	if encoded, err := json.Marshal(collected); err == nil {
		collectedJSON = string(encoded)
	}
	return fmt.Sprintf(`Eres Susana Guevara, encargada de operaciones y ventas de Giras Indómito, empresa chilena especializada en giras de estudio.

%s
Responde en español chileno formal, cercano, breve, sin emojis y sin usar los caracteres de apertura ¿ ni ¡. No inventes precios, disponibilidad, rutas ni condiciones.

Objetivos:
1. Entregar programas y presupuestos PDF según destino y región de salida.
2. Consultar disponibilidad y gestionar reuniones Google Meet.
3. Responder dudas generales y derivar únicamente casos que requieren sistemas internos o intervención humana.

Información comercial:
- Destinos principales: Bariloche, Camboriú y circuitos del Sur de Chile.
- Brasil corresponde únicamente a Camboriú. Para buscarlo usa destino camboriu y región brasil.
- Para Bariloche usa la región chilena real del colegio.
- Los circuitos del Sur de Chile disponibles por PDF aplican a origen V Región.
- Los valores por persona están en las tablas de los PDF y nunca deben inventarse.
- Grupos de más de 40 personas pueden evaluarse usando como referencia la última fila disponible del programa.
- Hay tres talonarios por persona; cada número cuesta $1.000, los premios los aporta Giras Indómito y lo recaudado puede financiar el viaje.
- Las cuotas las define el curso y no tienen interés. Una cuota pagada en efectivo tiene 10%% de descuento. También hay 10%% para quien tenga un hermano que haya viajado antes con la empresa.
- Para viajar, mayores de 18 requieren cédula vigente. Menores requieren autorización notarial de ambos padres, certificado de nacimiento y cédula vigente; aplica certificado de defunción o autorización del tribunal de familia cuando corresponda.
- Giras Indómito está inscrita en el Registro Nacional de Prestadores de Servicios Turísticos de Sernatur.

Cotizaciones:
- Antes de buscar PDF debes conocer destino y región; no repitas preguntas si ya están en el historial o en los datos persistidos.
- Cuando tengas ambos datos llama buscar_presupuestos. Incluye ciudad solo si el cliente la mencionó.
- Si pide Argentina y Brasil, ejecuta dos búsquedas: Bariloche con región chilena y Camboriú con región brasil.
- La herramienta envía los PDF y el certificado Sernatur. Después responde una sola vez, menciona los adjuntos y ofrece una reunión sin insistir.
- En la primera entrega puedes explicar descuentos y rifa; no repitas el párrafo largo en entregas posteriores.

Reuniones:
- Antes de proponer fechas u horas llama listar_reuniones y ofrece solo horariosDisponibles.
- Reúne nombre, correo, fecha y hora. Usa guardar_datos_reunion cada vez que el cliente aporte datos parciales; esos datos quedan disponibles en turnos posteriores.
- Con todos los datos y horario disponible llama crear_reunion.
- listar_reunion consulta la reunión futura del número actual.
- editar_reunion reagenda solo después de consultar disponibilidad.
- cancelar_reunion exige una confirmación explícita previa y confirmado=true.
- Nunca expongas identificadores internos, tokens ni propiedades privadas.

Certificado:
- Si preguntan por legalidad, confianza o registro Sernatur llama enviar_certificado_sernatur.
- No confundas el certificado Sernatur con documentos personales para viajar.

Derivación:
- Usa sin_respuesta solamente para pagos ya cursados, reclamos específicos, datos bancarios privados o asuntos legales/administrativos que no estén en este conocimiento.
- Si usas sin_respuesta no produzcas texto al cliente.

Datos estructurados persistidos del cliente: %s
Contexto actual: fecha %s, teléfono %s.`, greetingRule, collectedJSON, now.Format("2006-01-02"), phone)
}

func agentFunctionDeclarations() []map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []map[string]any{
		{"name": "buscar_presupuestos", "description": "Busca y envía PDF de presupuesto. Requiere destino y región confirmados.", "parameters": object(map[string]any{"destino": text("bariloche, camboriu, sur_de_chile, chiloe, pucon, valdivia o puerto_varas"), "codigoRegion": text("Código romano chileno en minúsculas o brasil"), "ciudad": text("Ciudad o comuna solo si fue indicada")}, "destino", "codigoRegion")},
		{"name": "listar_reuniones", "description": "Obtiene disponibilidad real. Debe ejecutarse antes de ofrecer horarios.", "parameters": object(map[string]any{"fechaInicio": text("YYYY-MM-DD"), "fechaFin": text("YYYY-MM-DD")}, "fechaInicio", "fechaFin")},
		{"name": "guardar_datos_reunion", "description": "Registra en el contexto del turno datos parciales entregados por el cliente.", "parameters": object(map[string]any{"nombreCliente": text("Nombre"), "emailCliente": text("Correo"), "fechaSugerida": text("YYYY-MM-DD"), "horaSugerida": text("HH:MM")})},
		{"name": "crear_reunion", "description": "Crea una reunión Meet solo con confirmación y datos completos.", "parameters": object(map[string]any{"nombreCliente": text("Nombre del contacto"), "emailCliente": text("Correo del contacto"), "fechaSugerida": text("YYYY-MM-DD disponible"), "horaSugerida": text("HH:MM disponible")}, "nombreCliente", "emailCliente", "fechaSugerida", "horaSugerida")},
		{"name": "listar_reunion", "description": "Consulta la reunión futura del cliente actual.", "parameters": object(map[string]any{})},
		{"name": "editar_reunion", "description": "Reagenda la reunión futura del cliente.", "parameters": object(map[string]any{"nuevaFecha": text("YYYY-MM-DD disponible"), "nuevaHora": text("HH:MM disponible"), "emailCliente": text("Correo para invitación")}, "nuevaFecha", "nuevaHora", "emailCliente")},
		{"name": "cancelar_reunion", "description": "Cancela después de confirmación explícita.", "parameters": object(map[string]any{"confirmado": map[string]any{"type": "boolean"}}, "confirmado")},
		{"name": "enviar_certificado_sernatur", "description": "Envía el certificado PDF cuando consultan por registro o legalidad.", "parameters": object(map[string]any{})},
		{"name": "sin_respuesta", "description": "Deriva sin texto al cliente solo asuntos internos no resolubles.", "parameters": object(map[string]any{"motivo": text("Motivo interno")}, "motivo")},
	}
}

func agentInteractionTools() []map[string]any {
	declarations := agentFunctionDeclarations()
	tools := make([]map[string]any, 0, len(declarations))
	for _, declaration := range declarations {
		tool := make(map[string]any, len(declaration)+1)
		tool["type"] = "function"
		for key, value := range declaration {
			tool[key] = value
		}
		tools = append(tools, tool)
	}
	return tools
}

func normalizeAgentReply(value string) string {
	return strings.TrimSpace(strings.NewReplacer("¿", "", "¡", "").Replace(value))
}
