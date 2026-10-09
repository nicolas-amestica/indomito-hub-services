// Package masteraccess administra la credencial de soporte del portal de pagos.
package masteraccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/libs/shared/apperr"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/functions"
)

const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

type item struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
	Digest string `dynamodbav:"digest,omitempty"`
	Active bool `dynamodbav:"active"`
	Version int64 `dynamodbav:"version"`
	UpdatedAt string `dynamodbav:"updatedAt"`
}

type View struct {
	Active bool `json:"active"`
	Version int64 `json:"version"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	Code string `json:"code,omitempty"`
}

func key() map[string]types.AttributeValue { return map[string]types.AttributeValue{"pk":&types.AttributeValueMemberS{Value:"CONFIGURATION"},"sk":&types.AttributeValueMemberS{Value:"PAYMENT_MASTER_ACCESS"}} }

func Register(e *echo.Echo, app *functions.App, _ *zap.Logger) {
	functions.GetMasterAccessRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest)(events.APIGatewayV2HTTPResponse,error){return get(ctx,app,req)}))
	functions.RotateMasterAccessRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest)(events.APIGatewayV2HTTPResponse,error){return rotate(ctx,app,req)}))
	functions.RevokeMasterAccessRoute.Register(e, lambdautil.EchoAdapter(func(ctx context.Context, req events.APIGatewayV2HTTPRequest)(events.APIGatewayV2HTTPResponse,error){return revoke(ctx,app,req)}))
}

func isAdmin(req events.APIGatewayV2HTTPRequest) bool {
	if req.RequestContext.Authorizer == nil { return false }
	code, ok := req.RequestContext.Authorizer.Lambda["profileCode"].(string)
	code = strings.ToUpper(strings.TrimSpace(code))
	return ok && (code == "ADMIN" || code == "ADM")
}

func load(ctx context.Context, app *functions.App) (item, error) {
	out, err := app.DDB.GetItem(ctx,&dynamodb.GetItemInput{TableName:aws.String(app.Config.ConfigurationsTableName),Key:key(),ConsistentRead:aws.Bool(true)})
	if err != nil || len(out.Item)==0 { return item{}, err }
	var current item
	err=attributevalue.UnmarshalMap(out.Item,&current)
	return current,err
}

func Get(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
	app,err:=functions.GetApp(ctx); if err!=nil{return lambdautil.ErrorResponse(req,err)}
	return get(ctx,app,req)
}
func get(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
	if !isAdmin(req){return lambdautil.ErrorResponse(req,apperr.Forbidden("Solo administración puede consultar este acceso"))}
	current,err:=load(ctx,app); if err!=nil{return lambdautil.ErrorResponse(req,err)}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK,View{Active:current.Active,Version:current.Version,UpdatedAt:current.UpdatedAt},map[string]string{"cache-control":"no-store"})
}

func Rotate(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
	app,err:=functions.GetApp(ctx); if err!=nil{return lambdautil.ErrorResponse(req,err)}
	return rotate(ctx,app,req)
}
func rotate(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
	if !isAdmin(req){return lambdautil.ErrorResponse(req,apperr.Forbidden("Solo administración puede rotar este acceso"))}
	current,err:=load(ctx,app); if err!=nil{return lambdautil.ErrorResponse(req,err)}
	raw:=make([]byte,20); if _,err=rand.Read(raw);err!=nil{return lambdautil.ErrorResponse(req,err)}
	code:=make([]byte,len(raw)); for i,b:=range raw{code[i]=alphabet[int(b)%len(alphabet)]}
	digest:=sha256.Sum256(code); current=item{PK:"CONFIGURATION",SK:"PAYMENT_MASTER_ACCESS",Digest:hex.EncodeToString(digest[:]),Active:true,Version:current.Version+1,UpdatedAt:time.Now().UTC().Format(time.RFC3339)}
	encoded,err:=attributevalue.MarshalMap(current);if err!=nil{return lambdautil.ErrorResponse(req,err)}
	_,err=app.DDB.PutItem(ctx,&dynamodb.PutItemInput{TableName:aws.String(app.Config.ConfigurationsTableName),Item:encoded});if err!=nil{return lambdautil.ErrorResponse(req,err)}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK,View{Active:true,Version:current.Version,UpdatedAt:current.UpdatedAt,Code:string(code)},map[string]string{"cache-control":"no-store"})
}

func Revoke(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
	app,err:=functions.GetApp(ctx); if err!=nil{return lambdautil.ErrorResponse(req,err)}
	return revoke(ctx,app,req)
}
func revoke(ctx context.Context, app *functions.App, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse,error) {
	if !isAdmin(req){return lambdautil.ErrorResponse(req,apperr.Forbidden("Solo administración puede revocar este acceso"))}
	current,err:=load(ctx,app);if err!=nil{return lambdautil.ErrorResponse(req,err)}
	current.PK,current.SK,current.Digest,current.Active,current.Version,current.UpdatedAt="CONFIGURATION","PAYMENT_MASTER_ACCESS","",false,current.Version+1,time.Now().UTC().Format(time.RFC3339)
	encoded,err:=attributevalue.MarshalMap(current);if err!=nil{return lambdautil.ErrorResponse(req,err)}
	_,err=app.DDB.PutItem(ctx,&dynamodb.PutItemInput{TableName:aws.String(app.Config.ConfigurationsTableName),Item:encoded});if err!=nil{return lambdautil.ErrorResponse(req,err)}
	return lambdautil.SuccessResponseWithHeaders(http.StatusOK,View{Active:false,Version:current.Version,UpdatedAt:current.UpdatedAt},map[string]string{"cache-control":"no-store"})
}
