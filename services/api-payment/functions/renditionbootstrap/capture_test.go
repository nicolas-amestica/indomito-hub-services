package renditionbootstrap

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

type captureStore struct {
	calls int
	body  []byte
	key   string
	err   error
}

func (s *captureStore) Save(_ context.Context, key, _ string, body []byte) error {
	s.calls++
	s.body, s.key = body, key
	return s.err
}

func signedFixture(t *testing.T, now time.Time) ([]byte, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	report := providers.Rendition{ID: 1, RecipientUID: "unknown-recipient", Bank: "unknown-bank", Account: "unknown-account",
		StartDate: now.Format(time.RFC3339), EndDate: now.Format(time.RFC3339),
		Items: []providers.RenditionItem{{PaymentID: "abcdefghijkl", AttemptID: "attempt", PaymentDate: now.Format(time.RFC3339), Amount: "100", Fee: "0"}}}
	payload, err := json.Marshal(map[string]any{"iat": now.Unix(), "report": report})
	if err != nil {
		t.Fatal(err)
	}
	message := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS512"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha512.Sum512([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA512, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err = writer.Write([]byte(message + "." + base64.RawURLEncoding.EncodeToString(signature))); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), cert
}

func TestCapture(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	raw, cert := signedFixture(t, now)
	for _, tc := range []struct {
		name   string
		change func(*App, *events.APIGatewayV2HTTPRequest, *captureStore)
		status int
		saves  int
	}{
		{"valid unknown destination", func(*App, *events.APIGatewayV2HTTPRequest, *captureStore) {}, 200, 1},
		{"storage failed", func(_ *App, _ *events.APIGatewayV2HTTPRequest, s *captureStore) { s.err = errors.New("failed") }, 503, 1},
		{"disabled", func(a *App, _ *events.APIGatewayV2HTTPRequest, _ *captureStore) { a.Enabled = false }, 503, 0},
		{"expired window", func(a *App, _ *events.APIGatewayV2HTTPRequest, _ *captureStore) { a.Until = now }, 503, 0},
		{"long window", func(a *App, _ *events.APIGatewayV2HTTPRequest, _ *captureStore) { a.Until = now.Add(73 * time.Hour) }, 503, 0},
		{"untrusted", func(a *App, _ *events.APIGatewayV2HTTPRequest, _ *captureStore) { a.Verifier.Pinned = nil }, 400, 0},
		{"expired cert", func(a *App, _ *events.APIGatewayV2HTTPRequest, _ *captureStore) {
			a.Now = func() time.Time { return now.Add(2 * time.Hour) }
		}, 400, 0},
		{"malformed", func(_ *App, r *events.APIGatewayV2HTTPRequest, _ *captureStore) { r.Body = "!" }, 400, 0},
		{"oversize", func(_ *App, r *events.APIGatewayV2HTTPRequest, _ *captureStore) {
			r.Body = strings.Repeat("x", 6*1024*1024)
		}, 413, 0},
		{"wrong method", func(_ *App, r *events.APIGatewayV2HTTPRequest, _ *captureStore) { r.RequestContext.HTTP.Method = "GET" }, 405, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &captureStore{}
			a := App{Store: store, Verifier: providers.RenditionCaptureVerifier{Pinned: cert}, Enabled: true, Until: now.Add(24 * time.Hour), Now: func() time.Time { return now }}
			req := events.APIGatewayV2HTTPRequest{Body: base64.StdEncoding.EncodeToString(raw), IsBase64Encoded: true}
			req.RequestContext.HTTP.Method = "POST"
			tc.change(&a, &req, store)
			result := a.Handle(context.Background(), req)
			if result.StatusCode != tc.status || store.calls != tc.saves {
				t.Fatalf("status=%d saves=%d", result.StatusCode, store.calls)
			}
			if result.StatusCode == 200 {
				var saved evidence
				if json.Unmarshal(store.body, &saved) != nil || saved.Trust != "AUTHENTICATED_DESTINATION_UNVERIFIED" ||
					!bytes.Equal(saved.Body, raw) || saved.Report.RecipientUID != "unknown-recipient" ||
					!strings.HasPrefix(store.key, "rendition-bootstrap/prd/") {
					t.Fatal("invalid private evidence")
				}
				if strings.Contains(result.Body, "unknown-recipient") {
					t.Fatal("public response leaks destination")
				}
			}
		})
	}
}

type fakeObjects struct {
	putErr  error
	headErr error
	digest  string
	input   *s3.PutObjectInput
}

func (f *fakeObjects) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.input = input
	return &s3.PutObjectOutput{}, f.putErr
}
func (f *fakeObjects) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return &s3.HeadObjectOutput{Metadata: map[string]string{"sha256": f.digest}}, f.headErr
}

func TestImmutableStorage(t *testing.T) {
	occupied := &smithy.GenericAPIError{Code: "PreconditionFailed"}
	for _, tc := range []struct {
		name   string
		client fakeObjects
		ok     bool
	}{
		{"first", fakeObjects{}, true},
		{"duplicate", fakeObjects{putErr: occupied, digest: "same"}, true},
		{"other report", fakeObjects{putErr: occupied, digest: "other"}, false},
		{"head failure", fakeObjects{putErr: occupied, headErr: errors.New("failed")}, false},
		{"put failure", fakeObjects{putErr: errors.New("failed")}, false},
		{"concurrent conflict retry", fakeObjects{putErr: &smithy.GenericAPIError{Code: "ConditionalRequestConflict"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (objects{client: &tc.client, bucket: "private"}).Save(context.Background(), "key", "same", []byte("evidence"))
			if (err == nil) != tc.ok {
				t.Fatal("unexpected storage result", err)
			}
			if aws.ToString(tc.client.input.IfNoneMatch) != "*" || tc.client.input.ServerSideEncryption != "AES256" {
				t.Fatal("missing immutability or encryption")
			}
		})
	}
}

func TestEnvironmentGuard(t *testing.T) {
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", "api-payment-prd-fn-capturar-rendicion-khipu-v1")
	for _, tc := range []struct {
		stage, account, region string
		want                   bool
	}{
		{"prd", "576394965493", "us-east-1", true},
		{"dev", "576394965493", "us-east-1", false},
		{"prd", "382670112717", "us-east-1", false},
		{"prd", "576394965493", "us-west-2", false},
	} {
		t.Setenv("APP_STAGE", tc.stage)
		ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{
			InvokedFunctionArn: "arn:aws:lambda:" + tc.region + ":" + tc.account + ":function:api-payment-prd-fn-capturar-rendicion-khipu-v1",
		})
		if allowed(ctx) != tc.want {
			t.Fatal("invalid environment guard")
		}
	}
	if allowed(context.Background()) {
		t.Fatal("missing Lambda context accepted")
	}
}
