package domain

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type templateCatalogFake struct {
	items []MessageTemplate
	err   error
}

func (f templateCatalogFake) ListApproved(context.Context) ([]MessageTemplate, error) {
	return f.items, f.err
}

type templateSenderFake struct {
	calls int
}

func (f *templateSenderFake) SendTemplate(context.Context, string, string, string, SendTemplate) (SendState, error) {
	f.calls++
	return SendState{State: SendCommitted}, nil
}

func TestTemplateServiceSendsOnlyApprovedSupportedExactTemplate(t *testing.T) {
	t.Parallel()
	sender := &templateSenderFake{}
	service, err := NewTemplateService(templateCatalogFake{items: []MessageTemplate{{Name: "recordatorio", Language: "es_CL", Status: "APPROVED", Supported: true, ParameterCount: 1}}}, sender)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.SendTemplate(context.Background(), "owner", "56912345678", "client-id", SendTemplate{Name: "recordatorio", Language: "es_CL", Parameters: []string{"Nico"}})
	if err != nil || state.State != SendCommitted || sender.calls != 1 {
		t.Fatalf("unexpected approved send state=%#v calls=%d err=%v", state, sender.calls, err)
	}
	_, err = service.SendTemplate(context.Background(), "owner", "56912345678", "client-id-2", SendTemplate{Name: "recordatorio", Language: "es_CL"})
	if err != ErrTemplateNotApproved || sender.calls != 1 {
		t.Fatalf("expected rejection without provider call, calls=%d err=%v", sender.calls, err)
	}
}

func TestMetaTemplateCatalogPaginatesAndFiltersApprovedTemplates(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("authorization") != "Bearer token" {
			t.Fatal("missing bearer token")
		}
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"name":"recordatorio","language":"es_CL","category":"UTILITY","status":"APPROVED","components":[{"type":"BODY","text":"Hola {{1}}"}]}],"paging":{"cursors":{"after":"cursor-2"},"next":"https://graph.facebook.com/next"}}`)), Header: make(http.Header)}, nil
		}
		if request.URL.Query().Get("after") != "cursor-2" {
			t.Fatalf("unexpected pagination query %s", request.URL.RawQuery)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"name":"rechazada","language":"es","category":"UTILITY","status":"REJECTED","components":[]},{"name":"saludo","language":"es","category":"UTILITY","status":"APPROVED","components":[{"type":"BODY","text":"Hola"}]}]}`)), Header: make(http.Header)}, nil
	})}
	catalog := NewMetaTemplateCatalog(client, "waba-id", "token")
	items, err := catalog.ListApproved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(items) != 2 || items[0].Name != "recordatorio" || items[0].ParameterCount != 1 || items[1].Name != "saludo" {
		t.Fatalf("unexpected inventory calls=%d items=%#v", calls, items)
	}
}

func TestNormalizeMessagesInitializesCollections(t *testing.T) {
	t.Parallel()
	messages := []Message{{}}
	normalizeMessages(messages)
	if messages[0].Attachments == nil || messages[0].Reactions == nil {
		t.Fatalf("expected non-nil collections: %#v", messages[0])
	}
}
