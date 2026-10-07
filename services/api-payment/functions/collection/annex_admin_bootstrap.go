package collection

import (
	"context"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func annexAdminHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest, run func(AnnexAdminApp) (events.APIGatewayV2HTTPResponse, error)) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return run(AnnexAdminApp{Accounts: app.Accounts, LookupSecret: app.LookupSecret, Now: time.Now})
}

func CreateAnnexHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminAppWithLookup(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (AnnexAdminApp{Accounts: app.Accounts, LookupSecret: app.LookupSecret, Now: time.Now}).HandleCreateAnnex(ctx, req)
}

func GetAnnexHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return annexAdminHandler(ctx, req, func(app AnnexAdminApp) (events.APIGatewayV2HTTPResponse, error) { return app.HandleGetAnnex(ctx, req) })
}

func RosterHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return annexAdminHandler(ctx, req, func(app AnnexAdminApp) (events.APIGatewayV2HTTPResponse, error) { return app.HandleRoster(ctx, req) })
}

func RosterMigrationHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (RosterMigrationApp{Accounts: app.Accounts, Contracts: app.Contracts, Now: time.Now}).Handle(ctx, req)
}

func RosterClosureHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (RosterClosureApp{Accounts: app.Accounts, Now: time.Now}).Handle(ctx, req)
}

func GroupDepositHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (GroupDepositApp{Accounts: app.Accounts, Now: time.Now}).Handle(ctx, req)
}

func GroupDiscountHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	app, err := getAdminApp(ctx)
	if err != nil {
		return adminFailure(req, 502, "PAYMENT_SERVICE_UNAVAILABLE")
	}
	return (GroupDiscountApp{Accounts: app.Accounts, Now: time.Now}).Handle(ctx, req)
}

func PreviewAnnexHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return annexAdminHandler(ctx, req, func(app AnnexAdminApp) (events.APIGatewayV2HTTPResponse, error) {
		return app.HandlePreviewAnnex(ctx, req)
	})
}

func AnnexImpactHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return annexAdminHandler(ctx, req, func(app AnnexAdminApp) (events.APIGatewayV2HTTPResponse, error) {
		return app.HandleAnnexImpact(ctx, req)
	})
}

func ApplyAnnexHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return annexAdminHandler(ctx, req, func(app AnnexAdminApp) (events.APIGatewayV2HTTPResponse, error) {
		return app.HandleApplyAnnex(ctx, req)
	})
}
