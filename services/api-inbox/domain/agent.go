package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	transcribetypes "github.com/aws/aws-sdk-go-v2/service/transcribe/types"
)

type AgentConfig struct {
	Enabled                bool     `json:"enabled"`
	DebounceMS             int      `json:"debounceMs"`
	RateLimitMax           int      `json:"rateLimitMax"`
	RateLimitWindowSeconds int      `json:"rateLimitWindowSeconds"`
	MaxMessageLength       int      `json:"maxMessageLength"`
	MaxHistoryTurns        int      `json:"maxHistoryTurns"`
	EnabledChannels        []string `json:"enabledChannels"`
}
type GeminiConfig struct {
	APIKey      string  `json:"apiKey"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"topP"`
}

type permanentAgentError struct{ cause error }

func (e *permanentAgentError) Error() string { return e.cause.Error() }
func (e *permanentAgentError) Unwrap() error { return e.cause }

type geminiProviderError struct {
	status  int
	code    string
	message string
}

func (e *geminiProviderError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("gemini status %d", e.status)
	}
	return fmt.Sprintf("gemini status %d (%s): %s", e.status, e.code, e.message)
}

type agentDynamo interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}
type agentS3 interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}
type agentTranscribe interface {
	StartTranscriptionJob(context.Context, *transcribe.StartTranscriptionJobInput, ...func(*transcribe.Options)) (*transcribe.StartTranscriptionJobOutput, error)
	GetTranscriptionJob(context.Context, *transcribe.GetTranscriptionJobInput, ...func(*transcribe.Options)) (*transcribe.GetTranscriptionJobOutput, error)
}
type AgentSender interface {
	SendAgentText(context.Context, string, string, string) (SendState, error)
	SendAgentMedia(context.Context, string, string, string, string, string, []byte) (SendState, error)
}

type agentPresenceSender interface {
	MarkAgentReadAndTyping(context.Context, string) error
}

type AgentQuotaNotifier interface {
	NotifyAgentQuota(context.Context, string, string) error
}

type AgentService struct {
	db            agentDynamo
	s3            agentS3
	transcribe    agentTranscribe
	sender        AgentSender
	table         string
	cfg           AgentConfig
	gemini        GeminiConfig
	http          *http.Client
	sleep         func(time.Duration)
	now           func() time.Time
	tools         *AgentToolbox
	mutations     *ConversationMutationService
	notifier      DerivationNotifier
	quotaNotifier AgentQuotaNotifier
	managed       *AgentManagedConfig
}

func (s *AgentService) SetManagedConfig(config *AgentManagedConfig) { s.managed = config }

// SetDerivationNotifier enables the optional internal WhatsApp alert.
func (s *AgentService) SetDerivationNotifier(notifier DerivationNotifier) {
	s.notifier = notifier
}

func (s *AgentService) SetAgentQuotaNotifier(notifier AgentQuotaNotifier) {
	s.quotaNotifier = notifier
}

func NewAgentService(db agentDynamo, storage agentS3, transcriber agentTranscribe, sender AgentSender, tools *AgentToolbox, table string, cfg AgentConfig, gemini GeminiConfig) (*AgentService, error) {
	if db == nil || storage == nil || transcriber == nil || sender == nil || tools == nil || table == "" || gemini.APIKey == "" || gemini.Model == "" {
		return nil, errors.New("agent configuration is incomplete")
	}
	if cfg.MaxHistoryTurns < 1 || cfg.MaxHistoryTurns > 50 || cfg.RateLimitMax < 1 || cfg.RateLimitWindowSeconds < 1 || cfg.DebounceMS < 0 {
		return nil, errors.New("agent limits are invalid")
	}
	mutations, err := NewConversationMutationService(db, table, nil)
	if err != nil {
		return nil, err
	}
	return &AgentService{db: db, s3: storage, transcribe: transcriber, sender: sender, tools: tools, mutations: mutations, table: table, cfg: cfg, gemini: gemini, http: &http.Client{Timeout: 25 * time.Second}, sleep: time.Sleep, now: time.Now}, nil
}

