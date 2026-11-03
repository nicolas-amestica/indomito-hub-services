package collection

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSIIParameters struct {
	values map[string]string
}

func (f *fakeSIIParameters) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := aws.ToString(input.Name)
	value, ok := f.values[name]
	if !ok {
		return nil, context.Canceled
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: &name, Value: &value}}, nil
}

func (f *fakeSIIParameters) GetParameters(_ context.Context, input *ssm.GetParametersInput, _ ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	output := &ssm.GetParametersOutput{}
	for _, name := range input.Names {
		value, ok := f.values[name]
		if !ok {
			output.InvalidParameters = append(output.InvalidParameters, name)
			continue
		}
		nameCopy, valueCopy := name, value
		output.Parameters = append(output.Parameters, ssmtypes.Parameter{Name: &nameCopy, Value: &valueCopy})
	}
	return output, nil
}

func (f *fakeSIIParameters) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if input.Type != ssmtypes.ParameterTypeSecureString || input.Tier != ssmtypes.ParameterTierStandard {
		return nil, context.Canceled
	}
	f.values[aws.ToString(input.Name)] = aws.ToString(input.Value)
	return &ssm.PutParameterOutput{}, nil
}

func TestSIIParameterStoreRoundTripLargeValue(t *testing.T) {
	t.Parallel()
	client := &fakeSIIParameters{values: map[string]string{}}
	store := siiParameterStore{Client: client, Stage: "dev"}
	raw := []byte(strings.Repeat("contenido-no-comprimible-", 700))
	if err := store.Save(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded) != string(raw) {
		t.Fatal("la configuración reconstruida difiere del original")
	}
	if len(client.values) < 3 {
		t.Fatalf("se esperaban fragmentos Standard, se guardaron %d parámetros", len(client.values))
	}
}

func TestSIIParameterStoreRejectsCorruptedPart(t *testing.T) {
	t.Parallel()
	client := &fakeSIIParameters{values: map[string]string{}}
	store := siiParameterStore{Client: client, Stage: "dev"}
	if err := store.Save(context.Background(), []byte(strings.Repeat("x", 5000))); err != nil {
		t.Fatal(err)
	}
	for name := range client.values {
		if strings.Contains(name, "/part-") {
			client.values[name] += "corrupto"
			break
		}
	}
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("se esperaba error de integridad")
	}
}
