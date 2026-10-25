package domain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMetaGraphResourceURLUsesCurrentValidatedVersion(t *testing.T) {
	t.Parallel()

	got := metaGraphResourceURL("resource-id")
	want := "https://graph.facebook.com/v26.0/resource-id"
	if got != want {
		t.Fatalf("metaGraphResourceURL() = %q, want %q", got, want)
	}
}

func TestNewMetaTextProviderUsesSharedGraphVersion(t *testing.T) {
	t.Parallel()

	provider, err := NewMetaTextProvider(nil, "phone-id", "access-token")
	if err != nil {
		t.Fatalf("NewMetaTextProvider() error = %v", err)
	}
	if provider.endpoint != metaGraphResourceURL("phone-id")+"/messages" {
		t.Fatalf("unexpected message endpoint %q", provider.endpoint)
	}
	if provider.mediaEndpoint != metaGraphResourceURL("phone-id")+"/media" {
		t.Fatalf("unexpected media endpoint %q", provider.mediaEndpoint)
	}
}

func TestMetaProviderMarksInboundMessageReadWithTypingIndicator(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != metaGraphResourceURL("phone-id")+"/messages" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		if request.Header.Get("authorization") != "Bearer access-token" {
			t.Fatal("unexpected authorization header")
		}
		var payload struct {
			MessagingProduct string            `json:"messaging_product"`
			Status           string            `json:"status"`
			MessageID        string            `json:"message_id"`
			TypingIndicator  map[string]string `json:"typing_indicator"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.MessagingProduct != "whatsapp" || payload.Status != "read" || payload.MessageID != "wamid.1" || payload.TypingIndicator["type"] != "text" {
			t.Fatalf("unexpected payload %#v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"success":true}`)), Header: make(http.Header)}, nil
	})}
	provider, err := NewMetaTextProvider(client, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.MarkReadAndTyping(context.Background(), "wamid.1"); err != nil {
		t.Fatal(err)
	}
}

func TestMetaProviderRejectsEmptyReadReceiptMessageID(t *testing.T) {
	t.Parallel()
	provider, err := NewMetaTextProvider(nil, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.MarkReadAndTyping(context.Background(), "  "); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestMetaProviderSendsContextualTextReply(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Type string `json:"type"`
			Text struct {
				Body       string `json:"body"`
				PreviewURL bool   `json:"preview_url"`
			} `json:"text"`
			Context map[string]string `json:"context"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type != "text" || payload.Text.Body != "Respuesta" || !payload.Text.PreviewURL || payload.Context["message_id"] != "wamid.original" {
			t.Fatalf("unexpected contextual payload %#v", payload)
		}
		return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.reply"}]}`), nil
	})}
	provider, err := NewMetaTextProvider(client, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	wamid, err := provider.SendTextWithContext(context.Background(), "56912345678", "Respuesta", "wamid.original")
	if err != nil || wamid != "wamid.reply" {
		t.Fatalf("unexpected result wamid=%q err=%v", wamid, err)
	}
}

func TestMetaProviderSendsMP4AsVideo(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if request.URL.String() != metaGraphResourceURL("phone-id")+"/media" {
				t.Fatalf("unexpected upload URL %s", request.URL)
			}
			if !strings.HasPrefix(request.Header.Get("content-type"), "multipart/form-data;") {
				t.Fatalf("unexpected upload content type %q", request.Header.Get("content-type"))
			}
			return jsonHTTPResponse(http.StatusOK, `{"id":"media-1"}`), nil
		case 2:
			var payload struct {
				Type  string            `json:"type"`
				Video map[string]string `json:"video"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Type != "video" || payload.Video["id"] != "media-1" || payload.Video["caption"] != "Recorrido" {
				t.Fatalf("unexpected video payload %#v", payload)
			}
			return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.video"}]}`), nil
		default:
			t.Fatalf("unexpected request %d", calls)
			return nil, nil
		}
	})}
	provider, err := NewMetaTextProvider(client, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	wamid, err := provider.SendMedia(context.Background(), "56912345678", "recorrido.mp4", "video/mp4", "Recorrido", []byte("video"))
	if err != nil {
		t.Fatal(err)
	}
	if wamid != "wamid.video" || calls != 2 {
		t.Fatalf("unexpected result wamid=%q calls=%d", wamid, calls)
	}
}

func TestMetaProviderSendsOGGAsVoiceMessage(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTPResponse(http.StatusOK, `{"id":"media-voice"}`), nil
		}
		var payload struct {
			Type  string `json:"type"`
			Audio struct {
				ID    string `json:"id"`
				Voice bool   `json:"voice"`
			} `json:"audio"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type != "audio" || payload.Audio.ID != "media-voice" || !payload.Audio.Voice {
			t.Fatalf("unexpected voice payload %#v", payload)
		}
		return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.voice"}]}`), nil
	})}
	provider, err := NewMetaTextProvider(client, "phone-id", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	wamid, err := provider.SendMedia(context.Background(), "56912345678", "audio.ogg", "audio/ogg", "", []byte("opus"))
	if err != nil || wamid != "wamid.voice" || calls != 2 {
		t.Fatalf("unexpected result wamid=%q calls=%d err=%v", wamid, calls, err)
	}
}

