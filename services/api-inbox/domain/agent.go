package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
type agentDynamo interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
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

type AgentService struct {
	db         agentDynamo
	s3         agentS3
	transcribe agentTranscribe
	sender     AgentSender
	table      string
	cfg        AgentConfig
	gemini     GeminiConfig
	http       *http.Client
	sleep      func(time.Duration)
	now        func() time.Time
	tools      *AgentToolbox
}

func NewAgentService(db agentDynamo, storage agentS3, transcriber agentTranscribe, sender AgentSender, tools *AgentToolbox, table string, cfg AgentConfig, gemini GeminiConfig) (*AgentService, error) {
	if db == nil || storage == nil || transcriber == nil || sender == nil || tools == nil || table == "" || gemini.APIKey == "" || gemini.Model == "" {
		return nil, errors.New("agent configuration is incomplete")
	}
	if cfg.MaxHistoryTurns < 1 || cfg.MaxHistoryTurns > 50 || cfg.RateLimitMax < 1 || cfg.RateLimitWindowSeconds < 1 || cfg.DebounceMS < 0 {
		return nil, errors.New("agent limits are invalid")
	}
	return &AgentService{db: db, s3: storage, transcribe: transcriber, sender: sender, tools: tools, table: table, cfg: cfg, gemini: gemini, http: &http.Client{Timeout: 25 * time.Second}, sleep: time.Sleep, now: time.Now}, nil
}

func (s *AgentService) Process(ctx context.Context, phone, messageID string) error {
	if !s.cfg.Enabled || !contains(s.cfg.EnabledChannels, "whatsapp") {
		return nil
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
	if len(latest.Attachments) > 0 && strings.HasPrefix(latest.Attachments[0].MIMEType, "audio/") {
		transcript, terr := s.transcribeAudio(ctx, latest.Attachments[0], messageID)
		if terr != nil {
			return terr
		}
		latest.Content = &transcript
		messages[len(messages)-1] = latest
	}
	reply, silent, err := s.generate(ctx, phone, messageID, messages)
	if err != nil {
		return err
	}
	if silent {
		return nil
	}
	reply = normalizeAgentReply(reply)
	if reply == "" {
		return nil
	}
	runes := []rune(reply)
	if len(runes) > s.cfg.MaxMessageLength {
		reply = string(runes[:s.cfg.MaxMessageLength])
	}
	_, err = s.sender.SendAgentText(ctx, phone, "agent-"+messageID, reply)
	return err
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

type geminiPart struct {
	Text         string `json:"text,omitempty"`
	FunctionCall *struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"functionCall,omitempty"`
	FunctionResponse map[string]any `json:"functionResponse,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

func (s *AgentService) generate(ctx context.Context, phone, messageID string, messages []Message) (string, bool, error) {
	contents := make([]map[string]any, 0, len(messages))
	state := &agentExecutionState{}
	for _, m := range messages {
		for _, attachment := range m.Attachments {
			if strings.EqualFold(attachment.FileName, path.Base(s.tools.assets.SernaturCertificateKey)) {
				state.certificateSent = true
			}
		}
		if m.Content == nil || strings.TrimSpace(*m.Content) == "" {
			continue
		}
		role := "user"
		if m.Type != MessageTypeCustomer {
			role = "model"
		}
		contents = append(contents, map[string]any{"role": role, "parts": []map[string]string{{"text": *m.Content}}})
	}
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + s.gemini.Model + ":generateContent?key=" + s.gemini.APIKey
	for round := 0; round < 8; round++ {
		payload := map[string]any{"contents": contents, "systemInstruction": map[string]any{"parts": []map[string]string{{"text": agentSystemPrompt(phone, len(messages) > 1, s.now())}}}, "generationConfig": map[string]any{"temperature": s.gemini.Temperature, "topP": s.gemini.TopP}, "tools": []map[string]any{{"functionDeclarations": agentFunctionDeclarations()}}}
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", false, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if err != nil {
			return "", false, err
		}
		req.Header.Set("content-type", "application/json")
		resp, err := s.http.Do(req)
		if err != nil {
			return "", false, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return "", false, readErr
		}
		if resp.StatusCode/100 != 2 {
			return "", false, fmt.Errorf("gemini status %d", resp.StatusCode)
		}
		var result struct {
			Candidates []struct {
				Content geminiContent `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal(body, &result) != nil || len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
			return "", false, errors.New("invalid gemini response")
		}
		candidate := result.Candidates[0].Content
		var reply strings.Builder
		var responses []geminiPart
		for index, part := range candidate.Parts {
			reply.WriteString(part.Text)
			if part.FunctionCall == nil {
				continue
			}
			toolResult, silent, toolErr := s.tools.execute(ctx, phone, fmt.Sprintf("%s-%d-%d", messageID, round, index), part.FunctionCall.Name, part.FunctionCall.Args, state)
			if toolErr != nil {
				toolResult = map[string]any{"exito": false, "error": "La herramienta no pudo completar la operación."}
			}
			if silent {
				return "", true, nil
			}
			responses = append(responses, geminiPart{FunctionResponse: map[string]any{"name": part.FunctionCall.Name, "response": toolResult}})
		}
		if len(responses) == 0 {
			return reply.String(), false, nil
		}
		contents = append(contents, map[string]any{"role": "model", "parts": candidate.Parts}, map[string]any{"role": "function", "parts": responses})
	}
	return "", false, errors.New("gemini tool loop exceeded")
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
