package meet

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "os"
 "strings"
 "time"
)

type OAuthCredentials struct { ClientID string `json:"client_id"`; ClientSecret string `json:"client_secret"`; RefreshToken string `json:"refresh_token"` }
type TokenResponse struct { AccessToken string `json:"access_token"`; ExpiresIn int `json:"expires_in"` }

// ParameterReader is implemented by the AWS SSM adapter. Never log decrypted values.
type ParameterReader interface { GetSecure(ctx context.Context, name string) (string, error) }
type Client struct { HTTP *http.Client; Parameters ParameterReader; CalendarID string; OAuthParameter string }

func (c Client) accessToken(ctx context.Context) (string,error) {
 if c.Parameters == nil || c.OAuthParameter == "" { return "", errors.New("Google Calendar OAuth SSM configuration missing") }
 secret,err:=c.Parameters.GetSecure(ctx,c.OAuthParameter);if err!=nil{return "",fmt.Errorf("read OAuth parameter: %w",err)}
 var credentials OAuthCredentials
 if err=json.Unmarshal([]byte(secret),&credentials);err!=nil{return "",errors.New("invalid OAuth JSON")}
 if credentials.ClientID==""||credentials.ClientSecret==""||credentials.RefreshToken=="" {return "",errors.New("incomplete OAuth credentials")}
 form:=url.Values{"grant_type":{"refresh_token"},"client_id":{credentials.ClientID},"client_secret":{credentials.ClientSecret},"refresh_token":{credentials.RefreshToken}}
 req,err:=http.NewRequestWithContext(ctx,http.MethodPost,"https://oauth2.googleapis.com/token",strings.NewReader(form.Encode()));if err!=nil{return "",err}
 req.Header.Set("Content-Type","application/x-www-form-urlencoded")
 resp,err:=c.http().Do(req);if err!=nil{return "",err};defer resp.Body.Close()
 if resp.StatusCode!=http.StatusOK{return "",fmt.Errorf("Google OAuth token exchange failed: status %d",resp.StatusCode)}
 var token TokenResponse
 if err=json.NewDecoder(io.LimitReader(resp.Body,1<<20)).Decode(&token);err!=nil{return "",err}
 if token.AccessToken=="" {return "",errors.New("Google OAuth response missing access token")}
 return token.AccessToken,nil
}
func(c Client) http()*http.Client {if c.HTTP!=nil{return c.HTTP};return &http.Client{Timeout:15*time.Second}}
func(c Client) EventsURL() (string,error) {
 if c.CalendarID=="" {return "",errors.New("GOOGLE_CALENDAR_ID missing")}
 return "https://www.googleapis.com/calendar/v3/calendars/"+url.PathEscape(c.CalendarID)+"/events",nil
}
func(c Client) Request(ctx context.Context,method,eventID string,query url.Values,body any)(json.RawMessage,error){
 token,err:=c.accessToken(ctx);if err!=nil{return nil,err}
 endpoint,err:=c.EventsURL();if err!=nil{return nil,err}
 if eventID!=""{endpoint+="/"+url.PathEscape(eventID)}
 if len(query)>0{endpoint+="?"+query.Encode()}
 var reader io.Reader
 if body!=nil {payload,e:=json.Marshal(body);if e!=nil{return nil,e};reader=strings.NewReader(string(payload))}
 req,err:=http.NewRequestWithContext(ctx,method,endpoint,reader);if err!=nil{return nil,err}
 req.Header.Set("Authorization","Bearer "+token);if body!=nil{req.Header.Set("Content-Type","application/json")}
 resp,err:=c.http().Do(req);if err!=nil{return nil,err};defer resp.Body.Close()
 response,err:=io.ReadAll(io.LimitReader(resp.Body,4<<20));if err!=nil{return nil,err}
 if resp.StatusCode<200||resp.StatusCode>=300{return nil,fmt.Errorf("Google Calendar API error: status %d",resp.StatusCode)}
 return response,nil
}
func ConfigFromEnvironment(parameters ParameterReader) Client {
 return Client{Parameters:parameters,CalendarID:os.Getenv("GOOGLE_CALENDAR_ID"),OAuthParameter:os.Getenv("GOOGLE_CALENDAR_OAUTH_SSM_PATH")}
}