func (s *AgentService) Process(ctx context.Context, phone, messageID string) error {
	if !s.cfg.Enabled || !contains(s.cfg.EnabledChannels, "whatsapp") {
		return nil
	}
	summary, err := s.conversationSummary(ctx, phone)
	if err != nil {
		return err
	}
	recoveringAgentError := summary.Status == ConversationStatusDerived && summary.Reason != nil && *summary.Reason == "agent_error"
	if summary.Status == ConversationStatusDerived && !recoveringAgentError {
		return s.confirmExistingDerivation(ctx, phone, messageID)
	}
	if s.cfg.DebounceMS > 0 {
		s.sleep(time.Duration(s.cfg.DebounceMS) * time.Millisecond)
	}
	messages, err := s.history(ctx, phone)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	latest := messages[len(messages)-1]
	if latest.Type != MessageTypeCustomer || latest.WhatsAppMessageID == nil || *latest.WhatsAppMessageID != messageID {
		return nil
	}
	window := s.now().UTC().Add(-time.Duration(s.cfg.RateLimitWindowSeconds) * time.Second)
	count := 0
	for _, m := range messages {
		if m.Type == MessageTypeCustomer && m.CreatedAt.After(window) {
			count++
		}
	}
	if count > s.cfg.RateLimitMax {
		return nil
	}
	if err = s.mutations.SetAgentProcessing(ctx, phone, messageID, true); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		// El lease hace que este indicador sea autocorregible. Una falla al
		// limpiarlo no debe reejecutar herramientas ni duplicar efectos externos.
		_ = s.mutations.SetAgentProcessing(cleanupCtx, phone, messageID, false)
	}()
	if len(latest.Attachments) > 0 && strings.HasPrefix(latest.Attachments[0].MIMEType, "audio/") {
		transcript, terr := s.transcribeAudio(ctx, latest.Attachments[0], messageID)
		if terr != nil {
			return terr
		}
		latest.Content = &transcript
		messages[len(messages)-1] = latest
	}
	reply, derivation, err := s.generate(ctx, phone, messageID, messages)
	if err != nil {
		if isGeminiQuotaExhausted(err) {
			if s.quotaNotifier != nil {
				if notifyErr := s.quotaNotifier.NotifyAgentQuota(ctx, s.gemini.Model, err.Error()); notifyErr != nil {
					log.Printf("agent quota notification failed messageId=%s: %v", messageID, notifyErr)
				}
			}
			err = &permanentAgentError{cause: err}
		}
		var permanent *permanentAgentError
		if !errors.As(err, &permanent) {
			return err
		}
		_, sendErr := s.sender.SendAgentText(ctx, phone, "agent-"+messageID+"-error", "Tuve un inconveniente al procesar tu solicitud. El equipo de Giras Indómito continuará la atención.")
		if sendErr != nil {
			return errors.Join(err, sendErr)
		}
		label := "Error del agente; requiere revision"
		_, deriveErr := s.mutations.Derive(ctx, phone, "agent_error", label)
		s.notifyDerivation(ctx, phone, messageID, latest, label)
		return errors.Join(err, deriveErr)
	}
	if derivation != nil {
		_, err = s.mutations.Derive(ctx, phone, derivation.Reason, derivation.Label)
		if err == nil {
			s.notifyDerivation(ctx, phone, messageID, latest, derivation.Label)
			confirmation := "Te derivé con nuestro equipo para que continúen ayudándote. Te contactarán apenas sea posible."
			if s.managed != nil && strings.TrimSpace(s.managed.Policies.HandoffConfirmation) != "" {
				confirmation = s.managed.Policies.HandoffConfirmation
			}
			_, err = s.sender.SendAgentText(ctx, phone, "agent-"+messageID+"-derivation", confirmation)
		}
		return err
	}
	reply = normalizeAgentReply(reply)
	if reply == "" {
		return nil
	}
	runes := []rune(reply)
	if len(runes) > s.cfg.MaxMessageLength {
		reply = string(runes[:s.cfg.MaxMessageLength])
	}
	if presence, ok := s.sender.(agentPresenceSender); ok {
		// The response remains the critical path: a read-receipt failure must not
		// suppress an otherwise valid answer or create an SQS retry duplicate.
		_ = presence.MarkAgentReadAndTyping(ctx, messageID)
	}
	_, err = s.sender.SendAgentText(ctx, phone, "agent-"+messageID, reply)
	if err != nil {
		return err
	}
	if recoveringAgentError {
		_, err = s.mutations.ActivateBot(ctx, phone)
	}
	return err
}

