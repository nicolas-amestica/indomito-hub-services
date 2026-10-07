package functions

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"ind-hub-api-gox-sls-pri-gh/services/api-contract/domain"
)

func accessFixture() PaymentAccessConfig {
	return PaymentAccessConfig{URL: "https://pagos.example.test", LookupSecret: strings.Repeat("synthetic-only-", 4)}
}

func TestTripCodeUsesUnambiguousAlphabetWithoutBias(t *testing.T) {
	code, err := newTripCode(bytes.NewReader([]byte{0, 1, 2, 3, 4, 5}))
	if err != nil || code != "234567" {
		t.Fatalf("unexpected code %q: %v", code, err)
	}
	if _, err = newTripCode(bytes.NewReader(nil)); err == nil {
		t.Fatal("entropy failure ignored")
	}
	counts := map[byte]int{}
	for n := 0; n < 256; n++ {
		sample, sampleErr := newTripCode(bytes.NewReader(bytes.Repeat([]byte{byte(n)}, 6)))
		if sampleErr != nil {
			t.Fatal(sampleErr)
		}
		counts[sample[0]]++
	}
	if len(counts) != 32 {
		t.Fatal("alphabet entropy changed")
	}
	for _, count := range counts {
		if count != 8 {
			t.Fatal("biased byte mapping")
		}
	}
	portal, err := issuePaymentPortal(accessFixture())
	if err != nil || len(portal.TripCode) != 6 {
		t.Fatal("failed secure code generation")
	}
}

func TestPaymentApprovalWritesReserveCodeAndPendingSetupWithoutMoney(t *testing.T) {
	config := accessFixture()
	item := domain.Item{Contract: domain.Contract{ID: "trip", Version: 3, Status: domain.StatusApproved, Content: domain.Content{PaymentPortal: &domain.PaymentPortal{URL: config.URL, TripCode: "234567"}}, PDFDocument: &domain.PDFDocument{SHA256: "digest"}, ApprovedBy: "operator", ApprovedAt: "2026-09-30T00:00:00Z"}}
	writes, err := paymentApprovalWrites("payments", item, config)
	if err != nil || len(writes) != 2 {
		t.Fatalf("writes: %v", err)
	}
	for _, write := range writes {
		if *write.Put.ConditionExpression != "attribute_not_exists(pk)" || *write.Put.TableName != "payments" {
			t.Fatal("missing uniqueness guard")
		}
		if _, exists := write.Put.Item["expiresAt"]; exists {
			t.Fatal("financial record has TTL")
		}
		if _, exists := write.Put.Item["account"]; exists {
			t.Fatal("approval must not create accounts")
		}
	}
	key := writes[0].Put.Item["pk"].(*types.AttributeValueMemberS).Value
	if !strings.HasPrefix(key, "CODE#") || strings.Contains(key, "234567") {
		t.Fatal("code stored in clear index")
	}
	if writes[1].Put.Item["status"].(*types.AttributeValueMemberS).Value != "PENDING_SETUP" {
		t.Fatal("approval enabled collection")
	}
	config.LookupSecret = strings.Repeat("different-key-", 4)
	other, err := paymentApprovalWrites("payments", item, config)
	if err != nil || other[0].Put.Item["pk"].(*types.AttributeValueMemberS).Value == key {
		t.Fatal("lookup must be keyed")
	}
}

func TestPaymentAccessRejectsUnsafeConfiguration(t *testing.T) {
	for _, uri := range []string{"http://payments.test", "https://user:password@payments.test", "https://payments.test/?code=abc", "https://payments.test/#code", ""} {
		t.Run(uri, func(t *testing.T) {
			c := accessFixture()
			c.URL = uri
			if _, err := issuePaymentPortal(c); err == nil {
				t.Fatal("invalid URL accepted")
			}
		})
	}
	c := accessFixture()
	c.LookupSecret = "short"
	if _, err := issuePaymentPortal(c); err == nil {
		t.Fatal("weak lookup secret accepted")
	}
	if _, err := paymentApprovalWrites("", domain.Item{}, c); err == nil {
		t.Fatal("incomplete approval accepted")
	}
}

type parameterFake struct{ fail bool }

func (f parameterFake) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.fail {
		return nil, errors.New("sensitive upstream detail")
	}
	if !aws.ToBool(in.WithDecryption) {
		return nil, errors.New("decryption required")
	}
	value := accessFixture().LookupSecret
	if strings.HasSuffix(*in.Name, "portal-url") {
		value = accessFixture().URL
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: &value}}, nil
}
func TestPaymentAccessLoadsFromSSMWithoutLeakingErrors(t *testing.T) {
	c, err := loadPaymentAccess(context.Background(), parameterFake{}, "dev")
	if err != nil || c != accessFixture() {
		t.Fatal("SSM configuration not loaded")
	}
	_, err = loadPaymentAccess(context.Background(), parameterFake{fail: true}, "dev")
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("upstream error exposed")
	}
}

func TestPaymentInstructionsOnlyWhenIssuedByServer(t *testing.T) {
	c := domain.Content{}
	if strings.Contains(buildClauses(c)[16].text, "código del viaje") {
		t.Fatal("historical document changed")
	}
	c.PaymentPortal = &domain.PaymentPortal{URL: "https://pagos.example.test", TripCode: "234567"}
	text := buildClauses(c)[16].text
	for _, value := range []string{c.PaymentPortal.URL, "234567", "su RUT", "puesta en marcha", "no constituye una boleta"} {
		if !strings.Contains(text, value) {
			t.Fatalf("missing %s", value)
		}
	}
	if !strings.Contains(buildClauses(c)[21].text, "registrar y actualizar") {
		t.Fatal("historical roster clause changed")
	}
	domain.UseCurrentTerms(&c)
	if text := buildClauses(c)[21].text; !strings.Contains(text, "anexo aprobado") || !strings.Contains(text, "no habilita modificaciones") {
		t.Fatal("new roster clause contradicts annex workflow")
	}
}
