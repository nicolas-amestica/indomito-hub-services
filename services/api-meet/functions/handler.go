package functions

import (
 "context"
 "encoding/json"
 "net/http"
 "net/url"
 "strconv"
 "strings"
 "time"

 "github.com/aws/aws-lambda-go/events"
 "ind-hub-api-gox-sls-pri-gh/services/api-meet/google"
)

type request struct {
 Title string `json:"title"`
 Date string `json:"date"`
 StartTime string `json:"startTime"`
 EndTime string `json:"endTime"`
 Description string `json:"description"`
 Guests []string `json:"guests"`
 HostSK *string `json:"hostSk"`
 HostName *string `json:"hostName"`
 HostColor *string `json:"hostColor"`
}
type calendarEvent struct {
 ID string `json:"id"`
 Summary string `json:"summary"`
 Description string `json:"description"`
 Start struct{DateTime string `json:"dateTime"`;Date string `json:"date"`} `json:"start"`
 End struct{DateTime string `json:"dateTime"`;Date string `json:"date"`} `json:"end"`
 Attendees []struct{Email string `json:"email"`} `json:"attendees"`
 HangoutLink string `json:"hangoutLink"`
 Created string `json:"created"`
 ExtendedProperties struct{Private map[string]string `json:"private"`} `json:"extendedProperties"`
}
func reply(status int, payload any) events.APIGatewayV2HTTPResponse {
 b,_:=json.Marshal(payload)
 return events.APIGatewayV2HTTPResponse{StatusCode:status,Headers:map[string]string{"content-type":"application/json"},Body:string(b)}
}
func fail(status int,code string)events.APIGatewayV2HTTPResponse{return reply(status,map[string]string{"error":code})}
func toView(e calendarEvent)map[string]any{
 date,start,end:="","",""
 if t,err:=time.Parse(time.RFC3339,e.Start.DateTime);err==nil {local:=t.In(santiago());date=local.Format("2006-01-02");start=local.Format("15:04")} else if e.Start.Date!=""{date=e.Start.Date}
 if t,err:=time.Parse(time.RFC3339,e.End.DateTime);err==nil {end=t.In(santiago()).Format("15:04")}
 guests:=make([]string,0,len(e.Attendees));for _,a:=range e.Attendees {guests=append(guests,a.Email)}
 link:=e.HangoutLink
 if link=="" {link=e.ExtendedProperties.Private["meetLink"]}
 return map[string]any{"id":e.ID,"title":e.Summary,"date":date,"startTime":start,"endTime":end,"description":e.Description,"guests":guests,"googleMeetLink":link,"createdAt":e.Created,"hostSk":e.ExtendedProperties.Private["hostSk"],"hostName":e.ExtendedProperties.Private["hostName"],"hostColor":e.ExtendedProperties.Private["hostColor"]}
}
func santiago()*time.Location{l,e:=time.LoadLocation("America/Santiago");if e!=nil{return time.UTC};return l}
func Handle(ctx context.Context, req events.APIGatewayV2HTTPRequest)(events.APIGatewayV2HTTPResponse,error){
 client,err:=meet.CalendarFromEnvironment(ctx);if err!=nil{return fail(503,"calendar_not_configured"),nil}
 method:=req.RequestContext.HTTP.Method
 if method=="GET" {
  year,e1:=strconv.Atoi(req.QueryStringParameters["year"]);month,e2:=strconv.Atoi(req.QueryStringParameters["month"])
  if e1!=nil||e2!=nil||year<2000||year>2100||month<1||month>12{return fail(400,"invalid_year_month"),nil}
  from:=time.Date(year,time.Month(month),1,0,0,0,0,santiago());until:=from.AddDate(0,1,0)
  query:=url.Values{"timeMin":{from.Format(time.RFC3339)},"timeMax":{until.Format(time.RFC3339)},"singleEvents":{"true"},"maxResults":{"250"}}
  result:=make([]map[string]any,0)
  for {
   raw,e:=client.Request(ctx,"GET","",query,nil);if e!=nil{return fail(502,"google_calendar_error"),nil}
   var page struct{Items []calendarEvent `json:"items"`;NextPageToken string `json:"nextPageToken"`}
   if json.Unmarshal(raw,&page)!=nil{return fail(502,"invalid_calendar_response"),nil}
   for _,item:=range page.Items{v:=toView(item);if d,ok:=v["date"].(string);ok&&strings.HasPrefix(d,from.Format("2006-01")){result=append(result,v)}}
   if page.NextPageToken==""{break};if len(result)>5000{return fail(502,"calendar_result_too_large"),nil};query.Set("pageToken",page.NextPageToken)
  }
  return reply(200,map[string]any{"data":result}),nil
 }
 id:=req.PathParameters["eventId"]
 if method=="DELETE" {
  if id=="" {return fail(400,"missing_event_id"),nil}
  _,e:=client.Request(ctx,"DELETE",id,nil,nil);if e!=nil{return fail(502,"google_calendar_error"),nil}
  return reply(200,map[string]bool{"ok":true}),nil
 }
 if method!="POST"&&method!="PUT"{return fail(405,"method_not_allowed"),nil}
 if method=="PUT"&&id==""{return fail(400,"missing_event_id"),nil}
 var input request
 if json.Unmarshal([]byte(req.Body),&input)!=nil||strings.TrimSpace(input.Title)==""{return fail(400,"invalid_body"),nil}
 start,e:=time.ParseInLocation("2006-01-02 15:04",input.Date+" "+input.StartTime,santiago());if e!=nil{return fail(400,"invalid_date_time"),nil}
 end:=start.Add(time.Hour)
 if input.EndTime!="" {
  parsed,parseErr:=time.ParseInLocation("2006-01-02 15:04",input.Date+" "+input.EndTime,santiago())
  if parseErr!=nil{return fail(400,"invalid_end_time"),nil}
  if !parsed.After(start){parsed=parsed.AddDate(0,0,1)}
  if parsed.Sub(start)>24*time.Hour{return fail(400,"invalid_duration"),nil}
  end=parsed
 }
 attendees:=make([]map[string]string,0,len(input.Guests))
 for _,g:=range input.Guests{if strings.Contains(g,"@"){attendees=append(attendees,map[string]string{"email":g})}}
 private:=map[string]string{}
 if method=="PUT" {
  original,fetchErr:=client.Request(ctx,"GET",id,nil,nil)
  if fetchErr!=nil{return fail(502,"google_calendar_error"),nil}
  var existing calendarEvent
  if json.Unmarshal(original,&existing)!=nil{return fail(502,"invalid_calendar_response"),nil}
  for k,v:=range existing.ExtendedProperties.Private{private[k]=v}
 }
 if method=="POST" {
  meetingURI,meetErr:=client.CreateMeetSpace(ctx)
  if meetErr!=nil{return fail(502,"google_meet_error"),nil}
  private["meetLink"]=meetingURI
 }
 if input.HostSK!=nil{private["hostSk"]=*input.HostSK};if input.HostName!=nil{private["hostName"]=*input.HostName};if input.HostColor!=nil{private["hostColor"]=*input.HostColor}
 body:=map[string]any{"summary":input.Title,"description":input.Description,"start":map[string]string{"dateTime":start.Format(time.RFC3339),"timeZone":"America/Santiago"},"end":map[string]string{"dateTime":end.Format(time.RFC3339),"timeZone":"America/Santiago"},"attendees":attendees,"extendedProperties":map[string]any{"private":private}}
 verb:="POST";if method=="PUT"{verb="PATCH"}
 raw,e:=client.Request(ctx,verb,id,url.Values{"sendUpdates":{"all"}},body);if e!=nil{return fail(502,"google_calendar_error"),nil}
 var saved calendarEvent;if json.Unmarshal(raw,&saved)!=nil{return fail(502,"invalid_calendar_response"),nil}
 status:=200;if method=="POST"{status=201}
 return reply(status,map[string]any{"data":toView(saved)}),nil
}
