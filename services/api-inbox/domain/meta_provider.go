package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

type MetaTextProvider struct {
	client                               *http.Client
	endpoint, mediaEndpoint, accessToken string
}

const metaGraphBaseURL = "https://graph.facebook.com/v26.0"

func metaGraphResourceURL(resourceID string) string {
	return metaGraphBaseURL + "/" + resourceID
}

func NewMetaTextProvider(client *http.Client, phoneNumberID, accessToken string) (*MetaTextProvider, error) {
	if client == nil {
		client = &http.Client{}
	}
	if phoneNumberID == "" || accessToken == "" {
		return nil, errors.New("meta provider configuration is required")
	}
	base := metaGraphResourceURL(phoneNumberID)
	return &MetaTextProvider{client: client, endpoint: base + "/messages", mediaEndpoint: base + "/media", accessToken: accessToken}, nil
}

func (p *MetaTextProvider) SendMedia(ctx context.Context, phone, fileName, mimeType, caption string, content []byte) (string, error) {
	return p.sendMedia(ctx, phone, fileName, mimeType, caption, content, "")
}

func (p *MetaTextProvider) SendSticker(ctx context.Context, phone, fileName, mimeType string, content []byte) (string, error) {
	return p.sendMedia(ctx, phone, fileName, mimeType, "", content, "sticker")
}

func (p *MetaTextProvider) sendMedia(ctx context.Context, phone, fileName, mimeType, caption string, content []byte, forcedType string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("messaging_product", "whatsapp")
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, fileName))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(content); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.mediaEndpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+p.accessToken)
	req.Header.Set("content-type", writer.FormDataContentType())
	startedAt := time.Now()
	response, err := p.client.Do(req)
	if err != nil {
		recordMetaGraphCall("upload_media", "transport", startedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta media transport: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	limited, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		recordMetaGraphCall("upload_media", "response_read", startedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		recordMetaGraphCall("upload_media", metaGraphStatusOutcome(response.StatusCode), startedAt)
		return "", &ProviderError{Retryable: response.StatusCode == 429 || response.StatusCode >= 500, Cause: fmt.Errorf("meta media status %d", response.StatusCode)}
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(limited, &uploaded) != nil || uploaded.ID == "" {
		recordMetaGraphCall("upload_media", "invalid_response", startedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: errors.New("meta media response without id")}
	}
	recordMetaGraphCall("upload_media", "success", startedAt)
	mediaType := "document"
	if strings.HasPrefix(mimeType, "image/") {
		mediaType = "image"
	} else if strings.HasPrefix(mimeType, "audio/") {
		mediaType = "audio"
	} else if strings.HasPrefix(mimeType, "video/") {
		mediaType = "video"
	}
	if forcedType != "" {
		mediaType = forcedType
	}
	payload := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone, "type": mediaType}
	media := map[string]any{"id": uploaded.ID}
	if mediaType == "document" {
		media["filename"] = fileName
	}
	if caption != "" && mediaType != "audio" {
		media["caption"] = caption
	}
	if mediaType == "audio" && mimeType == "audio/ogg" {
		media["voice"] = true
	}
	payload[mediaType] = media
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	messageReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	messageReq.Header.Set("authorization", "Bearer "+p.accessToken)
	messageReq.Header.Set("content-type", "application/json")
	messageStartedAt := time.Now()
	messageResponse, err := p.client.Do(messageReq)
	if err != nil {
		recordMetaGraphCall("send_message", "transport", messageStartedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta message transport: %w", err)}
	}
	defer func() { _ = messageResponse.Body.Close() }()
	messageBody, readErr := io.ReadAll(io.LimitReader(messageResponse.Body, 64<<10))
	if readErr != nil {
		recordMetaGraphCall("send_message", "response_read", messageStartedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: readErr}
	}
	if messageResponse.StatusCode < 200 || messageResponse.StatusCode >= 300 {
		recordMetaGraphCall("send_message", metaGraphStatusOutcome(messageResponse.StatusCode), messageStartedAt)
		return "", &ProviderError{Retryable: messageResponse.StatusCode == 429 || messageResponse.StatusCode >= 500, Cause: fmt.Errorf("meta status %d", messageResponse.StatusCode)}
	}
	var sent struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if json.Unmarshal(messageBody, &sent) != nil || len(sent.Messages) == 0 || sent.Messages[0].ID == "" {
		recordMetaGraphCall("send_message", "invalid_response", messageStartedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: errors.New("meta response without message id")}
	}
	recordMetaGraphCall("send_message", "success", messageStartedAt)
	return sent.Messages[0].ID, nil
}

func (p *MetaTextProvider) SendText(ctx context.Context, phone, content string) (string, error) {
	return p.sendText(ctx, phone, content, "")
}

func (p *MetaTextProvider) SendTemplate(ctx context.Context, phone string, template SendTemplate) (string, error) {
	components := []map[string]any{}
	if len(template.Parameters) > 0 {
		parameters := make([]map[string]string, 0, len(template.Parameters))
		for _, value := range template.Parameters {
			parameters = append(parameters, map[string]string{"type": "text", "text": value})
		}
		components = append(components, map[string]any{"type": "body", "parameters": parameters})
	}
	templatePayload := map[string]any{"name": template.Name, "language": map[string]string{"code": template.Language}}
	if len(components) > 0 {
		templatePayload["components"] = components
	}
	payload := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone, "type": "template", "template": templatePayload}
	return p.sendMessagePayload(ctx, payload)
}

func (p *MetaTextProvider) SendReaction(ctx context.Context, phone, messageID, emoji string) error {
	_, err := p.sendMessagePayload(ctx, map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone, "type": "reaction", "reaction": map[string]string{"message_id": messageID, "emoji": emoji}})
	return err
}

