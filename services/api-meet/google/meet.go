package meet

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "strings"
)

func (c Client) CreateMeetSpace(ctx context.Context)(string,error){
 host:=c
 host.OAuthParameter=c.HostOAuthPath()
 token,err:=host.accessToken(ctx);if err!=nil{return "",err}
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,"https://meet.googleapis.com/v2/spaces",strings.NewReader("{\"config\":{\"accessType\":\"OPEN\"}}"));if err!=nil{return "",err}
 req.Header.Set("Authorization","Bearer "+token)
 req.Header.Set("Content-Type","application/json")
 response,err:=c.http().Do(req);if err!=nil{return "",err};defer response.Body.Close()
 if response.StatusCode<200||response.StatusCode>=300{return "",fmt.Errorf("Meet API create space failed: HTTP %d",response.StatusCode)}
 var space struct{MeetingURI string `json:"meetingUri"`}
 if err=json.NewDecoder(io.LimitReader(response.Body,1<<20)).Decode(&space);err!=nil{return "",err}
 if space.MeetingURI==""{return "",errors.New("Meet API response missing meetingUri")}
 return space.MeetingURI,nil
}
