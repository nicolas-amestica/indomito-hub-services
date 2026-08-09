package bootstrap

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// IsLambda returns true when running inside AWS Lambda.
func IsLambda() bool {
	return os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != ""
}

// LoadAWSConfig carga la configuración de AWS SDK v2.
func LoadAWSConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	retryer := func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 7
			o.Backoff = retry.NewExponentialJitterBackoff(2 * time.Second)
		})
	}

	loaders := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRetryer(retryer),
	}

	if cfg.AppProfile != "" {
		loaders = append(loaders, awsconfig.WithSharedConfigProfile(cfg.AppProfile))
	}

	if cfg.AppRegion != "" {
		loaders = append(loaders, awsconfig.WithRegion(cfg.AppRegion))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loaders...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load aws config: %w", err)
	}

	if IsLambda() || cfg.AssumeRoleARN == "" {
		return awsCfg, nil
	}

	stsClient := sts.NewFromConfig(awsCfg)
	sessionName := cfg.AssumeRoleSessionName
	if sessionName == "" {
		sessionName = fmt.Sprintf("%s-%d", cfg.AppName, time.Now().Unix())
	}

	provider := stscreds.NewAssumeRoleProvider(stsClient, cfg.AssumeRoleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = sessionName
	})

	assumed := awsCfg.Copy()
	assumed.Credentials = aws.NewCredentialsCache(provider)

	return assumed, nil
}
