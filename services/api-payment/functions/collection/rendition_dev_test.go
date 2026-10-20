package collection

import (
	"context"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"strings"
	"testing"
	"time"
)

func TestDevRenditionRuntimeIsolation(t *testing.T) {
	for _, tc := range []struct{ arn, role string }{
		{devRenditionARNPrefix + "ind-khipu-rendition-capture-dev", "receiver"},
		{devRenditionARNPrefix + "ind-khipu-rendition-worker-dev:$LATEST", "worker"},
		{devRenditionARNPrefix + "ind-khipu-rendition-worker-dev-evil", ""},
		{"arn:aws:lambda:us-east-1:000000000000:function:ind-khipu-rendition-worker-dev", ""},
		{"arn:aws:lambda:us-west-2:382670112717:function:ind-khipu-rendition-worker-dev", ""},
		{devRenditionARNPrefix + "production", ""},
	} {
		t.Run(tc.arn, func(t *testing.T) {
			ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{InvokedFunctionArn: tc.arn})
			if devRenditionRole(ctx) != tc.role {
				t.Fatal("runtime isolation failed")
			}
		})
	}
	if _, err := DevRenditionHandler(context.Background(), nil); err == nil {
		t.Fatal("non-Lambda context accepted")
	}
}

func TestDevRenditionSimulationAuditAndReplay(t *testing.T) {
	s, _, objects, report, now := renditionFixture(t, 1)
	policy := testRenditionApplyPolicy()
	policy.DevelopmentSimulation = true
	report.Items[0].Fee = "0"
	if _, err := s.ReceiveRendition(context.Background(), objects, "prd", report, []byte("signed"), policy, now); err == nil {
		t.Fatal("development policy allowed in production")
	}
	id, err := s.ReceiveRendition(context.Background(), objects, "dev", report, []byte("signed"), policy, now)
	if err != nil {
		t.Fatal(err)
	}
	clock := now.Add(time.Second)
	app := RenditionApp{Accounts: s, Objects: objects, Policy: policy, Stage: "dev", Now: func() time.Time { return clock }}
	if err = app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	commandID := paymentOperationID("rendition-item:" + id + ":" + report.Items[0].PaymentID)
	row, err := s.read(context.Background(), "COMMAND#"+commandID, "META")
	if err != nil {
		t.Fatal(err)
	}
	if row.Event == nil || row.Event.Actor != "system:dev-rendition-simulation" || !strings.Contains(row.Event.Reason, "NO autenticado") {
		t.Fatal("simulation audit missing")
	}
}
