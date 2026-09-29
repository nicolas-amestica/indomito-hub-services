package providers

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeParameters struct {
	t     *testing.T
	plain bool
	calls int
}

func (f *fakeParameters) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls++
	if !aws.ToBool(in.WithDecryption) || !strings.HasPrefix(aws.ToString(in.Name), "/indomito/dev/payments/khipu/") {
		f.t.Fatal("invalid SSM request")
	}
	value := "synthetic-test-secret"
	if strings.HasSuffix(aws.ToString(in.Name), "receiver-id") {
		value = "123"
	}
	kind := types.ParameterTypeSecureString
	if f.plain {
		kind = types.ParameterTypeString
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Type: kind, Value: aws.String(value)}}, nil
}
func TestLoadDevelopmentSecrets(t *testing.T) {
	f := &fakeParameters{t: t}
	result, err := LoadKhipuDevelopment(context.Background(), f, "dev")
	if err != nil || result.Client == nil || result.WebhookSecret == "" || f.calls != 3 {
		t.Fatal("DEV configuration not loaded")
	}
	f.calls = 0
	if _, err = LoadKhipuDevelopment(context.Background(), f, "prd"); err == nil || f.calls != 0 {
		t.Fatal("production accepted")
	}
	f.plain = true
	if _, err = LoadKhipuDevelopment(context.Background(), f, "dev"); err == nil {
		t.Fatal("plaintext parameter accepted")
	}
}
