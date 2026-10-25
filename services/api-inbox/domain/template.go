package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ErrTemplateInventoryUnavailable = errors.New("template inventory is unavailable")
var ErrTemplateNotApproved = errors.New("template is not approved or supported")
var templateVariablePattern = regexp.MustCompile(`\{\{([1-9][0-9]*)\}\}`)

type MessageTemplate struct {
	Name           string `json:"name"`
	Language       string `json:"language"`
	Category       string `json:"category"`
	Status         string `json:"status"`
	Body           string `json:"body"`
	ParameterCount int    `json:"parameterCount"`
	Supported      bool   `json:"supported"`
}

type TemplateCatalog interface {
	ListApproved(context.Context) ([]MessageTemplate, error)
}

type DurableTemplateSender interface {
	SendTemplate(context.Context, string, string, string, SendTemplate) (SendState, error)
}

// TemplateService ensures that only a currently approved and supported
// template, with the exact number of required body parameters, reaches Meta.
type TemplateService struct {
	catalog TemplateCatalog
	sender  DurableTemplateSender
}

func NewTemplateService(catalog TemplateCatalog, sender DurableTemplateSender) (*TemplateService, error) {
	if catalog == nil || sender == nil {
		return nil, errors.New("template dependencies are required")
	}
	return &TemplateService{catalog: catalog, sender: sender}, nil
}

func (s *TemplateService) SendTemplate(ctx context.Context, ownerID, phone, clientMessageID string, requested SendTemplate) (SendState, error) {
	templates, err := s.catalog.ListApproved(ctx)
	if err != nil {
		return SendState{}, err
	}
	for _, template := range templates {
		if template.Name == requested.Name && template.Language == requested.Language && template.Supported && template.ParameterCount == len(requested.Parameters) {
			return s.sender.SendTemplate(ctx, ownerID, phone, clientMessageID, requested)
		}
	}
	return SendState{}, ErrTemplateNotApproved
}

type MetaTemplateCatalog struct {
	client                *http.Client
	endpoint, accessToken string
}

func NewMetaTemplateCatalog(client *http.Client, businessAccountID, accessToken string) *MetaTemplateCatalog {
	if client == nil {
		client = &http.Client{}
	}
	endpoint := ""
	if businessAccountID != "" {
		endpoint = metaGraphResourceURL(businessAccountID) + "/message_templates"
	}
	return &MetaTemplateCatalog{client: client, endpoint: endpoint, accessToken: accessToken}
}

func (c *MetaTemplateCatalog) ListApproved(ctx context.Context) ([]MessageTemplate, error) {
	if c.endpoint == "" {
		return nil, ErrTemplateInventoryUnavailable
	}
	type metaTemplatePage struct {
		Data []struct {
			Name, Language, Category, Status string
			Components                       []struct{ Type, Text, Format string } `json:"components"`
		} `json:"data"`
		Paging struct {
			Cursors struct {
				After string `json:"after"`
			} `json:"cursors"`
			Next string `json:"next"`
		} `json:"paging"`
	}
	items := make([]metaTemplatePage, 0, 1)
	after := ""
	for page := 0; page < 20; page++ {
		values := url.Values{"fields": {"name,language,category,status,components"}, "limit": {"250"}}
		if after != "" {
			values.Set("after", after)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+values.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("authorization", "Bearer "+c.accessToken)
		startedAt := time.Now()
		response, err := c.client.Do(req)
		if err != nil {
			recordMetaGraphCall("list_templates", "transport", startedAt)
			return nil, fmt.Errorf("list Meta templates: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		_ = response.Body.Close()
		if readErr != nil {
			recordMetaGraphCall("list_templates", "response_read", startedAt)
			return nil, readErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			recordMetaGraphCall("list_templates", metaGraphStatusOutcome(response.StatusCode), startedAt)
			return nil, fmt.Errorf("list Meta templates status %d", response.StatusCode)
		}
		var parsed metaTemplatePage
		if err = json.Unmarshal(body, &parsed); err != nil {
			recordMetaGraphCall("list_templates", "invalid_response", startedAt)
			return nil, err
		}
		recordMetaGraphCall("list_templates", "success", startedAt)
		items = append(items, parsed)
		if parsed.Paging.Next == "" || parsed.Paging.Cursors.After == "" || parsed.Paging.Cursors.After == after {
			break
		}
		after = parsed.Paging.Cursors.After
	}
	result := make([]MessageTemplate, 0)
	for _, parsed := range items {
		for _, item := range parsed.Data {
			if item.Status != "APPROVED" {
				continue
			}
			bodyText, count, supported := "", 0, true
			for _, component := range item.Components {
				switch component.Type {
				case "BODY":
					bodyText = component.Text
					for _, match := range templateVariablePattern.FindAllStringSubmatch(component.Text, -1) {
						var index int
						_, _ = fmt.Sscan(match[1], &index)
						if index > count {
							count = index
						}
					}
				case "FOOTER":
				case "HEADER":
					if component.Format != "TEXT" && component.Format != "" {
						supported = false
					}
				default:
					supported = false
				}
			}
			result = append(result, MessageTemplate{Name: item.Name, Language: item.Language, Category: item.Category, Status: item.Status, Body: strings.TrimSpace(bodyText), ParameterCount: count, Supported: supported})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Language < result[j].Language
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
