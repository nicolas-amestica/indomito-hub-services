package collection

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/dteinbox"
)

type syncRequestFake struct {
	claimed bool
	calls   int
	err     error
	failed  bool
}

func (s *syncRequestFake) State(context.Context) (dteinbox.SyncState, error) {
	return dteinbox.SyncState{LastSuccess: "2026-11-09T12:00:00Z"}, nil
}
func (s *syncRequestFake) BeginSyncRequest(_ context.Context, _ string, id string, _ time.Time) (*dteinbox.SyncRequest, bool, error) {
	return &dteinbox.SyncRequest{ID: id, Status: "PENDING"}, s.claimed, nil
}
func (s *syncRequestFake) TransitionSyncRequest(_ context.Context, job dteinbox.SyncRequest, status, _ string, _ time.Time) (*dteinbox.SyncRequest, error) {
	s.failed = status == "FAILED"
	job.Status = status
	return &job, nil
}
func (s *syncRequestFake) Invoke(_ context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	s.calls++
	var event ReceivedSyncEvent
	if json.Unmarshal(in.Payload, &event) != nil || !validPortalID(event.RequestID) {
		return nil, errors.New("missing request identifier")
	}
	if *in.FunctionName != "api-payment-dev-fn-sincronizar-dte-recibidos-v1" || in.InvocationType != types.InvocationTypeEvent {
		return nil, errors.New("unsafe invocation")
	}
	return &lambda.InvokeOutput{StatusCode: 202}, s.err
}

func TestManualSyncAuthorizationCooldownAndAsyncFailure(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		claimed, passenger, failure bool
		status, calls               int
	}{
		{"admin queues", true, false, false, 202, 1},
		{"cooldown does not queue again", false, false, false, 202, 0},
		{"passenger denied before invocation", true, true, false, 403, 0},
		{"invoke failure is not success", true, false, true, 502, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := adminRequest("POST", "")
			if tc.passenger {
				req.RequestContext.Authorizer.Lambda["paymentAccess"] = "passenger"
			}
			fake := &syncRequestFake{claimed: tc.claimed}
			if tc.failure {
				fake.err = errors.New("unavailable")
			}
			out, err := requestReceivedSync(context.Background(), req, fake, fake, "dev", time.Now())
			if err != nil || out.StatusCode != tc.status || fake.calls != tc.calls {
				t.Fatalf("status=%d calls=%d err=%v", out.StatusCode, fake.calls, err)
			}
			if fake.failed != tc.failure {
				t.Fatal("invoke failure was not persisted")
			}
		})
	}
}