func TestMetaProviderSendsTemplateAndReactionPayloads(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			if payload["type"] != "template" {
				t.Fatalf("unexpected template payload %#v", payload)
			}
			template := payload["template"].(map[string]any)
			if template["name"] != "recordatorio_viaje" {
				t.Fatalf("unexpected template %#v", template)
			}
			return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.template"}]}`), nil
		}
		if payload["type"] != "reaction" || payload["reaction"].(map[string]any)["emoji"] != "👍" {
			t.Fatalf("unexpected reaction payload %#v", payload)
		}
		return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.reaction"}]}`), nil
	})}
	provider, _ := NewMetaTextProvider(client, "phone-id", "access-token")
	if _, err := provider.SendTemplate(context.Background(), "56912345678", SendTemplate{Name: "recordatorio_viaje", Language: "es_CL", Parameters: []string{"Nico"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SendReaction(context.Background(), "56912345678", "wamid.original", "👍"); err != nil {
		t.Fatal(err)
	}
}

func TestMetaProviderSendsLocationAndContacts(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			if payload["type"] != "location" || payload["location"].(map[string]any)["name"] != "Oficina" {
				t.Fatalf("unexpected location payload %#v", payload)
			}
		} else if payload["type"] != "contacts" || len(payload["contacts"].([]any)) != 1 {
			t.Fatalf("unexpected contacts payload %#v", payload)
		} else {
			contact := payload["contacts"].([]any)[0].(map[string]any)
			name := contact["name"].(map[string]any)
			if name["formatted_name"] != "Ana" || name["first_name"] != "Ana" {
				t.Fatalf("unexpected contact name payload %#v", name)
			}
		}
		return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.rich"}]}`), nil
	})}
	provider, _ := NewMetaTextProvider(client, "phone-id", "access-token")
	if _, err := provider.SendRichMessage(context.Background(), "56912345678", MessageRichContent{Kind: "location", Location: &MessageLocation{Latitude: -33.4, Longitude: -70.6, Name: "Oficina"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SendRichMessage(context.Background(), "56912345678", MessageRichContent{Kind: "contacts", Contacts: []MessageContact{{Name: "Ana", Phone: "+56911111111", Email: "ana@example.com", Company: "Indomito"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestMetaContactName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     string
		wantFirst string
		wantError bool
	}{
		{name: "single name", value: "Ana", wantFirst: "Ana"},
		{name: "full name", value: "  Ana Maria Perez  ", wantFirst: "Ana"},
		{name: "empty name", value: "   ", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := metaContactName(test.value)
			if test.wantError {
				if err == nil {
					t.Fatal("expected validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("metaContactName() error = %v", err)
			}
			if got["first_name"] != test.wantFirst || got["formatted_name"] != strings.TrimSpace(test.value) {
				t.Fatalf("metaContactName() = %#v", got)
			}
		})
	}
}

func TestMetaProviderSendsInteractiveButtons(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Type        string `json:"type"`
			Interactive struct {
				Type   string `json:"type"`
				Action struct {
					Buttons []map[string]any `json:"buttons"`
				} `json:"action"`
			} `json:"interactive"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type != "interactive" || payload.Interactive.Type != "button" || len(payload.Interactive.Action.Buttons) != 2 {
			t.Fatalf("unexpected interactive payload %#v", payload)
		}
		return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.interactive"}]}`), nil
	})}
	provider, _ := NewMetaTextProvider(client, "phone-id", "access-token")
	_, err := provider.SendRichMessage(context.Background(), "56912345678", MessageRichContent{Kind: "interactive", Interactive: &MessageInteractive{Kind: "button", Body: "Elige", Options: []MessageInteractiveOption{{ID: "one", Title: "Uno"}, {ID: "two", Title: "Dos"}}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMetaProviderSendsStickerByUploadedMediaID(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonHTTPResponse(http.StatusOK, `{"id":"media-sticker"}`), nil
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["type"] != "sticker" || payload["sticker"].(map[string]any)["id"] != "media-sticker" {
			t.Fatalf("unexpected sticker payload %#v", payload)
		}
		return jsonHTTPResponse(http.StatusOK, `{"messages":[{"id":"wamid.sticker"}]}`), nil
	})}
	provider, _ := NewMetaTextProvider(client, "phone-id", "access-token")
	if _, err := provider.SendSticker(context.Background(), "56912345678", "sticker.webp", "image/webp", []byte("RIFFWEBP")); err != nil {
		t.Fatal(err)
	}
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
