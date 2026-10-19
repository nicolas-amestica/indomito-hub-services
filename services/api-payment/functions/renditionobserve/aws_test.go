package renditionobserve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type emptyDatabase struct{ fail bool }

func (d emptyDatabase) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if d.fail {
		return nil, errors.New("unavailable")
	}
	return &dynamodb.GetItemOutput{}, nil
}
func TestAWSReaderMissingAndUnavailable(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, err := (awsReader{db: emptyDatabase{fail: fail}}).Read(context.Background(), "attempt", "testpay00000")
		if (err != nil) != fail || s.Attempt != nil {
			t.Fatal(s, err)
		}
	}
}

func toWire(t *testing.T, value types.AttributeValue) any {
	t.Helper()
	switch v := value.(type) {
	case *types.AttributeValueMemberS:
		return map[string]any{"S": v.Value}
	case *types.AttributeValueMemberN:
		return map[string]any{"N": v.Value}
	case *types.AttributeValueMemberBOOL:
		return map[string]any{"BOOL": v.Value}
	case *types.AttributeValueMemberNULL:
		return map[string]any{"NULL": v.Value}
	case *types.AttributeValueMemberL:
		list := []any{}
		for _, x := range v.Value {
			list = append(list, toWire(t, x))
		}
		return map[string]any{"L": list}
	case *types.AttributeValueMemberM:
		m := map[string]any{}
		for k, x := range v.Value {
			m[k] = toWire(t, x)
		}
		return map[string]any{"M": m}
	default:
		t.Fatalf("unsupported fixture attribute %T", value)
		return nil
	}
}

// TestRunDEVWithLocalAWSAdapters no realiza solicitudes reales ni usa credenciales
// del usuario. Comprueba el flujo completo y rechaza cualquier escritura DynamoDB.
func TestRunDEVWithLocalAWSAdapters(t *testing.T) {
	f := newFixture(t)
	for _, scenario := range []string{"success", "wrong-account", "plain-parameter", "wrong-pin"} {
		t.Run(scenario, func(t *testing.T) {
			p := f.p
			if scenario == "wrong-pin" {
				p.CertificateSHA256 = digest([]byte("different certificate"))
			}
			writes := 0
			var saved []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch target := r.Header.Get("X-Amz-Target"); target {
				case "AmazonSSM.GetParameter":
					value, err := json.Marshal(p)
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					kind := "SecureString"
					if scenario == "plain-parameter" {
						kind = "String"
					}
					w.Header().Set("Content-Type", "application/x-amz-json-1.1")
					if err = json.NewEncoder(w).Encode(map[string]any{"Parameter": map[string]string{"Type": kind, "Value": string(value)}}); err != nil {
						t.Error(err)
					}
					return
				case "DynamoDB_20120810.GetItem":
					var input struct{ Key map[string]struct{ S string } }
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					pk, sk := input.Key["pk"].S, input.Key["sk"].S
					var item row
					s, _ := (&memoryReader{}).Read(r.Context(), strings.TrimPrefix(pk, "ATTEMPT#"), strings.TrimPrefix(sk, "SETTLEMENT#khipu:"))
					switch {
					case strings.HasPrefix(pk, "ATTEMPT#") && sk == "META":
						item.Attempt = s.Attempt
					case strings.HasPrefix(pk, "ATTEMPT#") && sk == "OUTCOME":
						idx := strings.TrimPrefix(pk, "ATTEMPT#attempt-")
						s.Event.Reference = "khipu:testpay0000" + idx
						item.Event = s.Event
					case strings.HasPrefix(pk, "ACCOUNT#"):
						item.Account = s.Account
					case strings.HasPrefix(pk, "TRIP#"):
						item.Settlement = s.Settlement
					case sk == "RESOLUTION":
						if err := json.NewEncoder(w).Encode(map[string]any{}); err != nil {
							t.Error(err)
						}
						return
					default:
						t.Error("unexpected key")
						w.WriteHeader(400)
						return
					}
					attrs, err := attributevalue.MarshalMap(item)
					if err != nil {
						t.Error(err)
						return
					}
					// SDK AttributeValue implementations are not DynamoDB wire JSON.
					wire := map[string]any{}
					for key, value := range attrs {
						wire[key] = toWire(t, value)
					}
					if err = json.NewEncoder(w).Encode(map[string]any{"Item": wire}); err != nil {
						t.Error(err)
					}
					return
				default:
					if strings.HasPrefix(target, "DynamoDB_") {
						t.Error("unexpected DynamoDB operation")
						w.WriteHeader(400)
						return
					}
				}
				if r.URL.Path == "/" {
					account := DevAccount
					if scenario == "wrong-account" {
						account = "other"
					}
					w.Header().Set("Content-Type", "text/xml")
					if _, err := io.WriteString(w, `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>`+account+`</Account><Arn>arn:aws:iam::`+account+`:user/test</Arn><UserId>test</UserId></GetCallerIdentityResult></GetCallerIdentityResponse>`); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/slot-01.json") {
					if err := json.NewEncoder(w).Encode(map[string]any{"bodyBase64": f.raw, "sha256": f.p.ReportSHA256}); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method == "PUT" {
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("missing conditional write")
					}
					if saved != nil {
						w.Header().Set("Content-Type", "application/xml")
						w.WriteHeader(412)
						if _, err := io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`); err != nil {
							t.Error(err)
						}
						return
					}
					var err error
					saved, err = io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					writes++
					return
				}
				if r.Method == "GET" && saved != nil {
					if _, err := w.Write(saved); err != nil {
						t.Error(err)
					}
					return
				}
				w.WriteHeader(400)
			}))
			defer server.Close()
			dir := t.TempDir()
			creds := filepath.Join(dir, "credentials")
			conf := filepath.Join(dir, "config")
			if err := os.WriteFile(creds, []byte("[pa-dev]\naws_access_key_id=synthetic-observe-fixture\naws_secret_access_key=synthetic-observe-fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(conf, []byte("[profile pa-dev]\nregion=us-east-1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", creds)
			t.Setenv("AWS_CONFIG_FILE", conf)
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "WHEN_REQUIRED")
			for _, service := range []string{"STS", "SSM", "S3", "DYNAMODB"} {
				t.Setenv("AWS_ENDPOINT_URL_"+service, server.URL)
			}
			result, err := RunDEV(context.Background())
			if scenario != "success" {
				if err == nil || writes != 0 {
					t.Fatal("guard failed", result, err)
				}
				return
			}
			if err != nil || result.Matched != 3 || result.CashWrites != 0 || writes != 1 {
				t.Fatal(result, err, writes)
			}
			again, err := RunDEV(context.Background())
			if err != nil || !again.Duplicate || writes != 1 {
				t.Fatal(again, err, writes)
			}
		})
	}
}
