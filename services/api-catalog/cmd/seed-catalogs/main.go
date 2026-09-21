package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"ind-hub-api-gox-sls-pri-gh/bootstrap"
	"ind-hub-api-gox-sls-pri-gh/libs/awsddb"
	"ind-hub-api-gox-sls-pri-gh/libs/logger"
	"ind-hub-api-gox-sls-pri-gh/services/api-catalog/seed"
)

const maxManifestBytes = 1 << 20

type config struct {
	file    string
	table   string
	stage   string
	region  string
	profile string
	apply   bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitCode := run(ctx, os.Args[1:])
	stop()
	os.Exit(exitCode)
}

func run(ctx context.Context, args []string) int {
	cfg, manifest, err := prepare(args)
	stage := cfg.stage
	if stage == "" {
		stage = "local"
	}
	log := logger.New("seed-catalogs", "cli", stage)
	defer func() {
		_ = log.Sync()
	}()

	if err != nil {
		log.Warn("catalogSeedValidationFailed", zap.Error(err))
		return 1
	}

	log.Info("catalogSeedManifestValidated", zap.Int("itemCount", seed.ItemCount(manifest)))
	if !cfg.apply {
		log.Info("catalogSeedDryRunCompleted", zap.Bool("apply", false))
		return 0
	}

	base := bootstrap.Config{
		AppName:    "api-catalog-seed",
		AppStage:   cfg.stage,
		AppRegion:  cfg.region,
		AppProfile: cfg.profile,
	}
	awsConfig, err := bootstrap.LoadAWSConfig(ctx, base)
	if err != nil {
		log.Error("catalogSeedAWSConfigFailed", zap.Error(err))
		return 1
	}

	if err := seed.Apply(ctx, awsddb.New(awsConfig), cfg.table, manifest); err != nil {
		log.Error("catalogSeedFailed", zap.Int("itemCount", seed.ItemCount(manifest)), zap.Error(err))
		return 1
	}

	log.Info("catalogSeedCompleted", zap.Int("itemCount", seed.ItemCount(manifest)))
	return 0
}

func prepare(args []string) (config, seed.Manifest, error) {
	flags := flag.NewFlagSet("seed-catalogs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var cfg config
	flags.StringVar(&cfg.file, "file", "", "ruta al manifiesto JSON")
	flags.StringVar(&cfg.table, "table", "", "nombre exacto de la tabla DynamoDB")
	flags.StringVar(&cfg.stage, "stage", "dev", "ambiente dev o prd")
	flags.StringVar(&cfg.region, "region", "us-east-1", "región AWS")
	flags.StringVar(&cfg.profile, "profile", "", "perfil AWS explícito")
	flags.BoolVar(&cfg.apply, "apply", false, "escribe el manifiesto validado en DynamoDB")

	if err := flags.Parse(args); err != nil {
		return cfg, seed.Manifest{}, fmt.Errorf("argumentos inválidos: %w", err)
	}
	if flags.NArg() != 0 {
		return cfg, seed.Manifest{}, fmt.Errorf("no se admiten argumentos posicionales")
	}
	if cfg.file == "" {
		return cfg, seed.Manifest{}, fmt.Errorf("-file es obligatorio")
	}
	if cfg.stage != "dev" && cfg.stage != "prd" {
		return cfg, seed.Manifest{}, fmt.Errorf("-stage debe ser dev o prd")
	}
	if cfg.region != "us-east-1" {
		return cfg, seed.Manifest{}, fmt.Errorf("-region debe ser us-east-1")
	}
	if cfg.apply && cfg.table == "" {
		return cfg, seed.Manifest{}, fmt.Errorf("-table es obligatorio junto con -apply")
	}
	if cfg.apply && cfg.profile == "" {
		return cfg, seed.Manifest{}, fmt.Errorf("-profile es obligatorio junto con -apply")
	}
	if cfg.apply && cfg.profile != "pa-"+cfg.stage {
		return cfg, seed.Manifest{}, fmt.Errorf("-profile debe ser pa-%s para el stage %s", cfg.stage, cfg.stage)
	}

	file, err := os.Open(cfg.file)
	if err != nil {
		return cfg, seed.Manifest{}, fmt.Errorf("no se pudo abrir el manifiesto: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	manifest, err := seed.LoadManifest(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return cfg, seed.Manifest{}, err
	}
	if info, err := file.Stat(); err != nil {
		return cfg, seed.Manifest{}, fmt.Errorf("no se pudo inspeccionar el manifiesto: %w", err)
	} else if info.Size() > maxManifestBytes {
		return cfg, seed.Manifest{}, fmt.Errorf("el manifiesto excede el máximo de %d bytes", maxManifestBytes)
	}
	if err := seed.ValidateManifest(manifest); err != nil {
		return cfg, seed.Manifest{}, err
	}

	return cfg, manifest, nil
}
