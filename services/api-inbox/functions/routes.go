// Package functions define la superficie HTTP compartida de api-inbox.
package functions

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Route define un método y path registrados de forma uniforme en Echo.
type Route struct {
	Method string
	Path   string
}

// Register agrega la ruta al servidor local.
func (r Route) Register(e *echo.Echo, handler echo.HandlerFunc) {
	e.Add(r.Method, r.Path, handler)
}

// Routes contiene la superficie HTTP normativa del servicio.
var Routes = []Route{
	GetAgentManagementRoute,
	GetOperationalMetricsRoute,
	SaveAgentDraftRoute,
	PublishAgentConfigRoute,
	RestoreAgentConfigRoute,
	PrepareAgentAssetRoute,
	ListConversationsRoute,
	GetConversationRoute,
	{Method: http.MethodPost, Path: "/conversaciones/:telefono/respuestas"},
	{Method: http.MethodGet, Path: "/plantillas-whatsapp"},
	{Method: http.MethodPost, Path: "/conversaciones/:telefono/plantillas"},
	{Method: http.MethodPut, Path: "/conversaciones/:telefono/reacciones"},
	{Method: http.MethodPost, Path: "/conversaciones/:telefono/mensajes-enriquecidos"},
	{Method: http.MethodPost, Path: "/conversaciones/:telefono/adjuntos/preparar"},
	{Method: http.MethodPost, Path: "/conversaciones/:telefono/adjuntos"},
	{Method: http.MethodPatch, Path: "/conversaciones/:telefono/contacto"},
	{Method: http.MethodPatch, Path: "/conversaciones/:telefono/estado-lectura"},
	{Method: http.MethodPatch, Path: "/conversaciones/:telefono/estado-agente"},
	{Method: http.MethodDelete, Path: "/conversaciones/:telefono"},
	{Method: http.MethodPatch, Path: "/conversaciones/acciones/marcar-leidas"},
	{Method: http.MethodPatch, Path: "/conversaciones/acciones/recalcular-no-leidas"},
	{Method: http.MethodGet, Path: "/conversaciones/suscripcion"},
	{Method: http.MethodGet, Path: "/envios-inbox/:clientMessageId"},
	{Method: http.MethodPost, Path: "/envios-inbox/:clientMessageId/reintentar"},
	{Method: http.MethodPatch, Path: "/envios-inbox/:clientMessageId/resolucion"},
	{Method: http.MethodGet, Path: "/operaciones-inbox"},
	{Method: http.MethodGet, Path: "/operaciones-inbox/:operationId"},
	{Method: http.MethodGet, Path: "/webhooks/whatsapp"},
	{Method: http.MethodPost, Path: "/webhooks/whatsapp"},
}

// ListConversationsRoute es la ruta implementada del listado Inbox.
var ListConversationsRoute = Route{Method: http.MethodGet, Path: "/conversaciones"}
var GetConversationRoute = Route{Method: http.MethodGet, Path: "/conversaciones/:telefono"}
var SendReplyRoute = Route{Method: http.MethodPost, Path: "/conversaciones/:telefono/respuestas"}
var ListTemplatesRoute = Route{Method: http.MethodGet, Path: "/plantillas-whatsapp"}
var SendTemplateRoute = Route{Method: http.MethodPost, Path: "/conversaciones/:telefono/plantillas"}
var SendRichMessageRoute = Route{Method: http.MethodPost, Path: "/conversaciones/:telefono/mensajes-enriquecidos"}
var SendReactionRoute = Route{Method: http.MethodPut, Path: "/conversaciones/:telefono/reacciones"}
var PrepareAttachmentsRoute = Route{Method: http.MethodPost, Path: "/conversaciones/:telefono/adjuntos/preparar"}
var SendAttachmentRoute = Route{Method: http.MethodPost, Path: "/conversaciones/:telefono/adjuntos"}
var GetSendRoute = Route{Method: http.MethodGet, Path: "/envios-inbox/:clientMessageId"}
var RetrySendRoute = Route{Method: http.MethodPost, Path: "/envios-inbox/:clientMessageId/reintentar"}
var ResolveSendRoute = Route{Method: http.MethodPatch, Path: "/envios-inbox/:clientMessageId/resolucion"}
var UpdateContactRoute = Route{Method: http.MethodPatch, Path: "/conversaciones/:telefono/contacto"}
var MarkUnreadRoute = Route{Method: http.MethodPatch, Path: "/conversaciones/:telefono/estado-lectura"}
var ReactivateAgentRoute = Route{Method: http.MethodPatch, Path: "/conversaciones/:telefono/estado-agente"}
var DeleteConversationRoute = Route{Method: http.MethodDelete, Path: "/conversaciones/:telefono"}
var StartMarkAllReadRoute = Route{Method: http.MethodPatch, Path: "/conversaciones/acciones/marcar-leidas"}
var StartRecalculateUnreadRoute = Route{Method: http.MethodPatch, Path: "/conversaciones/acciones/recalcular-no-leidas"}
var GetActiveBulkRoute = Route{Method: http.MethodGet, Path: "/operaciones-inbox"}
var GetBulkRoute = Route{Method: http.MethodGet, Path: "/operaciones-inbox/:operationId"}
var CreateSubscriptionRoute = Route{Method: http.MethodGet, Path: "/conversaciones/suscripcion"}
var VerifyWhatsAppWebhookRoute = Route{Method: http.MethodGet, Path: "/webhooks/whatsapp"}
var ReceiveWhatsAppWebhookRoute = Route{Method: http.MethodPost, Path: "/webhooks/whatsapp"}
var GetAgentManagementRoute = Route{Method: http.MethodGet, Path: "/configuracion-agente"}
var GetOperationalMetricsRoute = Route{Method: http.MethodGet, Path: "/configuracion-agente/metricas"}
var SaveAgentDraftRoute = Route{Method: http.MethodPut, Path: "/configuracion-agente/borrador"}
var PublishAgentConfigRoute = Route{Method: http.MethodPost, Path: "/configuracion-agente/publicaciones"}
var RestoreAgentConfigRoute = Route{Method: http.MethodPost, Path: "/configuracion-agente/restauraciones"}
var PrepareAgentAssetRoute = Route{Method: http.MethodPost, Path: "/configuracion-agente/programas/cargas"}
var PrepareAgentAssetDownloadRoute = Route{Method: http.MethodGet, Path: "/configuracion-agente/programas/descargas"}

// ImplementedRoutes contiene sólo endpoints con handler y binario desplegable.
var ImplementedRoutes = []Route{GetAgentManagementRoute, GetOperationalMetricsRoute, SaveAgentDraftRoute, PublishAgentConfigRoute, RestoreAgentConfigRoute, PrepareAgentAssetRoute, PrepareAgentAssetDownloadRoute, ListConversationsRoute, GetConversationRoute, SendReplyRoute, ListTemplatesRoute, SendTemplateRoute, SendReactionRoute, SendRichMessageRoute, PrepareAttachmentsRoute, SendAttachmentRoute, UpdateContactRoute, MarkUnreadRoute, ReactivateAgentRoute, DeleteConversationRoute, StartMarkAllReadRoute, StartRecalculateUnreadRoute, CreateSubscriptionRoute, GetSendRoute, RetrySendRoute, ResolveSendRoute, GetActiveBulkRoute, GetBulkRoute, VerifyWhatsAppWebhookRoute, ReceiveWhatsAppWebhookRoute}
