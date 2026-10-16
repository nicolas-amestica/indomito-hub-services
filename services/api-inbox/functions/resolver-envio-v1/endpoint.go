package resolverenviov1

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/libs/lambdautil"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func Handle(ctx context.Context, r events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	a, e := functions.GetApp(ctx)
	if e != nil {
		return lambdautil.ErrorResponse(r, e)
	}
	return functions.ResolveSend(ctx, a.Send, r)
}
