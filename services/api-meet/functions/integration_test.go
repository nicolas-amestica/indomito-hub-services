package functions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	meet "ind-hub-api-gox-sls-pri-gh/services/api-meet/google"
)

type mockSecrets struct{}
func (mockSecrets) GetSecure(_ context.Context, name string) (string,error) {
	return `{"client_id":"client","client_secret":"secret","refresh_token":"refresh"}`,nil
}

type mockTransport func(*http.Request)(*http.Response,error)
func (fn mockTransport) RoundTrip(req *http.Request)(*http.Response,error){return fn(req)}
func mockHTTP(status int,body any)*http.Response{
	data,_:=json.Marshal(body)
	return &http.Response{StatusCode:status,Header:make(http.Header),Body:io.NopCloser(strings.NewReader(string(data)))}
}

func TestMeetingLifecycleWithGoogleMock(t *testing.T){
	stored:=map[string]map[string]any{}
	meetCalls:=0
	calendarPosts:=0
	client:=meet.Client{
		Parameters:mockSecrets{},
		OAuthParameter:"/oauth",HostOAuthParameter:"/host",
		CalendarID:"test-calendar@example.com",
		HTTP:&http.Client{Transport:mockTransport(func(req *http.Request)(*http.Response,error){
			switch req.URL.Host {
			case "oauth2.googleapis.com":
				return mockHTTP(200,map[string]any{"access_token":"fake-token"}),nil
			case "meet.googleapis.com":
				meetCalls++
				return mockHTTP(200,map[string]any{"meetingUri":"https://meet.google.com/test-space"}),nil
			case "www.googleapis.com":
				path:=req.URL.Path
				const suffix="/events"
				idx:=strings.Index(path,suffix)
				if idx<0{t.Errorf("unexpected Calendar route: %s",path);return mockHTTP(404,map[string]any{}),nil}
				id:=strings.TrimPrefix(path[idx+len(suffix):],"/")
				switch req.Method {
				case "GET":
					if id==""{
						items:=[]map[string]any{}
						for _,v:=range stored{items=append(items,v)}
						return mockHTTP(200,map[string]any{"items":items}),nil
					}
					if v,ok:=stored[id];ok{return mockHTTP(200,v),nil}
					return mockHTTP(404,map[string]any{"error":"not found"}),nil
				case "POST":
					calendarPosts++
					if req.URL.Query().Get("sendUpdates")!="none"{t.Error("event placeholder must not send invitations")}
					var event map[string]any
					if err:=json.NewDecoder(req.Body).Decode(&event);err!=nil{return nil,err}
					eventID,ok:=event["id"].(string)
					if !ok||eventID==""{eventID="temporaryeventid123"}
					if _,found:=stored[eventID];found{return mockHTTP(409,map[string]any{}),nil}
					event["id"]=eventID
					stored[eventID]=event
					return mockHTTP(200,event),nil
				case "PATCH":
					if req.URL.Query().Get("sendUpdates")!="all"{t.Error("final event must send invitations")}
					old,ok:=stored[id]
					if !ok{return mockHTTP(404,map[string]any{}),nil}
					var update map[string]any
					if err:=json.NewDecoder(req.Body).Decode(&update);err!=nil{return nil,err}
					for k,v:=range update{old[k]=v}
					return mockHTTP(200,old),nil
				case "DELETE":
					delete(stored,id)
					return mockHTTP(204,map[string]any{}),nil
				}
			}
			t.Errorf("unexpected request: %s %s",req.Method,req.URL)
			return mockHTTP(500,map[string]any{}),nil
		})},
	}
	roundtrip:=func(method,id string,payload any)(events.APIGatewayV2HTTPResponse,error){
		var body string
		if payload!=nil{b,_:=json.Marshal(payload);body=string(b)}
		return handleWithClient(context.Background(),events.APIGatewayV2HTTPRequest{
			Body:body,PathParameters:map[string]string{"eventId":id},
			RequestContext:events.APIGatewayV2HTTPRequestContext{
				HTTP:events.APIGatewayV2HTTPRequestContextHTTPDescription{Method:method},
			},
		},client)
	}
	newMeeting:=request{
		Title:"Reunión de coordinación",Date:"2026-10-11",StartTime:"11:00",EndTime:"12:30",
		Guests:[]string{"test@example.com"},IdempotencyKey:"same-key-for-retries",
	}
	first,err:=roundtrip("POST","",newMeeting)
	if err!=nil||first.StatusCode!=201{t.Fatalf("first create: %+v %v",first,err)}
	var response struct{Data struct{ID string `json:"id"`;Link string `json:"googleMeetLink"`} `json:"data"`}
	if err:=json.Unmarshal([]byte(first.Body),&response);err!=nil{t.Fatal(err)}
	if response.Data.ID==""||response.Data.Link!="https://meet.google.com/test-space"{t.Fatalf("create response: %+v",response)}
	if meetCalls!=1||calendarPosts!=1{t.Fatalf("create calls: meet=%d calendar=%d",meetCalls,calendarPosts)}
	second,err:=roundtrip("POST","",newMeeting)
	if err!=nil||second.StatusCode!=200{t.Fatalf("retry: %+v %v",second,err)}
	if meetCalls!=1||calendarPosts!=1{t.Fatal("idempotent retry created duplicate resources")}
	newMeeting.Title="Reunión actualizada"
	edit,err:=roundtrip("PUT",response.Data.ID,newMeeting)
	if err!=nil||edit.StatusCode!=200{t.Fatalf("edit: %+v %v",edit,err)}
	if !strings.Contains(edit.Body,"https://meet.google.com/test-space"){t.Fatal("edit lost Meet URL")}
	removed,err:=roundtrip("DELETE",response.Data.ID,nil)
	if err!=nil||removed.StatusCode!=200{t.Fatalf("delete: %+v %v",removed,err)}
	if len(stored)!=0{t.Fatalf("calendar event was not removed: %d",len(stored))}
}
