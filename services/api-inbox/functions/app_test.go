package functions

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeParameterReader struct {
	values map[string]string
	kind   ssmtypes.ParameterType
}

func (fake fakeParameterReader) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	value, exists := fake.values[aws.ToString(input.Name)]
	if !exists {
		return nil, errors.New("not found")
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Type: fake.kind, Value: &value}}, nil
}

func TestLoadInboxParameters(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	reader := fakeParameterReader{kind: ssmtypes.ParameterTypeSecureString, values: map[string]string{
		"cursor":  `{"current":"` + secret + `","previous":null,"rotatedAt":"2026-10-14T12:00:00Z"}`,
		"runtime": `{"maxMaterializableConversations":20000,"dueQueueShards":16,"workerQueryLimit":100}`,
	}}
	decoded, runtime, err := loadInboxParameters(context.Background(), reader, "cursor", "runtime")
	if err != nil || string(decoded.Current) != "01234567890123456789012345678901" || runtime.MaxMaterializableConversations != 20_000 {
		t.Fatalf("secret = %q, runtime = %#v, error = %v", decoded, runtime, err)
	}
}

func TestLoadInboxParametersRejectsInvalidConfiguration(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	tests := []struct {
		name    string
		kind    ssmtypes.ParameterType
		cursor  string
		runtime string
	}{
		{name: "parameter is not secure", kind: ssmtypes.ParameterTypeString, cursor: `{}`, runtime: `{}`},
		{name: "unknown cursor field", kind: ssmtypes.ParameterTypeSecureString, cursor: `{"current":"` + secret + `","previous":null,"rotatedAt":"2026-10-14T12:00:00Z","extra":true}`, runtime: `{}`},
		{name: "invalid runtime limits", kind: ssmtypes.ParameterTypeSecureString, cursor: `{"current":"` + secret + `","previous":null,"rotatedAt":"2026-10-14T12:00:00Z"}`, runtime: `{"maxMaterializableConversations":19999,"dueQueueShards":16,"workerQueryLimit":100}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := fakeParameterReader{kind: test.kind, values: map[string]string{"cursor": test.cursor, "runtime": test.runtime}}
			if _, _, err := loadInboxParameters(context.Background(), reader, "cursor", "runtime"); err == nil {
				t.Fatal("expected invalid configuration error")
			}
		})
	}
}
