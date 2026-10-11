package meet

import (
 "context"
 "errors"
 "fmt"
 "os"

 "github.com/aws/aws-sdk-go-v2/aws"
 "github.com/aws/aws-sdk-go-v2/config"
 "github.com/aws/aws-sdk-go-v2/service/ssm"
)

type SSMReader struct { Client *ssm.Client }

func NewSSMReader(ctx context.Context) (*SSMReader,error) {
 cfg,err:=config.LoadDefaultConfig(ctx)
 if err!=nil{return nil,fmt.Errorf("AWS config: %w",err)}
 return &SSMReader{Client:ssm.NewFromConfig(cfg)},nil
}

func (r *SSMReader) GetSecure(ctx context.Context,name string)(string,error){
 if name=="" {return "",errors.New("SSM parameter path is required")}
 if r==nil||r.Client==nil{return "",errors.New("SSM client is not initialized")}
 out,err:=r.Client.GetParameter(ctx,&ssm.GetParameterInput{Name:aws.String(name),WithDecryption:aws.Bool(true)})
 if err!=nil{return "",fmt.Errorf("SSM GetParameter %q: %w",name,err)}
 if out.Parameter==nil||out.Parameter.Value==nil{return "",errors.New("SSM parameter has no value")}
 return *out.Parameter.Value,nil
}

func CalendarFromEnvironment(ctx context.Context)(Client,error){
 reader,err:=NewSSMReader(ctx);if err!=nil{return Client{},err}
 client:=ConfigFromEnvironment(reader)
 if client.CalendarID==""||client.OAuthParameter=="" {return Client{},fmt.Errorf("missing GOOGLE_CALENDAR_ID or GOOGLE_CALENDAR_OAUTH_SSM_PATH")}
 _ = os.Getenv("APP_STAGE")
 return client,nil
}
