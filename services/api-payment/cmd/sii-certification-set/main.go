package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"ind-hub-api-gox-sls-pri-gh/services/api-payment/functions/sii"
)

func main() {
	step := flag.String("step", "all", "envio a ejecutar: all, dte o rcof")
	execute := flag.Bool("execute", false, "confirma los envios reales al SII")
	flag.Parse()
	if !*execute || (*step != "all" && *step != "dte" && *step != "rcof") {
		fmt.Fprintln(os.Stderr, "uso: go run ./services/api-payment/cmd/sii-certification-set --execute [--step all|dte|rcof]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	check(err)
	secret, err := secretsmanager.NewFromConfig(awsConfig).GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String("indomito/dev/payments/sii/boleta-config")})
	check(err)
	if secret.SecretString == nil {
		check(fmt.Errorf("configuracion SII sin contenido"))
	}
	now := time.Now()
	credentials, err := sii.ParseConfig([]byte(aws.ToString(secret.SecretString)), now)
	check(err)
	if credentials.Environment != sii.EnvironmentCertification || credentials.FolioTo-credentials.FolioFrom+1 != 5 {
		check(fmt.Errorf("se requiere ambiente CERTIFICATION y un CAF de exactamente 5 folios; rango actual %d-%d", credentials.FolioFrom, credentials.FolioTo))
	}
	location, err := time.LoadLocation("America/Santiago")
	check(err)
	issueDate := now.In(location)
	cases := []struct {
		reference string
		items     []sii.SaleItem
	}{
		{"CASO-1", []sii.SaleItem{{Description: "Cambio de aceite", Quantity: 1, UnitPrice: 19900}, {Description: "Alineacion y balanceo", Quantity: 1, UnitPrice: 9900}}},
		{"CASO-2", []sii.SaleItem{{Description: "Papel de regalo", Quantity: 17, UnitPrice: 120}}},
		{"CASO-3", []sii.SaleItem{{Description: "Sandwic", Quantity: 2, UnitPrice: 1500}, {Description: "Bebida", Quantity: 2, UnitPrice: 550}}},
		{"CASO-4", []sii.SaleItem{{Description: "item afecto 1", Quantity: 8, UnitPrice: 1590}, {Description: "item exento 2", Quantity: 2, UnitPrice: 1000, Exempt: true}}},
		{"CASO-5", []sii.SaleItem{{Description: "Arroz", Quantity: 5, UnitPrice: 700, Unit: "Kg"}}},
	}
	documents := make([]sii.Document, 0, len(cases))
	for index, testCase := range cases {
		var total int64
		for _, item := range testCase.items {
			total += item.Quantity * item.UnitPrice
		}
		document, buildErr := sii.BuildDocument(credentials, sii.Sale{RequestID: testCase.reference, Folio: credentials.FolioFrom + int64(index), Amount: total, IssueDate: issueDate, Items: testCase.items, Reference: testCase.reference}, now)
		check(buildErr)
		documents = append(documents, document)
	}
	client := sii.Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Now: time.Now}
	if *step == "all" || *step == "dte" {
		submission, submitErr := client.SubmitSet(ctx, credentials, documents)
		check(submitErr)
		fmt.Printf("DTE_TRACK_ID=%s FOLIOS=%d-%d TOTAL=54160\n", submission.TrackID, credentials.FolioFrom, credentials.FolioTo)
	}
	if *step == "all" || *step == "rcof" {
		contents, buildErr := sii.BuildConsumption(credentials, sii.ConsumptionResume{Date: issueDate, Sequence: 1, NetAmount: 43831, VATAmount: 8329, ExemptAmount: 2000, TotalAmount: 54160, FolioFrom: credentials.FolioFrom, FolioTo: credentials.FolioTo}, now)
		check(buildErr)
		submission, submitErr := client.SubmitConsumption(ctx, credentials, contents)
		check(submitErr)
		fmt.Printf("RCOF_TRACK_ID=%s FOLIOS=%d-%d TOTAL=54160\n", submission.TrackID, credentials.FolioFrom, credentials.FolioTo)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
