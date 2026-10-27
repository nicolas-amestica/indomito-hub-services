package domain

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// AgentAssetConfig identifies the permanent documents used by the commercial agent.
type AgentAssetConfig struct {
	BudgetBucketName       string `json:"budgetBucketName"`
	BudgetPrefix           string `json:"budgetPrefix"`
	BudgetExceptionsPrefix string `json:"budgetExceptionsPrefix"`
	SernaturCertificateKey string `json:"sernaturCertificateKey"`
}

// AgentMeetingConfig contains the calendar and scheduling policy used by the agent.
type AgentMeetingConfig struct {
	CalendarOAuthParameterPath string   `json:"calendarOAuthParameterPath"`
	CalendarID                 string   `json:"calendarId"`
	Timezone                   string   `json:"timezone"`
	DefaultGuestEmails         []string `json:"defaultGuestEmails"`
	DefaultHostSK              string   `json:"defaultHostSk"`
	DefaultHostName            string   `json:"defaultHostName"`
	SlotDurationMinutes        int      `json:"slotDurationMinutes"`
	MinAdvanceMinutes          int      `json:"minAdvanceMinutes"`
	WeekdayStartHour           int      `json:"weekdayStartHour"`
	WeekdayEndHour             int      `json:"weekdayEndHour"`
	SaturdayStartHour          int      `json:"saturdayStartHour"`
	SaturdayEndHour            int      `json:"saturdayEndHour"`
}

// GoogleOAuthConfig contains a refresh-token OAuth client without exposing it to Gemini.
type GoogleOAuthConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

type agentAssetStore interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// AgentToolbox executes trusted side effects requested through Gemini function calling.
type AgentToolbox struct {
	storage agentAssetStore
	sender  AgentSender
	assets  AgentAssetConfig
	meeting AgentMeetingConfig
	oauth   GoogleOAuthConfig
	http    *http.Client
	now     func() time.Time
	managed *AgentManagedConfig
}

func (t *AgentToolbox) SetManagedConfig(config *AgentManagedConfig) { t.managed = config }

type agentExecutionState struct {
	certificateSent       bool
	derivation            *AgentDerivation
	latestCustomerMessage string
	collected             AgentCollectedData
	collectedDirty        bool
}

// AgentCollectedData is the short-lived structured context required to resume
// a meeting or quotation flow after a later WhatsApp message.
type AgentCollectedData struct {
	Destination string `json:"destino,omitempty" dynamodbav:"destino,omitempty"`
	Region      string `json:"region,omitempty" dynamodbav:"region,omitempty"`
	City        string `json:"ciudad,omitempty" dynamodbav:"ciudad,omitempty"`
	MeetingName string `json:"nombreCliente,omitempty" dynamodbav:"nombreCliente,omitempty"`
	MeetingMail string `json:"emailCliente,omitempty" dynamodbav:"emailCliente,omitempty"`
	MeetingDate string `json:"fechaSugerida,omitempty" dynamodbav:"fechaSugerida,omitempty"`
	MeetingTime string `json:"horaSugerida,omitempty" dynamodbav:"horaSugerida,omitempty"`
}

// AgentDerivation describes a persistent handoff requested by the model.
type AgentDerivation struct {
	Reason string
	Label  string
}

// NewAgentToolbox validates and creates the commercial tools used by AgentService.
func NewAgentToolbox(storage agentAssetStore, sender AgentSender, assets AgentAssetConfig, meeting AgentMeetingConfig, oauth GoogleOAuthConfig) (*AgentToolbox, error) {
	if storage == nil || sender == nil || assets.BudgetBucketName == "" || assets.BudgetPrefix == "" || meeting.CalendarID == "" || meeting.Timezone == "" {
		return nil, errors.New("agent tool configuration is incomplete")
	}
	if oauth.ClientID == "" || oauth.ClientSecret == "" || oauth.RefreshToken == "" || meeting.SlotDurationMinutes < 1 || meeting.MinAdvanceMinutes < 0 {
		return nil, errors.New("agent meeting configuration is invalid")
	}
	return &AgentToolbox{storage: storage, sender: sender, assets: assets, meeting: meeting, oauth: oauth, http: &http.Client{Timeout: 25 * time.Second}, now: time.Now}, nil
}

