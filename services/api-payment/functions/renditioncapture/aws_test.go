package renditioncapture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/lambdacontext"
)

// TestAWSAdapter usa endpoints locales; ninguna credencial ni llamada real AWS.
func TestAWSAdapter(t *testing.T) {
	for _, scenario := range []string{"capture", "duplicate", "collision", "plain parameter", "wrong bucket", "invalid config"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, req := fixture()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Amz-Target") == "AmazonSSM.GetParameter" {
					value, err := json.Marshal(captureConfig{Bucket: "ind-khipu-rendition-capture-dev-382670112717", Until: time.Now().Add(time.Hour)})
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					kind := "SecureString"
					switch scenario {
					case "plain parameter":
						kind = "String"
					case "wrong bucket":
						value = []byte(`{"bucket":"unexpected"}`)
					case "invalid config":
						value = []byte(`invalid`)
					}
					w.Header().Set("Content-Type", "application/x-amz-json-1.1")
					if err = json.NewEncoder(w).Encode(map[string]any{"Parameter": map[string]string{"Type": kind, "Value": string(value)}}); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method == "HEAD" {
					if scenario == "duplicate" {
						w.Header().Set("x-amz-meta-sha256", "different-existing-hash")
						return
					}
					w.WriteHeader(404)
					return
				}
				if r.Method == "PUT" {
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("missing conditional write")
					}
					if scenario == "collision" {
						w.Header().Set("Content-Type", "application/xml")
						w.WriteHeader(412)
						if _, err := w.Write([]byte(`<Error><Code>PreconditionFailed</Code></Error>`)); err != nil {
							t.Error(err)
						}
						return
					}
					w.WriteHeader(200)
					return
				}
				w.WriteHeader(400)
			}))
			defer server.Close()
			t.Setenv("AWS_REGION", "us-east-1")
			t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-capture-fixture")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-capture-fixture")
			t.Setenv("AWS_SESSION_TOKEN", "")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_ENDPOINT_URL_SSM", server.URL)
			t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
			ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{InvokedFunctionArn: "arn:aws:lambda:us-east-1:382670112717:function:ind-khipu-rendition-capture-dev"})
			got, err := Handler(ctx, req)
			want := 503
			if scenario == "capture" {
				want = 200
			}
			if scenario == "duplicate" || scenario == "collision" {
				want = 429
			}
			if err != nil || got.StatusCode != want {
				t.Fatalf("got %d, want %d, err %v", got.StatusCode, want, err)
			}
		})
	}
}
