package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"ind-hub-api-gox-sls-pri-gh/services/api-inbox/domain"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	stage := flag.String("stage", "dev", "Ambiente dev o prd")
	region := flag.String("region", "us-east-1", "Región AWS")
	profile := flag.String("profile", "", "Perfil AWS; por defecto pa-<stage>")
	table := flag.String("table", "", "Tabla de conversaciones; se deriva del stage si se omite")
	bucket := flag.String("bucket", "", "Bucket permanente de Inbox; se deriva del stage si se omite")
	prefix := flag.String("prefix", "presupuestos/", "Prefijo histórico regional")
	exceptionsPrefix := flag.String("exceptions-prefix", "presupuestos/excepciones/", "Prefijo histórico de excepciones")
	apply := flag.Bool("apply", false, "Guarda los programas en DRAFT; nunca publica")
	confirm := flag.String("confirm", "", "Confirmación requerida para aplicar en PRD")
	expectedAccount := flag.String("expected-account", "", "Cuenta AWS requerida para aplicar cambios")
	flag.Parse()

	if *stage != "dev" && *stage != "prd" {
		return fmt.Errorf("stage must be dev or prd")
	}
	if *profile == "" {
		*profile = "pa-" + *stage
	}
	if *table == "" {
		*table = fmt.Sprintf("ind-%s-conversaciones-ddb-%s-pri-use1", *stage, *stage)
	}
	if *bucket == "" {
		*bucket = fmt.Sprintf("ind-%s-inbox-assets-s3-%s-pri-use1", *stage, *stage)
	}
	if *apply && *stage == "prd" && *confirm != "APPLY_PRD_AGENT_PROGRAMS" {
		return fmt.Errorf("PRD apply requires --confirm APPLY_PRD_AGENT_PROGRAMS")
	}
	if *apply && strings.TrimSpace(*expectedAccount) == "" {
		return fmt.Errorf("apply requires --expected-account")
	}

	clearAmbientAWSCredentials()
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(*region), config.WithSharedConfigProfile(*profile))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	identity, err := sts.NewFromConfig(awsConfig).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("validate AWS identity: %w", err)
	}
	account := aws.ToString(identity.Account)
	if *expectedAccount != "" && account != *expectedAccount {
		return fmt.Errorf("AWS account mismatch: expected %s, got %s", *expectedAccount, account)
	}
	fmt.Printf("Identidad AWS: cuenta=%s arn=%s\n", account, aws.ToString(identity.Arn))
	storage := s3.NewFromConfig(awsConfig)
	keys, err := listProgramKeys(ctx, storage, *bucket, *prefix)
	if err != nil {
		return err
	}
	programs, err := domain.BuildManagedProgramsFromLegacyKeys(keys, *prefix, *exceptionsPrefix)
	if err != nil {
		return fmt.Errorf("build migration plan: %w", err)
	}

	client := dynamodb.NewFromConfig(awsConfig)
	service, err := domain.NewAgentManagementService(client, *table)
	if err != nil {
		return err
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("read agent configuration: %w", err)
	}
	for _, program := range snapshot.Draft.Config.Programs {
		if !strings.HasPrefix(program.ID, "legacy-") {
			return fmt.Errorf("draft contains non-migrated program %q; refusing to replace it", program.Name)
		}
	}

	counts := make(map[string]int)
	for _, program := range programs {
		for _, destination := range program.Destinations {
			counts[destination]++
		}
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("Modo: %s\n", map[bool]string{true: "APPLY DRAFT", false: "DRY-RUN"}[*apply])
	fmt.Printf("Stage: %s | bucket: %s | tabla: %s\n", *stage, *bucket, *table)
	fmt.Printf("PDF únicos: %d | programas administrados: %d | paridad: OK\n", len(keys), len(programs))
	for _, name := range names {
		fmt.Printf("- %s: %d PDF\n", name, counts[name])
	}
	if !*apply {
		fmt.Println("Sin escrituras. Ejecuta con --apply para reemplazar solo los programas legacy del borrador.")
		return nil
	}

	draft := snapshot.Draft.Config
	draft.Programs = programs
	if _, err := service.SaveDraftIfUnchanged(ctx, draft, snapshot.Draft.UpdatedAt); err != nil {
		return fmt.Errorf("save migration draft: %w", err)
	}
	fmt.Println("Borrador actualizado. La versión publicada no fue modificada; revisa y publica desde Configuración.")
	return nil
}

func clearAmbientAWSCredentials() {
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN", "AWS_CREDENTIAL_EXPIRATION", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
		_ = os.Unsetenv(name)
	}
}

func listProgramKeys(ctx context.Context, client *s3.Client, bucket, prefix string) ([]string, error) {
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix)})
	keys := make([]string, 0)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list legacy program PDFs: %w", err)
		}
		for _, item := range page.Contents {
			if item.Key != nil && strings.HasSuffix(strings.ToLower(*item.Key), ".pdf") {
				keys = append(keys, *item.Key)
			}
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no PDF files found under s3://%s/%s", bucket, prefix)
	}
	return keys, nil
}