func (t *AgentToolbox) execute(ctx context.Context, phone, messageID, name string, args map[string]any, state *agentExecutionState) (map[string]any, bool, error) {
	if t.managed != nil {
		if (name == "sin_respuesta" || name == "derivar_equipo") && !t.managed.Policies.HumanHandoffEnabled {
			return map[string]any{"derivado": false, "motivo": "La derivacion humana esta deshabilitada. Responde usando el conocimiento disponible."}, false, nil
		}
		if isMeetingTool(name) && !t.managed.Policies.OfferMeetings {
			return map[string]any{"exito": false, "mensaje": "La gestion de reuniones esta deshabilitada."}, false, nil
		}
	}
	switch name {
	case "sin_respuesta", "derivar_equipo":
		reason := stringArg(args, "motivo")
		if reason == "" {
			reason = "agent_handoff"
		}
		if isHumanHandoffReason(reason) && !isExplicitHumanHandoff(state.latestCustomerMessage) {
			return map[string]any{"derivado": false, "motivo": "La solicitud de atención humana no está en el mensaje más reciente. Responde la consulta actual sin derivar."}, false, nil
		}
		state.derivation = &AgentDerivation{Reason: reason, Label: "Requiere atencion del equipo"}
		return map[string]any{"derivado": true}, true, nil
	case "guardar_datos_reunion":
		state.mergeMeeting(args)
		return map[string]any{"guardado": true, "datos": state.collected}, false, nil
	case "buscar_presupuestos":
		state.mergeQuote(args)
		return t.sendBudgets(ctx, phone, messageID, stringArg(args, "destino"), stringArg(args, "codigoRegion"), stringArg(args, "ciudad"), state)
	case "enviar_certificado_sernatur":
		result, err := t.sendCertificate(ctx, phone, messageID, "explicito")
		if err == nil {
			state.certificateSent = true
		}
		return result, false, err
	case "listar_reuniones":
		result, err := t.listMeetings(ctx, stringArg(args, "fechaInicio"), stringArg(args, "fechaFin"), "")
		return result, false, err
	case "listar_reunion":
		result, err := t.listCustomerMeeting(ctx, phone)
		return result, false, err
	case "crear_reunion":
		result, err := t.createMeeting(ctx, phone, args)
		if err == nil && boolResult(result, "exito") {
			state.clearMeeting()
		}
		return result, false, err
	case "editar_reunion", "reagendar_reunion":
		result, err := t.updateMeeting(ctx, phone, args)
		return result, false, err
	case "cancelar_reunion":
		if confirmed, _ := args["confirmado"].(bool); !confirmed {
			return map[string]any{"exito": false, "mensaje": "La cancelación requiere confirmación explícita."}, false, nil
		}
		result, err := t.cancelMeeting(ctx, phone)
		return result, false, err
	default:
		return map[string]any{"error": "herramienta no permitida"}, false, nil
	}
}

func isMeetingTool(name string) bool {
	switch name {
	case "guardar_datos_reunion", "listar_reuniones", "listar_reunion", "crear_reunion", "editar_reunion", "reagendar_reunion", "cancelar_reunion":
		return true
	default:
		return false
	}
}

