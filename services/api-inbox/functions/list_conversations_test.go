package functions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

type fakeConversationLister struct {
	firstOwner string
	firstQuery string
	nextCursor string
	response   domain.ConversationListResponse
	err        error
}

func (fake *fakeConversationLister) FirstPage(_ context.Context, owner, query string) (domain.ConversationListResponse, error) {
	fake.firstOwner, fake.firstQuery = owner, query
	return fake.response, fake.err
}

func (fake *fakeConversationLister) NextPage(_ context.Context, owner, query, cursor string) (domain.ConversationListResponse, error) {
	fake.firstOwner, fake.firstQuery, fake.nextCursor = owner, query, cursor
	return fake.response, fake.err
}

func TestListConversationsReturnsCollectionContract(t *testing.T) {
	fake := &fakeConversationLister{response: domain.ConversationListResponse{Data: []domain.ConversationSummary{}, PageInfo: domain.PageInfo{Page: 1, PageSize: 200}}}
	response, err := ListConversations(context.Background(), fake, listRequest(map[string]string{"page": "1", "pageSize": "200", "q": " viaje "}))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
	if fake.firstOwner != "user-1" || fake.firstQuery != "viaje" || response.Headers["cache-control"] != "no-store" {
		t.Fatalf("fake = %#v, headers = %#v", fake, response.Headers)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := body["data"].(map[string]any); wrapped {
		t.Fatal("collection response must not be wrapped in an additional data envelope")
	}
	if _, ok := body["pageInfo"]; !ok {
		t.Fatalf("body = %#v", body)
	}
}

func TestListConversationsRequiresCursorFromSecondPage(t *testing.T) {
	response, _ := ListConversations(context.Background(), &fakeConversationLister{}, listRequest(map[string]string{"page": "2", "pageSize": "200"}))
	assertErrorCode(t, response, 400, "VALIDATION_INPUT_INVALID")
}

func TestListConversationsMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid cursor", err: domain.ErrInvalidCursor, status: 400, code: "VALIDATION_CURSOR_INVALID"},
		{name: "expired cursor", err: domain.ErrExpiredCursor, status: 400, code: "INBOX_CURSOR_EXPIRED"},
		{name: "slot quota", err: domain.ErrSnapshotLimitReached, status: 429, code: "INBOX_SNAPSHOT_LIMIT_REACHED"},
		{name: "capacity", err: domain.ErrListCapacityExceeded, status: 503, code: "INBOX_CAPACITY_UNAVAILABLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, _ := ListConversations(context.Background(), &fakeConversationLister{err: test.err}, listRequest(map[string]string{"page": "1", "pageSize": "200"}))
			assertErrorCode(t, response, test.status, test.code)
		})
	}
}

func TestListConversationsRejectsMissingIdentity(t *testing.T) {
	request := listRequest(nil)
	request.RequestContext.Authorizer = nil
	response, _ := ListConversations(context.Background(), &fakeConversationLister{err: errors.New("must not run")}, request)
	assertErrorCode(t, response, 401, "AUTH_UNAUTHORIZED")
}

func listRequest(query map[string]string) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{QueryStringParameters: query, RequestContext: events.APIGatewayV2HTTPRequestContext{RequestID: "00000000-0000-0000-0000-000000000001", Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{Lambda: map[string]any{"userId": "user-1"}}}}
}

func assertErrorCode(t *testing.T, response events.APIGatewayV2HTTPResponse, status int, code string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d; body = %s", response.StatusCode, status, response.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != code {
		t.Fatalf("code = %v, want %s", body["code"], code)
	}
}
