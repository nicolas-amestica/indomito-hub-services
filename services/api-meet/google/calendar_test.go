package meet

import (
 "context"
 "errors"
 "testing"
)

type fakeReader struct{}
func (fakeReader) GetSecure(context.Context,string)(string,error){return "",errors.New("unavailable")}

func TestHostOAuthPath(t *testing.T){
 shared:=Client{OAuthParameter:"/calendar"}
 if got:=shared.HostOAuthPath();got!="/calendar"{t.Fatalf("shared OAuth: %s",got)}
 dedicated:=Client{OAuthParameter:"/calendar",HostOAuthParameter:"/meet"}
 if got:=dedicated.HostOAuthPath();got!="/meet"{t.Fatalf("dedicated OAuth: %s",got)}
}
func TestEventsURL(t *testing.T){
 c:=Client{CalendarID:"team@example.com"}
 got,err:=c.EventsURL()
 if err!=nil{t.Fatal(err)}
 if got==""{t.Fatal("empty events URL")}
}
func TestMissingOAuth(t *testing.T){
 c:=Client{Parameters:fakeReader{}}
 if _,err:=c.accessToken(context.Background());err==nil{t.Fatal("missing OAuth path must fail")}
}
