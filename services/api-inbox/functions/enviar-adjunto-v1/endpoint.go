package enviaradjuntov1

import (
	"context"
	"github.com/aws/aws-lambda-go/events"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/functions"
)

func Handle(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := functions.GetApp(ctx)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{}, err
	}
	return functions.SendAttachment(ctx, app.Attachment, request)
}
