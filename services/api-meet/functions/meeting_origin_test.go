package functions

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-lambda-go/events"
	meet "ind-hub-api-gox-sls-pri-gh/services/api-meet/google"
	"net/http"
	"testing"
)

func TestAgentMeetingTitleProtectedByPersistedOrigin(t *testing.T) {
	for _, origin := range []string{"private", "historical", "manual"} {
		t.Run(origin, func(t *testing.T) {
			patched := false
			original := calendarEvent{Summary: "AG — Cliente [ABC123]"}
			original.ExtendedProperties.Private = map[string]string{"agent": "false"}
			if origin == "private" {
				original.ExtendedProperties.Private["agent"] = "true"
			}
			if origin == "historical" {
				original.ExtendedProperties.Shared = map[string]string{"createdBy": "indomito-inbox-agent", "defaultHostSk": "MEET_HOST#one", "defaultHostName": "Coordinadora"}
			}
			client := meet.Client{Parameters: mockSecrets{}, OAuthParameter: "/oauth", CalendarID: "calendar", HTTP: &http.Client{Transport: mockTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host == "oauth2.googleapis.com" {
					return mockHTTP(200, map[string]any{"access_token": "test"}), nil
				}
				if req.Method == "GET" {
					return mockHTTP(200, original), nil
				}
				if req.Method == "PATCH" {
					patched = true
					var body struct {
						Summary            string `json:"summary"`
						ExtendedProperties struct {
							Private map[string]string `json:"private"`
						} `json:"extendedProperties"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body.ExtendedProperties.Private["agent"] != "false" {
						t.Fatal("manual edit spoofed agent origin")
					}
					return mockHTTP(200, original), nil
				}
				t.Fatalf("unexpected request %s", req.Method)
				return nil, nil
			})}}
			request := events.APIGatewayV2HTTPRequest{PathParameters: map[string]string{"eventId": "one"}, Body: `{"title":"Renombrado","date":"2026-11-20","startTime":"10:00","endTime":"11:00","agent":true}`}
			request.RequestContext.HTTP.Method = "PUT"
			result, err := handleWithClient(context.Background(), request, client)
			if err != nil {
				t.Fatal(err)
			}
			if origin == "manual" {
				if result.StatusCode != 200 || !patched {
					t.Fatalf("manual title not editable: %+v", result)
				}
			} else if result.StatusCode != 409 || patched {
				t.Fatalf("agent title was not protected: %+v", result)
			}
			if origin == "historical" {
				view := toView(original)
				if view["hostSk"] != "MEET_HOST#one" || view["hostName"] != "Coordinadora" {
					t.Fatal("historical host lost")
				}
			}
		})
	}
}
