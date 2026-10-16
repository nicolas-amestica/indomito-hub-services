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
)

type MetaTextProvider struct {
	client                               *http.Client
	endpoint, mediaEndpoint, accessToken string
}

func NewMetaTextProvider(client *http.Client, phoneNumberID, accessToken string) (*MetaTextProvider, error) {
	if client == nil {
		client = &http.Client{}
	}
	if phoneNumberID == "" || accessToken == "" {
		return nil, errors.New("meta provider configuration is required")
	}
	base := "https://graph.facebook.com/v21.0/" + phoneNumberID
	return &MetaTextProvider{client: client, endpoint: base + "/messages", mediaEndpoint: base + "/media", accessToken: accessToken}, nil
}

func (p *MetaTextProvider) SendMedia(ctx context.Context, phone, fileName, mimeType, caption string, content []byte) (string, error) {
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
	response, err := p.client.Do(req)
	if err != nil {
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta media transport: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	limited, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return "", &ProviderError{TransmissionPossible: true, Cause: readErr}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &ProviderError{Retryable: response.StatusCode == 429 || response.StatusCode >= 500, Cause: fmt.Errorf("meta media status %d", response.StatusCode)}
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(limited, &uploaded) != nil || uploaded.ID == "" {
		return "", &ProviderError{TransmissionPossible: true, Cause: errors.New("meta media response without id")}
	}
	mediaType := "document"
	if strings.HasPrefix(mimeType, "image/") {
		mediaType = "image"
	} else if strings.HasPrefix(mimeType, "audio/") {
		mediaType = "audio"
	}
	payload := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone, "type": mediaType}
	media := map[string]string{"id": uploaded.ID}
	if mediaType == "document" {
		media["filename"] = fileName
	}
	if caption != "" && mediaType != "audio" {
		media["caption"] = caption
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
	messageResponse, err := p.client.Do(messageReq)
	if err != nil {
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta message transport: %w", err)}
	}
	defer func() { _ = messageResponse.Body.Close() }()
	messageBody, readErr := io.ReadAll(io.LimitReader(messageResponse.Body, 64<<10))
	if readErr != nil {
		return "", &ProviderError{TransmissionPossible: true, Cause: readErr}
	}
	if messageResponse.StatusCode < 200 || messageResponse.StatusCode >= 300 {
		return "", &ProviderError{Retryable: messageResponse.StatusCode == 429 || messageResponse.StatusCode >= 500, Cause: fmt.Errorf("meta status %d", messageResponse.StatusCode)}
	}
	var sent struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if json.Unmarshal(messageBody, &sent) != nil || len(sent.Messages) == 0 || sent.Messages[0].ID == "" {
		return "", &ProviderError{TransmissionPossible: true, Cause: errors.New("meta response without message id")}
	}
	return sent.Messages[0].ID, nil
}

func (p *MetaTextProvider) SendText(ctx context.Context, phone, content string) (string, error) {
	body, err := json.Marshal(map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": phone, "type": "text", "text": map[string]string{"body": content}})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("authorization", "Bearer "+p.accessToken)
	req.Header.Set("content-type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("meta transport: %w", err)}
	}
	defer func() { _ = response.Body.Close() }()
	limited, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return "", &ProviderError{TransmissionPossible: true, Cause: fmt.Errorf("read meta response: %w", readErr)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryable := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return "", &ProviderError{Retryable: retryable, Cause: fmt.Errorf("meta status %d", response.StatusCode)}
	}
	var parsed struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err = json.Unmarshal(limited, &parsed); err != nil || len(parsed.Messages) == 0 || strings.TrimSpace(parsed.Messages[0].ID) == "" {
		return "", &ProviderError{TransmissionPossible: true, Cause: errors.New("meta response without message id")}
	}
	return parsed.Messages[0].ID, nil
}