func (p *MetaTextProvider) SendRichMessage(ctx context.Context, phone string, rich MessageRichContent) (string, error) {
	payload := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone}
	switch rich.Kind {
	case "location":
		if rich.Location == nil {
			return "", errors.New("location is required")
		}
		payload["type"] = "location"
		payload["location"] = rich.Location
	case "contacts":
		if len(rich.Contacts) == 0 {
			return "", errors.New("contacts are required")
		}
		contacts := make([]map[string]any, 0, len(rich.Contacts))
		for _, contact := range rich.Contacts {
			name, err := metaContactName(contact.Name)
			if err != nil {
				return "", err
			}
			item := map[string]any{
				"name":   name,
				"phones": []map[string]string{{"phone": contact.Phone, "type": "WORK"}},
			}
			if contact.Email != "" {
				item["emails"] = []map[string]string{{"email": contact.Email, "type": "WORK"}}
			}
			if contact.Company != "" {
				item["org"] = map[string]string{"company": contact.Company}
			}
			contacts = append(contacts, item)
		}
		payload["type"] = "contacts"
		payload["contacts"] = contacts
	case "interactive":
		if rich.Interactive == nil {
			return "", errors.New("interactive message is required")
		}
		interactive := map[string]any{"type": rich.Interactive.Kind, "body": map[string]string{"text": rich.Interactive.Body}}
		if rich.Interactive.Footer != "" {
			interactive["footer"] = map[string]string{"text": rich.Interactive.Footer}
		}
		if rich.Interactive.Kind == "button" {
			buttons := make([]map[string]any, 0, len(rich.Interactive.Options))
			for _, option := range rich.Interactive.Options {
				buttons = append(buttons, map[string]any{"type": "reply", "reply": map[string]string{"id": option.ID, "title": option.Title}})
			}
			interactive["action"] = map[string]any{"buttons": buttons}
		} else {
			rows := make([]map[string]string, 0, len(rich.Interactive.Options))
			for _, option := range rich.Interactive.Options {
				rows = append(rows, map[string]string{"id": option.ID, "title": option.Title, "description": option.Description})
			}
			interactive["action"] = map[string]any{"button": rich.Interactive.Button, "sections": []map[string]any{{"rows": rows}}}
		}
		payload["type"] = "interactive"
		payload["interactive"] = interactive
	default:
		return "", errors.New("unsupported rich message type")
	}
	return p.sendMessagePayload(ctx, payload)
}

func metaContactName(value string) (map[string]string, error) {
	formattedName := strings.TrimSpace(value)
	parts := strings.Fields(formattedName)
	if len(parts) == 0 {
		return nil, errors.New("contact name is required")
	}
	return map[string]string{
		"formatted_name": formattedName,
		"first_name":     parts[0],
	}, nil
}

func (p *MetaTextProvider) SendTextWithContext(ctx context.Context, phone, content, messageID string) (string, error) {
	return p.sendText(ctx, phone, content, messageID)
}

func (p *MetaTextProvider) sendText(ctx context.Context, phone, content, messageID string) (string, error) {
	payload := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone, "type": "text", "text": map[string]any{"body": content, "preview_url": true}}
	if messageID != "" {
		payload["context"] = map[string]string{"message_id": messageID}
	}
	return p.sendMessagePayload(ctx, payload)
}

func (p *MetaTextProvider) sendMessagePayload(ctx context.Context, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+p.accessToken)
	req.Header.Set("content-type", "application/json")
	startedAt := time.Now()
	response, err := p.client.Do(req)
	if err != nil {
		recordMetaGraphCall("send_message", "transport", startedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta transport: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	limited, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		recordMetaGraphCall("send_message", "response_read", startedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("read meta response: %w", readErr)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		recordMetaGraphCall("send_message", metaGraphStatusOutcome(response.StatusCode), startedAt)
		retryable := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return "", &ProviderError{Retryable: retryable, Cause: fmt.Errorf("meta status %d", response.StatusCode)}
	}
	var parsed struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err = json.Unmarshal(limited, &parsed); err != nil || len(parsed.Messages) == 0 || strings.TrimSpace(parsed.Messages[0].ID) == "" {
		recordMetaGraphCall("send_message", "invalid_response", startedAt)
		return "", &ProviderError{TransmissionPossible: true, Cause: errors.New("meta response without message id")}
	}
	recordMetaGraphCall("send_message", "success", startedAt)
	return parsed.Messages[0].ID, nil
}

// MarkReadAndTyping confirms that an inbound WhatsApp message was processed and
// displays the text typing indicator while the agent response is being sent.
func (p *MetaTextProvider) MarkReadAndTyping(ctx context.Context, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return errors.New("whatsapp message id is required")
	}
	body, err := json.Marshal(map[string]any{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageID,
		"typing_indicator":  map[string]string{"type": "text"},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+p.accessToken)
	req.Header.Set("content-type", "application/json")
	startedAt := time.Now()
	response, err := p.client.Do(req)
	if err != nil {
		recordMetaGraphCall("mark_read", "transport", startedAt)
		return &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta read receipt transport: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		recordMetaGraphCall("mark_read", "response_read", startedAt)
		return &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("read meta receipt response: %w", readErr)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		recordMetaGraphCall("mark_read", metaGraphStatusOutcome(response.StatusCode), startedAt)
		retryable := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return &ProviderError{Retryable: retryable, Cause: fmt.Errorf("meta read receipt status %d", response.StatusCode)}
	}
	recordMetaGraphCall("mark_read", "success", startedAt)
	return nil
}
