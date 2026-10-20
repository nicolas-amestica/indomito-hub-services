//go:build devfinancial

package renditionobserve

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/oklog/ulid/v2"
	domain "ind-hub-api-gox-sls-pri-gh/services/api-payment/domain/collection"
	service "ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/collection"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/providers"
)

var confirmFinancialDEV = flag.String("dev-financial-confirm", "", "Hash exacto del reporte autorizado para prueba financiera DEV")

// TestFinancialDEV es una prueba manual con escrituras reales solo en DEV.
// No forma parte de go test ./... ni de ningún binario desplegable.
func TestFinancialDEV(t *testing.T) {
	const approvedHash = "94ad368e9765206caa396e74bcdfb08fa461b92d6f1648870f32453c773be35a"
	if *confirmFinancialDEV != approvedHash {
		t.Skip("requiere confirmación explícita del reporte DEV")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	check := func(err error, step string) {
		t.Helper()
		if err != nil {
			t.Fatalf("falló %s; detener y revisar sin imprimir datos sensibles", step)
		}
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile("pa-dev"), config.WithRegion("us-east-1"))
	check(err, "configuración")
	identity, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	check(err, "identidad")
	if aws.ToString(identity.Account) != DevAccount {
		t.Fatal("cuenta no autorizada")
	}
	param, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String("/indomito/dev/payments/khipu/renditions/dev-observation-config"), WithDecryption: aws.Bool(true)})
	check(err, "política")
	if param.Parameter == nil || param.Parameter.Type != "SecureString" {
		t.Fatal("política no segura")
	}
	var p Policy
	check(json.Unmarshal([]byte(aws.ToString(param.Parameter.Value)), &p), "lectura de política")
	check(p.validate("dev", DevAccount, time.Now()), "vigencia")
	if p.ReportSHA256 != approvedHash {
		t.Fatal("reporte no autorizado")
	}
	objects := s3.NewFromConfig(cfg)
	raw, err := readObject(ctx, objects, "unverified/slot-01.json")
	check(err, "captura")
	var captured struct {
		Body []byte `json:"bodyBase64"`
		Hash string `json:"sha256"`
	}
	check(json.Unmarshal(raw, &captured), "envoltorio")
	if captured.Hash != approvedHash {
		t.Fatal("captura diferente")
	}
	report, extras, err := verify(p, captured.Body, time.Now())
	check(err, "firma limitada DEV")
	if len(report.Items) != 3 {
		t.Fatal("se requieren exactamente tres pagos")
	}
	db := dynamodb.NewFromConfig(cfg)
	reader := awsReader{db: db}
	snapshots := make([]Snapshot, 3)
	var total int64
	for i, item := range report.Items {
		snapshots[i], err = reader.Read(ctx, item.AttemptID, item.PaymentID)
		check(err, "estado previo")
		s := snapshots[i]
		gross, e := providers.CLPInteger(item.Amount)
		check(e, "importe")
		fee, e := providers.CLPInteger(item.Fee)
		check(e, "comisión")
		delayed, e := providers.CLPInteger(extras[i].DelayedFee)
		check(e, "comisión diferida")
		if s.Attempt == nil || s.Account == nil || s.Event == nil || s.Settlement == nil || s.Resolved || s.Settlement.SettledGross != 0 || s.Settlement.ActualFees != 0 || s.Settlement.Amount != gross || s.Attempt.Amount != gross || s.Attempt.AccountID != s.Account.ID || s.Event.AccountID != s.Account.ID || s.Event.TripID != s.Account.TripID || s.Event.Type != "PAYMENT_RECEIVED" || s.Event.Reference != "khipu:"+item.PaymentID || s.Event.Amount != gross || s.Settlement.PaymentReference != s.Event.Reference || s.Settlement.TripID != s.Account.TripID || fee != 0 || delayed != 0 || item.FeeScheme != p.FeeScheme {
			t.Fatal("precondiciones financieras no satisfechas; no escribir")
		}
		total += gross
	}
	if total != 104245 {
		t.Fatal("total inesperado")
	}
	// Respaldo privado previo: nunca se imprime ni se incorpora al repositorio.
	backup, err := json.Marshal(snapshots)
	check(err, "serialización de respaldo")
	_, err = objects.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(evidenceBucket), Key: aws.String("unverified/financial-test/" + approvedHash + "/before.json"), Body: bytes.NewReader(backup), IfNoneMatch: aws.String("*"), ContentType: aws.String("application/json")})
	check(err, "respaldo único previo (si existe, revisar ejecución anterior)")
	svc := service.Service{DB: db, Table: "ind-dev-pagos-ddb-dev-pri-use1"}
	audits := make([]domain.Audit, 3)
	results := make([]domain.Settlement, 3)
	events := make([]domain.Event, 3)
	for i := range audits {
		audits[i] = domain.Audit{CommandID: ulid.Make().String(), Actor: "operator:dev-financial-test", Reason: "PRUEBA DEV SIMULADA; certificado no confiable; reporte " + approvedHash + "; fecha de prueba, no abono bancario", RecordedAt: time.Now().UTC()}
	}
	// Se conserva el manifiesto antes de escribir para inspeccionar una ejecución parcial.
	manifest, err := json.Marshal(audits)
	check(err, "manifiesto")
	_, err = objects.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(evidenceBucket), Key: aws.String("unverified/financial-test/" + approvedHash + "/commands.json"), Body: bytes.NewReader(manifest), IfNoneMatch: aws.String("*")})
	check(err, "respaldo de comandos")
	zone, err := time.LoadLocation("America/Santiago")
	check(err, "zona")
	date := time.Now().In(zone).Format(time.DateOnly)
	for pass := 0; pass < 2; pass++ {
		for i, item := range report.Items {
			check(p.validate("dev", DevAccount, time.Now()), "vigencia antes de escritura")
			s := snapshots[i]
			guard := types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{TableName: &svc.Table, Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "ATTEMPT#" + item.AttemptID}, "sk": &types.AttributeValueMemberS{Value: "RESOLUTION"}}, ConditionExpression: aws.String("attribute_not_exists(pk)")}}
			next, event, e := svc.ReconcileSettlement(ctx, s.Account.TripID, s.Settlement.PaymentReference, s.Settlement.Version, s.Settlement.Amount, 0, "dev-test:"+approvedHash+":"+item.PaymentID, date, audits[i], guard)
			check(e, "conciliación controlada")
			if next.SettledGross != s.Settlement.Amount || next.Version != s.Settlement.Version+1 || next.ActualFees != 0 {
				t.Fatal("resultado financiero inesperado")
			}
			if pass == 0 {
				results[i], events[i] = next, event
			} else if !reflect.DeepEqual(next, results[i]) || !reflect.DeepEqual(event, events[i]) {
				t.Fatal("replay no idempotente")
			}
		}
	}
	for i, item := range report.Items {
		after, e := reader.Read(ctx, item.AttemptID, item.PaymentID)
		check(e, "lectura final")
		if !reflect.DeepEqual(after.Settlement, &results[i]) || !reflect.DeepEqual(after.Account, snapshots[i].Account) || !reflect.DeepEqual(after.Event, snapshots[i].Event) || after.Resolved {
			t.Fatal("estado final inesperado")
		}
		for _, k := range [][2]string{
			{"COMMAND#" + audits[i].CommandID, "META"},
			{"TRIP#" + after.Account.TripID, "TREASURY_EVENT#" + audits[i].CommandID},
			{"TRIP#" + after.Account.TripID, "CASH#" + date + "#" + audits[i].CommandID},
			{"CASH#" + date[:7], "TRIP#" + after.Account.TripID + "#" + date + "#" + audits[i].CommandID},
		} {
			r, found, readErr := reader.get(ctx, k[0], k[1])
			check(readErr, "proyecciones de caja")
			if !found || !reflect.DeepEqual(r.Event, &events[i]) {
				t.Fatal("proyección de caja o auditoría inconsistente")
			}
		}
	}
	result, err := json.Marshal(map[string]any{"trust": TrustLabel, "mode": "DEV_FINANCIAL_TEST", "payments": 3, "gross": total, "replayVerified": true, "participantAccountsUnchanged": true, "effectiveDate": date})
	check(err, "resumen")
	_, err = objects.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(evidenceBucket), Key: aws.String("unverified/financial-test/" + approvedHash + "/result.json"), Body: bytes.NewReader(result), IfNoneMatch: aws.String("*")})
	check(err, "evidencia final")
	t.Log("DEV: tres liquidaciones por CLP 104245; replay idéntico; cuentas de pasajeros sin cambios; certificado continúa no confiable")
}