func isHumanHandoffReason(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	for _, phrase := range []string{"atencion humana", "atención humana", "persona", "agente real", "agente humano", "ejecutivo", "humano"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func (s *agentExecutionState) mergeMeeting(args map[string]any) {
	mergeCollectedString(&s.collected.MeetingName, stringArg(args, "nombreCliente"), &s.collectedDirty)
	mergeCollectedString(&s.collected.MeetingMail, strings.ToLower(stringArg(args, "emailCliente")), &s.collectedDirty)
	mergeCollectedString(&s.collected.MeetingDate, stringArg(args, "fechaSugerida"), &s.collectedDirty)
	mergeCollectedString(&s.collected.MeetingTime, stringArg(args, "horaSugerida"), &s.collectedDirty)
}

func (s *agentExecutionState) mergeQuote(args map[string]any) {
	mergeCollectedString(&s.collected.Destination, normalizeSegment(stringArg(args, "destino")), &s.collectedDirty)
	mergeCollectedString(&s.collected.Region, normalizeSegment(stringArg(args, "codigoRegion")), &s.collectedDirty)
	mergeCollectedString(&s.collected.City, normalizeSegment(stringArg(args, "ciudad")), &s.collectedDirty)
}

func (s *agentExecutionState) clearMeeting() {
	if s.collected.MeetingName != "" || s.collected.MeetingMail != "" || s.collected.MeetingDate != "" || s.collected.MeetingTime != "" {
		s.collected.MeetingName, s.collected.MeetingMail = "", ""
		s.collected.MeetingDate, s.collected.MeetingTime = "", ""
		s.collectedDirty = true
	}
}

func mergeCollectedString(target *string, value string, dirty *bool) {
	if value != "" && *target != value {
		*target = value
		*dirty = true
	}
}

func boolResult(result map[string]any, key string) bool {
	value, _ := result[key].(bool)
	return value
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func normalizeSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n", " ", "_")
	return replacer.Replace(value)
}

var budgetRegionByCity = map[string]string{
	"arica": "xv", "putre": "xv", "alto_hospicio": "i", "iquique": "i", "pica": "i", "huara": "i", "camina": "i",
	"antofagasta": "ii", "calama": "ii", "mejillones": "ii", "tocopilla": "ii", "san_pedro_de_atacama": "ii",
	"copiapo": "iii", "vallenar": "iii", "la_serena": "iv", "coquimbo": "iv", "ovalle": "iv", "illapel": "iv", "los_vilos": "iv",
	"valparaiso": "v", "vina_del_mar": "v", "vina": "v", "quilpue": "v", "villa_alemana": "v", "san_felipe": "vi", "los_andes": "vi",
	"rancagua": "vi", "san_fernando": "vi", "curico": "vii", "talca": "vii", "linares": "vii", "chillan": "xvi", "chillan_viejo": "xvi", "san_carlos": "xvi",
	"concepcion": "viii", "los_angeles": "viii", "temuco": "ix", "villarrica": "ix", "pucon": "ix", "valdivia": "xiv", "lago_ranco": "xiv", "futrono": "xiv", "rio_bueno": "xiv", "la_union": "xiv",
	"osorno": "x", "puerto_montt": "x", "castro": "x", "ancud": "x", "puerto_varas": "x", "frutillar": "x", "fresia": "x",
	"coyhaique": "xi", "puerto_aysen": "xi", "aysen": "xi", "chile_chico": "xi", "cochrane": "xi", "puerto_cisnes": "xi", "cisnes": "xi", "lago_verde": "xi", "tortel": "xi", "caleta_tortel": "xi", "rio_ibanez": "xi",
	"puerto_natales": "xii", "punta_arenas": "xii", "santiago": "rm", "maipu": "rm", "puente_alto": "rm", "nunoa": "rm", "las_condes": "rm", "providencia": "rm", "vitacura": "rm", "la_florida": "rm", "penalolen": "rm", "huechuraba": "rm", "recoleta": "rm", "independencia": "rm", "estacion_central": "rm", "cerrillos": "rm", "pudahuel": "rm", "quilicura": "rm", "colina": "rm", "lampa": "rm", "melipilla": "rm", "talagante": "rm", "san_bernardo": "rm", "la_pintana": "rm", "el_bosque": "rm", "la_granja": "rm", "san_miguel": "rm",
}

func fileContainsDestination(key, destination string) bool {
	name := strings.TrimSuffix(strings.ToLower(path.Base(key)), ".pdf")
	parts := strings.Split(name, "-")
	for _, part := range parts[:max(0, len(parts)-1)] {
		if part == destination {
			return true
		}
	}
	return false
}

func (t *AgentToolbox) listPDFs(ctx context.Context, prefix, destination string) ([]string, error) {
	var token *string
	var keys []string
	for {
		out, err := t.storage.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(t.assets.BudgetBucketName), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, fmt.Errorf("list budget assets: %w", err)
		}
		for _, item := range out.Contents {
			if item.Key != nil && strings.HasSuffix(strings.ToLower(*item.Key), ".pdf") && fileContainsDestination(*item.Key, destination) {
				keys = append(keys, *item.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}

func (t *AgentToolbox) sendBudgets(ctx context.Context, phone, messageID, destination, region, city string, state *agentExecutionState) (map[string]any, bool, error) {
	destination = normalizeSegment(destination)
	region = normalizeSegment(region)
	city = normalizeSegment(city)
	if mappedRegion := budgetRegionByCity[city]; mappedRegion != "" {
		region = mappedRegion
	}
	if destination == "camboriu" {
		destination, region = "brasil", "brasil"
	}
	if destination == "" || region == "" {
		return map[string]any{"encontrados": false, "mensaje": "Destino y región son obligatorios."}, false, nil
	}
	var keys []string
	var err error
	if t.managed != nil && len(t.managed.Programs) > 0 {
		keys = managedBudgetKeys(t.managed.Programs, destination, region, city)
	}
	if len(keys) == 0 && city != "" {
		prefix := strings.TrimSuffix(t.assets.BudgetExceptionsPrefix, "/") + "/" + city + "/"
		keys, err = t.listPDFs(ctx, prefix, destination)
	}
	if err == nil && len(keys) == 0 {
		prefix := strings.TrimSuffix(t.assets.BudgetPrefix, "/") + "/" + region + "/"
		keys, err = t.listPDFs(ctx, prefix, destination)
	}
	if err != nil {
		return nil, false, err
	}
	if len(keys) == 0 {
		return map[string]any{"encontrados": false, "archivos": []string{}}, false, nil
	}
	files := make([]string, 0, len(keys))
	for index, key := range keys {
		data, contentType, readErr := t.readAsset(ctx, key)
		if readErr != nil {
			return nil, false, readErr
		}
		name := path.Base(key)
		if contentType == "" {
			contentType = "application/pdf"
		}
		if _, sendErr := t.sender.SendAgentMedia(ctx, phone, fmt.Sprintf("agent-%s-budget-%d", messageID, index+1), name, contentType, "Programa y presupuesto solicitado", data); sendErr != nil {
			return nil, false, fmt.Errorf("send budget %s: %w", name, sendErr)
		}
		files = append(files, name)
	}
	result := map[string]any{"encontrados": true, "archivos": files, "certificadoSernatur": map[string]any{"enviado": false, "yaEnviado": state.certificateSent}}
	if !state.certificateSent && (t.managed == nil || t.managed.Policies.SendSernaturWithQuotations) {
		certificate, certificateErr := t.sendCertificate(ctx, phone, messageID+"-budget", "post_presupuesto")
		result["certificadoSernatur"] = certificate
		if certificateErr != nil {
			result["advertenciaCertificado"] = "No fue posible adjuntar el certificado Sernatur."
		} else {
			state.certificateSent = true
		}
	}
	return result, false, nil
}

func managedBudgetKeys(programs []AgentProgram, destination, region, city string) []string {
	destination, region, city = normalizeSegment(destination), normalizeSegment(region), normalizeSegment(city)
	selected := make(map[string]struct{})
	if city != "" {
		for _, program := range programs {
			if !program.Active || program.DocumentKey == "" || !agentProgramSupportsDestination(program, destination) || !segmentListContains(program.OriginCities, city) {
				continue
			}
			selected[program.DocumentKey] = struct{}{}
		}
	}
	if len(selected) == 0 {
		for _, program := range programs {
			if !program.Active || program.DocumentKey == "" || len(program.OriginCities) > 0 || !agentProgramSupportsDestination(program, destination) || !segmentListContains(program.OriginRegions, region) {
				continue
			}
			selected[program.DocumentKey] = struct{}{}
		}
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func segmentListContains(values []string, expected string) bool {
	for _, value := range values {
		if normalizeSegment(value) == expected {
			return true
		}
	}
	return false
}

func (t *AgentToolbox) sendCertificate(ctx context.Context, phone, messageID, reason string) (map[string]any, error) {
	data, contentType, err := t.readAsset(ctx, t.assets.SernaturCertificateKey)
	if err != nil {
		return map[string]any{"enviado": false}, err
	}
	if contentType == "" {
		contentType = "application/pdf"
	}
	caption := "Certificado de registro Sernatur de Giras Indómito."
	if reason == "post_presupuesto" {
		caption = "También adjuntamos nuestro certificado de registro Sernatur para su seguridad y confianza."
	}
	_, err = t.sender.SendAgentMedia(ctx, phone, "agent-"+messageID+"-sernatur", path.Base(t.assets.SernaturCertificateKey), contentType, caption, data)
	return map[string]any{"enviado": err == nil}, err
}

func (t *AgentToolbox) readAsset(ctx context.Context, key string) ([]byte, string, error) {
	out, err := t.storage.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(t.assets.BudgetBucketName), Key: aws.String(key)})
	if err != nil {
		return nil, "", fmt.Errorf("read agent asset %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(out.Body, 32<<20))
	if err != nil {
		return nil, "", fmt.Errorf("read agent asset body: %w", err)
	}
	if len(data) == 0 || len(data) >= 32<<20 {
		return nil, "", errors.New("agent asset is empty or exceeds 32 MiB")
	}
	return data, aws.ToString(out.ContentType), nil
}

type calendarEvent struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	HangoutLink string `json:"hangoutLink,omitempty"`
	Start       struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
		TimeZone string `json:"timeZone"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
		TimeZone string `json:"timeZone"`
	} `json:"end"`
	Attendees          []map[string]any             `json:"attendees,omitempty"`
	ExtendedProperties map[string]map[string]string `json:"extendedProperties,omitempty"`
}

func (t *AgentToolbox) accessToken(ctx context.Context) (string, error) {
	form := url.Values{"client_id": {t.oauth.ClientID}, "client_secret": {t.oauth.ClientSecret}, "refresh_token": {t.oauth.RefreshToken}, "grant_type": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh calendar token: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode/100 != 2 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload) != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("refresh calendar token status %d", response.StatusCode)
	}
	return payload.AccessToken, nil
}

func (t *AgentToolbox) calendarRequest(ctx context.Context, method, eventID string, query url.Values, body any, target any) error {
	token, err := t.accessToken(ctx)
	if err != nil {
		return err
	}
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/" + url.PathEscape(t.meeting.CalendarID) + "/events"
	if eventID != "" {
		endpoint += "/" + url.PathEscape(eventID)
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	response, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("calendar request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("calendar status %d", response.StatusCode)
	}
	if target != nil && response.StatusCode != http.StatusNoContent {
		if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(target); err != nil {
			return fmt.Errorf("decode calendar response: %w", err)
		}
	}
	return nil
}

func (t *AgentToolbox) calendarEvents(ctx context.Context, start, end, phone string) ([]calendarEvent, error) {
	query := url.Values{"singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"2500"}}
	if start != "" {
		query.Set("timeMin", start)
	}
	if end != "" {
		query.Set("timeMax", end)
	}
	if phone != "" {
		query.Set("privateExtendedProperty", "phoneNumber="+strings.TrimPrefix(phone, "+"))
	}
	var events []calendarEvent
	for {
		var payload struct {
			Items         []calendarEvent `json:"items"`
			NextPageToken string          `json:"nextPageToken"`
		}
		if err := t.calendarRequest(ctx, http.MethodGet, "", query, nil, &payload); err != nil {
			return nil, err
		}
		events = append(events, payload.Items...)
		if payload.NextPageToken == "" {
			break
		}
		query.Set("pageToken", payload.NextPageToken)
	}
	return events, nil
}

func (t *AgentToolbox) listMeetings(ctx context.Context, startDate, endDate, excludeID string) (map[string]any, error) {
	location, err := time.LoadLocation(t.meeting.Timezone)
	if err != nil {
		return nil, err
	}
	start, err := time.ParseInLocation("2006-01-02", startDate, location)
	if err != nil {
		return map[string]any{"error": "fechaInicio inválida"}, nil
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, location)
	if err != nil || end.Before(start) || end.Sub(start) > 31*24*time.Hour {
		return map[string]any{"error": "fechaFin inválida o rango mayor a 31 días"}, nil
	}
	events, err := t.calendarEvents(ctx, start.UTC().Format(time.RFC3339), end.Add(24*time.Hour).UTC().Format(time.RFC3339), "")
	if err != nil {
		return nil, err
	}
	availability := map[string]any{}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		availability[date] = map[string]any{"diaHabil": t.isWorkday(day), "esFestivo": isChileanHoliday(day), "horariosDisponibles": t.availableSlots(day, events, excludeID)}
	}
	return map[string]any{"reuniones": t.safeEvents(events), "disponibilidadPorDia": availability}, nil
}

func (t *AgentToolbox) safeEvents(events []calendarEvent) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		if event.Status == "cancelled" {
			continue
		}
		result = append(result, map[string]any{"id": event.ID, "titulo": event.Summary, "fecha": firstN(event.Start.DateTime, 10), "horaInicio": clock(event.Start.DateTime), "horaFin": clock(event.End.DateTime), "meetLink": event.HangoutLink})
	}
	return result
}

func (t *AgentToolbox) isWorkday(day time.Time) bool {
	return day.Weekday() != time.Sunday && !isChileanHoliday(day)
}

func (t *AgentToolbox) availableSlots(day time.Time, events []calendarEvent, excludeID string) []string {
	if !t.isWorkday(day) {
		return []string{}
	}
	startHour, endHour := t.meeting.WeekdayStartHour, t.meeting.WeekdayEndHour
	if day.Weekday() == time.Saturday {
		startHour, endHour = t.meeting.SaturdayStartHour, t.meeting.SaturdayEndHour
	}
	var result []string
	for minute := startHour * 60; minute+t.meeting.SlotDurationMinutes <= endHour*60; minute += t.meeting.SlotDurationMinutes {
		start := time.Date(day.Year(), day.Month(), day.Day(), minute/60, minute%60, 0, 0, day.Location())
		finish := start.Add(time.Duration(t.meeting.SlotDurationMinutes) * time.Minute)
		if start.Before(t.now().Add(time.Duration(t.meeting.MinAdvanceMinutes)*time.Minute)) || overlapsCalendar(start, finish, events, excludeID) {
			continue
		}
		result = append(result, start.Format("15:04"))
	}
	return result
}

func overlapsCalendar(start, finish time.Time, events []calendarEvent, excludeID string) bool {
	for _, event := range events {
		if event.ID == excludeID || event.Status == "cancelled" || event.Start.DateTime == "" || event.End.DateTime == "" {
			continue
		}
		eventStart, startErr := time.Parse(time.RFC3339, event.Start.DateTime)
		eventEnd, endErr := time.Parse(time.RFC3339, event.End.DateTime)
		if startErr == nil && endErr == nil && start.Before(eventEnd) && eventStart.Before(finish) {
			return true
		}
	}
	return false
}

func (t *AgentToolbox) validateSlot(ctx context.Context, date, clockValue, excludeID string) ([]string, bool, error) {
	location, err := time.LoadLocation(t.meeting.Timezone)
	if err != nil {
		return nil, false, err
	}
	day, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return []string{}, false, nil
	}
	events, err := t.calendarEvents(ctx, day.UTC().Format(time.RFC3339), day.Add(24*time.Hour).UTC().Format(time.RFC3339), "")
	if err != nil {
		return nil, false, err
	}
	slots := t.availableSlots(day, events, excludeID)
	for _, slot := range slots {
		if slot == clockValue {
			return slots, true, nil
		}
	}
	return slots, false, nil
}

func (t *AgentToolbox) createMeeting(ctx context.Context, phone string, args map[string]any) (map[string]any, error) {
	name, email := stringArg(args, "nombreCliente"), stringArg(args, "emailCliente")
	date, clockValue := stringArg(args, "fechaSugerida"), stringArg(args, "horaSugerida")
	if name == "" || email == "" || !strings.Contains(email, "@") {
		return map[string]any{"exito": false, "mensaje": "Nombre y correo válido son obligatorios."}, nil
	}
	existing, err := t.customerMeetings(ctx, phone)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return map[string]any{"exito": false, "mensaje": "Ya existe una reunión futura para este contacto.", "eventos": t.safeEvents(existing)}, nil
	}
	slots, valid, err := t.validateSlot(ctx, date, clockValue, "")
	if err != nil || !valid {
		return map[string]any{"exito": false, "mensaje": "El horario no está disponible.", "horariosDisponibles": slots}, err
	}
	code, err := meetingCode()
	if err != nil {
		return nil, err
	}
	attendees := t.attendees(email)
	body := map[string]any{
		"summary": fmt.Sprintf("AG — %s [%s]", name, code), "description": "Reunión informativa sobre giras de estudio.", "colorId": "4",
		"start": map[string]string{"dateTime": date + "T" + clockValue + ":00", "timeZone": t.meeting.Timezone},
		"end":   map[string]string{"dateTime": t.endDateTime(date, clockValue), "timeZone": t.meeting.Timezone}, "attendees": attendees,
		"reminders":          map[string]any{"useDefault": false, "overrides": []map[string]any{{"method": "popup", "minutes": 60}, {"method": "popup", "minutes": 15}}},
		"extendedProperties": map[string]any{"private": map[string]string{"phoneNumber": strings.TrimPrefix(phone, "+"), "eventCode": code}, "shared": map[string]string{"createdBy": "indomito-inbox-agent", "defaultHostSk": t.meeting.DefaultHostSK, "defaultHostName": t.meeting.DefaultHostName}},
		"conferenceData":     map[string]any{"createRequest": map[string]any{"requestId": code + "-" + strconv.FormatInt(t.now().UnixNano(), 10), "conferenceSolutionKey": map[string]string{"type": "hangoutsMeet"}}},
	}
	var event calendarEvent
	if err = t.calendarRequest(ctx, http.MethodPost, "", url.Values{"conferenceDataVersion": {"1"}, "sendUpdates": {"all"}}, body, &event); err != nil {
		return nil, err
	}
	return map[string]any{"exito": true, "eventId": event.ID, "codigoEvento": code, "fecha": date, "hora": clockValue, "meetLink": event.HangoutLink}, nil
}

func (t *AgentToolbox) customerMeetings(ctx context.Context, phone string) ([]calendarEvent, error) {
	events, err := t.calendarEvents(ctx, t.now().UTC().Format(time.RFC3339), "", phone)
	if err != nil {
		return nil, err
	}
	active := events[:0]
	for _, event := range events {
		if event.Status != "cancelled" {
			active = append(active, event)
		}
	}
	return active, nil
}

func (t *AgentToolbox) listCustomerMeeting(ctx context.Context, phone string) (map[string]any, error) {
	events, err := t.customerMeetings(ctx, phone)
	if err != nil {
		return nil, err
	}
	return map[string]any{"encontrados": len(events) > 0, "eventos": t.safeEvents(events)}, nil
}

func (t *AgentToolbox) updateMeeting(ctx context.Context, phone string, args map[string]any) (map[string]any, error) {
	events, err := t.customerMeetings(ctx, phone)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return map[string]any{"exito": false, "mensaje": "No se encontró una reunión agendada."}, nil
	}
	event := events[0]
	date, clockValue, email := stringArg(args, "nuevaFecha"), stringArg(args, "nuevaHora"), stringArg(args, "emailCliente")
	slots, valid, err := t.validateSlot(ctx, date, clockValue, event.ID)
	if err != nil || !valid {
		return map[string]any{"exito": false, "mensaje": "El horario no está disponible.", "horariosDisponibles": slots}, err
	}
	body := map[string]any{"start": map[string]string{"dateTime": date + "T" + clockValue + ":00", "timeZone": t.meeting.Timezone}, "end": map[string]string{"dateTime": t.endDateTime(date, clockValue), "timeZone": t.meeting.Timezone}, "attendees": t.mergeAttendees(event.Attendees, email), "extendedProperties": event.ExtendedProperties}
	var updated calendarEvent
	if err = t.calendarRequest(ctx, http.MethodPatch, event.ID, url.Values{"sendUpdates": {"all"}}, body, &updated); err != nil {
		return nil, err
	}
	return map[string]any{"exito": true, "fecha": date, "hora": clockValue, "meetLink": updated.HangoutLink, "mensaje": "Reunión reagendada correctamente."}, nil
}

func (t *AgentToolbox) cancelMeeting(ctx context.Context, phone string) (map[string]any, error) {
	events, err := t.customerMeetings(ctx, phone)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return map[string]any{"exito": false, "mensaje": "No se encontró una reunión agendada."}, nil
	}
	for _, event := range events {
		if err = t.calendarRequest(ctx, http.MethodDelete, event.ID, url.Values{"sendUpdates": {"all"}}, nil, nil); err != nil {
			return nil, err
		}
	}
	return map[string]any{"exito": true, "mensaje": "Reunión cancelada correctamente."}, nil
}

func (t *AgentToolbox) attendees(email string) []map[string]string {
	seen := map[string]bool{}
	var result []map[string]string
	for _, candidate := range append([]string{email}, t.meeting.DefaultGuestEmails...) {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && !seen[candidate] {
			result = append(result, map[string]string{"email": candidate})
			seen[candidate] = true
		}
	}
	return result
}

func (t *AgentToolbox) mergeAttendees(current []map[string]any, email string) []map[string]any {
	result := append([]map[string]any{}, current...)
	seen := map[string]bool{}
	for _, attendee := range result {
		if value, ok := attendee["email"].(string); ok {
			seen[strings.ToLower(value)] = true
		}
	}
	for _, attendee := range t.attendees(email) {
		if !seen[attendee["email"]] {
			result = append(result, map[string]any{"email": attendee["email"]})
		}
	}
	return result
}

func (t *AgentToolbox) endDateTime(date, clockValue string) string {
	location, _ := time.LoadLocation(t.meeting.Timezone)
	start, _ := time.ParseInLocation("2006-01-02T15:04", date+"T"+clockValue, location)
	return start.Add(time.Duration(t.meeting.SlotDurationMinutes) * time.Minute).Format("2006-01-02T15:04:05")
}

func meetingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for index := range raw {
		raw[index] = alphabet[int(raw[index])%len(alphabet)]
	}
	return string(raw), nil
}

func firstN(value string, length int) string {
	if len(value) < length {
		return value
	}
	return value[:length]
}

func clock(value string) string {
	if len(value) >= 16 {
		return value[11:16]
	}
	return ""
}

func isChileanHoliday(day time.Time) bool {
	fixed := map[string]bool{"01-01": true, "05-01": true, "05-21": true, "06-20": true, "07-16": true, "08-15": true, "09-18": true, "09-19": true, "10-12": true, "10-31": true, "11-01": true, "12-08": true, "12-25": true}
	if fixed[day.Format("01-02")] {
		return true
	}
	easter := easterSunday(day.Year())
	return sameDate(day, easter.AddDate(0, 0, -2)) || sameDate(day, easter.AddDate(0, 0, -1))
}

func sameDate(left, right time.Time) bool {
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}

func easterSunday(year int) time.Time {
	a, b, c := year%19, year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 12, 0, 0, 0, time.UTC)
}