func isGeminiQuotaExhausted(err error) bool {
	var provider *geminiProviderError
	return errors.As(err, &provider) && provider.status == http.StatusTooManyRequests && strings.EqualFold(provider.code, "RESOURCE_EXHAUSTED")
}

func (s *AgentService) confirmExistingDerivation(ctx context.Context, phone, messageID string) error {
	messages, err := s.history(ctx, phone)
	if err != nil || len(messages) == 0 {
		return err
	}
	latest := messages[len(messages)-1]
	if latest.Type != MessageTypeCustomer || latest.WhatsAppMessageID == nil || *latest.WhatsAppMessageID != messageID || latest.Content == nil || !isExplicitHumanHandoff(*latest.Content) {
		return nil
	}
	_, err = s.sender.SendAgentText(ctx, phone, "agent-"+messageID+"-derivation-confirmation", "Tu conversación ya está derivada con nuestro equipo. Te contactarán apenas sea posible.")
	return err
}

func isExplicitHumanHandoff(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	for _, phrase := range []string{"hablar con una persona", "hablar con un agente", "agente real", "atencion humana", "atención humana", "hablar con un humano", "hablar con un ejecutivo"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func (s *AgentService) notifyDerivation(ctx context.Context, phone, messageID string, latest Message, label string) {
	if s.notifier == nil {
		return
	}
	detail := ""
	if latest.Content != nil {
		detail = *latest.Content
	}
	if detail == "" && len(latest.Attachments) > 0 {
		detail = "Adjunto: " + latest.Attachments[0].FileName
	}
	if err := s.notifier.NotifyDerivation(ctx, DerivationNotification{SourcePhone: phone, MessageID: messageID, Detail: detail, ReasonLabel: label}); err != nil {
		log.Printf("derivation notification failed messageId=%s: %v", messageID, err)
	}
}

func (s *AgentService) conversationSummary(ctx context.Context, phone string) (ConversationSummary, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("CONV#"+phone, "META"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return ConversationSummary{}, fmt.Errorf("read conversation agent status: %w", err)
	}
	if len(out.Item) == 0 {
		return ConversationSummary{Phone: phone, Status: ConversationStatusBot}, nil
	}
	var summary ConversationSummary
	if err = attributevalue.UnmarshalMap(out.Item, &summary); err != nil {
		return ConversationSummary{}, fmt.Errorf("decode conversation agent status: %w", err)
	}
	return summary, nil
}

func (s *AgentService) history(ctx context.Context, phone string) ([]Message, error) {
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{TableName: &s.table, KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :message)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CONV#" + phone}, ":message": &types.AttributeValueMemberS{Value: "MSG#"}}, ScanIndexForward: aws.Bool(false), Limit: aws.Int32(int32(s.cfg.MaxHistoryTurns))})
	if err != nil {
		return nil, err
	}
	var result []Message
	if err = attributevalue.UnmarshalListOfMaps(out.Items, &result); err != nil {
		return nil, err
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func (s *AgentService) transcribeAudio(ctx context.Context, a MessageAttachment, messageID string) (string, error) {
	if a.Bucket == "" || a.Key == "" {
		return "", errors.New("audio attachment storage reference missing")
	}
	format := transcribetypes.MediaFormatOgg
	switch strings.Split(a.MIMEType, ";")[0] {
	case "audio/mpeg":
		format = transcribetypes.MediaFormatMp3
	case "audio/mp4":
		format = transcribetypes.MediaFormatMp4
	case "audio/wav":
		format = transcribetypes.MediaFormatWav
	case "audio/webm":
		format = transcribetypes.MediaFormatWebm
	case "audio/flac":
		format = transcribetypes.MediaFormatFlac
	case "audio/amr":
		format = transcribetypes.MediaFormatAmr
	}
	name := "inbox-" + strings.NewReplacer(".", "-", ":", "-", "/", "-").Replace(messageID)
	_, err := s.transcribe.StartTranscriptionJob(ctx, &transcribe.StartTranscriptionJobInput{TranscriptionJobName: &name, Media: &transcribetypes.Media{MediaFileUri: aws.String("s3://" + a.Bucket + "/" + a.Key)}, MediaFormat: format, LanguageCode: transcribetypes.LanguageCodeEsUs})
	if err != nil && !strings.Contains(err.Error(), "ConflictException") {
		return "", err
	}
	for range 20 {
		s.sleep(3 * time.Second)
		out, gerr := s.transcribe.GetTranscriptionJob(ctx, &transcribe.GetTranscriptionJobInput{TranscriptionJobName: &name})
		if gerr != nil {
			return "", gerr
		}
		job := out.TranscriptionJob
		if job.TranscriptionJobStatus == transcribetypes.TranscriptionJobStatusFailed {
			return "", errors.New("audio transcription failed")
		}
		if job.TranscriptionJobStatus == transcribetypes.TranscriptionJobStatusCompleted && job.Transcript != nil && job.Transcript.TranscriptFileUri != nil {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, *job.Transcript.TranscriptFileUri, nil)
			resp, herr := s.http.Do(req)
			if herr != nil {
				return "", herr
			}
			defer func() { _ = resp.Body.Close() }()
			var payload struct {
				Results struct {
					Transcripts []struct {
						Transcript string `json:"transcript"`
					} `json:"transcripts"`
				} `json:"results"`
			}
			if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload) != nil || len(payload.Results.Transcripts) == 0 {
				return "", errors.New("invalid transcription result")
			}
			return payload.Results.Transcripts[0].Transcript, nil
		}
	}
	return "", errors.New("audio transcription timed out")
}

type interactionStep struct {
	Type      string         `json:"type"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
}

type interactionResponse struct {
	Status string            `json:"status"`
	Steps  []json.RawMessage `json:"steps"`
	Usage  AgentTokenUsage   `json:"usage"`
	Error  *struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (s *AgentService) generate(ctx context.Context, phone, messageID string, messages []Message) (string, *AgentDerivation, error) {
	state := &agentExecutionState{}
	if len(messages) > 0 {
		latest := messages[len(messages)-1]
		if latest.Type == MessageTypeCustomer && latest.Content != nil {
			state.latestCustomerMessage = strings.TrimSpace(*latest.Content)
		}
	}
	if s.db != nil {
		collected, err := s.agentCollectedData(ctx, phone)
		if err != nil {
			return "", nil, err
		}
		state.collected = collected
	}
	var history strings.Builder
	history.WriteString("Historial de WhatsApp en orden cronológico:\n")
	for _, m := range messages {
		for _, attachment := range m.Attachments {
			if strings.EqualFold(attachment.FileName, path.Base(s.tools.assets.SernaturCertificateKey)) {
				state.certificateSent = true
			}
		}
		if m.Content == nil || strings.TrimSpace(*m.Content) == "" {
			continue
		}
		role := "Cliente"
		if m.Type != MessageTypeCustomer {
			role = "Agente IA"
		}
		fmt.Fprintf(&history, "%s: %s\n", role, strings.TrimSpace(*m.Content))
	}
	initialStep, err := json.Marshal(map[string]any{"type": "user_input", "content": []map[string]string{{"type": "text", "text": history.String()}}})
	if err != nil {
		return "", nil, err
	}
	steps := []json.RawMessage{initialStep}
	endpoint := "https://generativelanguage.googleapis.com/v1beta/interactions"
	for round := 0; round < 8; round++ {
		payload := map[string]any{"model": s.gemini.Model, "input": steps, "system_instruction": agentSystemPromptWithConfig(phone, len(messages) > 1, s.now(), state.collected, s.managed), "generation_config": map[string]any{"temperature": s.gemini.Temperature, "top_p": s.gemini.TopP}, "tools": agentInteractionTools(), "store": false}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", nil, err
		}
		resp, err := s.doGeminiRequest(ctx, endpoint, raw)
		if err != nil {
			return "", nil, &permanentAgentError{cause: fmt.Errorf("gemini unavailable after immediate retries: %w", err)}
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return "", nil, readErr
		}
		if resp.StatusCode/100 != 2 {
			statusErr := geminiStatusError(resp.StatusCode, body)
			if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
				return "", nil, &permanentAgentError{cause: statusErr}
			}
			return "", nil, statusErr
		}
		var result interactionResponse
		if json.Unmarshal(body, &result) != nil || len(result.Steps) == 0 {
			return "", nil, &permanentAgentError{cause: errors.New("invalid gemini response")}
		}
		if err = s.recordTokenUsage(ctx, result.Usage); err != nil {
			// La telemetria nunca debe impedir una respuesta al cliente.
			log.Printf("agent token usage persistence failed messageId=%s: %v", messageID, err)
		}
		var reply strings.Builder
		var responses []json.RawMessage
		for index, rawStep := range result.Steps {
			var step interactionStep
			if err = json.Unmarshal(rawStep, &step); err != nil {
				return "", nil, &permanentAgentError{cause: fmt.Errorf("decode gemini step: %w", err)}
			}
			if step.Type == "model_output" {
				for _, content := range step.Content {
					if content.Type == "text" {
						reply.WriteString(content.Text)
					}
				}
			}
			if step.Type != "function_call" || step.Name == "" || step.ID == "" {
				continue
			}
			toolResult, silent, toolErr := s.tools.execute(ctx, phone, fmt.Sprintf("%s-%d-%d", messageID, round, index), step.Name, step.Arguments, state)
			if state.collectedDirty && s.db != nil {
				if persistErr := s.saveAgentCollectedData(ctx, phone, state.collected); persistErr != nil {
					return "", nil, persistErr
				} else {
					state.collectedDirty = false
				}
			}
			if toolErr != nil {
				toolResult = map[string]any{"exito": false, "error": "La herramienta no pudo completar la operación."}
			}
			if silent {
				return "", state.derivation, nil
			}
			response, marshalErr := json.Marshal(map[string]any{"type": "function_result", "name": step.Name, "call_id": step.ID, "result": toolResult, "is_error": toolErr != nil})
			if marshalErr != nil {
				return "", nil, marshalErr
			}
			responses = append(responses, response)
		}
		if len(responses) == 0 {
			if strings.TrimSpace(reply.String()) == "" {
				return "", nil, &permanentAgentError{cause: fmt.Errorf("gemini completed without output: %s", result.Status)}
			}
			return reply.String(), nil, nil
		}
		steps = append(steps, result.Steps...)
		steps = append(steps, responses...)
	}
	return "", nil, &permanentAgentError{cause: errors.New("gemini tool loop exceeded")}
}

func (s *AgentService) doGeminiRequest(ctx context.Context, endpoint string, payload []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("x-goog-api-key", s.gemini.APIKey)
		resp, requestErr := s.http.Do(req)
		if requestErr == nil && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < http.StatusInternalServerError {
			return resp, nil
		}
		if requestErr == nil {
			if attempt == 2 {
				return resp, nil
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("transient gemini status %d", resp.StatusCode)
		} else {
			lastErr = requestErr
		}
		if attempt < 2 {
			delay := time.Duration(attempt+1) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, lastErr
}

type agentStateRecord struct {
	PK            string             `dynamodbav:"pk"`
	SK            string             `dynamodbav:"sk"`
	CollectedData AgentCollectedData `dynamodbav:"collectedData"`
	UpdatedAt     time.Time          `dynamodbav:"updatedAt"`
	ExpiresAt     int64              `dynamodbav:"expiresAt"`
}

func (s *AgentService) agentCollectedData(ctx context.Context, phone string) (AgentCollectedData, error) {
	output, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: snapshotKey("CONV#"+phone, "AGENT_STATE"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return AgentCollectedData{}, fmt.Errorf("read agent collected data: %w", err)
	}
	if len(output.Item) == 0 {
		return AgentCollectedData{}, nil
	}
	var record agentStateRecord
	if err = attributevalue.UnmarshalMap(output.Item, &record); err != nil {
		return AgentCollectedData{}, fmt.Errorf("decode agent collected data: %w", err)
	}
	return record.CollectedData, nil
}

func (s *AgentService) saveAgentCollectedData(ctx context.Context, phone string, collected AgentCollectedData) error {
	now := s.now().UTC()
	record, err := attributevalue.MarshalMap(agentStateRecord{PK: "CONV#" + phone, SK: "AGENT_STATE", CollectedData: collected, UpdatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour).Unix()})
	if err != nil {
		return fmt.Errorf("encode agent collected data: %w", err)
	}
	if _, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: record}); err != nil {
		return fmt.Errorf("persist agent collected data: %w", err)
	}
	return nil
}

func geminiStatusError(status int, body []byte) error {
	var result interactionResponse
	if json.Unmarshal(body, &result) == nil && result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
		message := []rune(strings.TrimSpace(result.Error.Message))
		if len(message) > 500 {
			message = message[:500]
		}
		return &geminiProviderError{status: status, code: result.Error.Status, message: string(message)}
	}
	return &geminiProviderError{status: status}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
