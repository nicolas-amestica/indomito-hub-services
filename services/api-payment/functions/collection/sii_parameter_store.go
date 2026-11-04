package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const siiParameterChunkSize = 3500

var errSIIParameterMissing = errors.New("configuración SII no creada")

type siiParameterAPI interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	GetParameters(context.Context, *ssm.GetParametersInput, ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
	PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// LoadSIIParameterValue reconstruye la configuración SII almacenada en parámetros Standard.
func LoadSIIParameterValue(ctx context.Context, client *ssm.Client, stage string) ([]byte, error) {
	return (siiParameterStore{Client: client, Stage: stage}).Load(ctx)
}

type siiParameterManifest struct {
	Version string `json:"version"`
	Parts   int    `json:"parts"`
	SHA256  string `json:"sha256"`
}

type siiParameterStore struct {
	Client siiParameterAPI
	Stage  string
}

func (s siiParameterStore) baseName() string {
	return "/indomito/" + s.Stage + "/payments/sii/boleta-config"
}

func (s siiParameterStore) Load(ctx context.Context) ([]byte, error) {
	if s.Client == nil || s.Stage == "" {
		return nil, errors.New("configuración SII no disponible")
	}
	result, err := s.Client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(s.baseName()), WithDecryption: aws.Bool(true)})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return nil, errSIIParameterMissing
		}
		return nil, errors.New("configuración SII no disponible")
	}
	if result == nil || result.Parameter == nil || result.Parameter.Value == nil {
		return nil, errors.New("configuración SII no disponible")
	}
	var manifest siiParameterManifest
	if err := json.Unmarshal([]byte(aws.ToString(result.Parameter.Value)), &manifest); err != nil || manifest.Parts < 1 || manifest.SHA256 == "" || manifest.Version == "" {
		return nil, errors.New("manifiesto SII inválido")
	}
	names := make([]string, manifest.Parts)
	for index := range names {
		names[index] = fmt.Sprintf("%s/versions/%s/part-%03d", s.baseName(), manifest.Version, index)
	}
	parts, err := s.Client.GetParameters(ctx, &ssm.GetParametersInput{Names: names, WithDecryption: aws.Bool(true)})
	if err != nil || len(parts.InvalidParameters) > 0 || len(parts.Parameters) != manifest.Parts {
		return nil, errors.New("fragmentos SII incompletos")
	}
	sort.Slice(parts.Parameters, func(i, j int) bool {
		return aws.ToString(parts.Parameters[i].Name) < aws.ToString(parts.Parameters[j].Name)
	})
	var raw []byte
	for _, part := range parts.Parameters {
		raw = append(raw, []byte(aws.ToString(part.Value))...)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != manifest.SHA256 {
		return nil, errors.New("integridad SII inválida")
	}
	return raw, nil
}

func (s siiParameterStore) Save(ctx context.Context, raw []byte) error {
	if s.Client == nil || s.Stage == "" || len(raw) == 0 {
		return errors.New("configuración SII inválida")
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	version := hash[:16]
	parts := (len(raw) + siiParameterChunkSize - 1) / siiParameterChunkSize
	for index := 0; index < parts; index++ {
		end := min((index+1)*siiParameterChunkSize, len(raw))
		name := fmt.Sprintf("%s/versions/%s/part-%03d", s.baseName(), version, index)
		if _, err := s.Client.PutParameter(ctx, &ssm.PutParameterInput{Name: aws.String(name), Value: aws.String(string(raw[index*siiParameterChunkSize : end])), Type: ssmtypes.ParameterTypeSecureString, Tier: ssmtypes.ParameterTierStandard, Overwrite: aws.Bool(true)}); err != nil {
			return fmt.Errorf("guardar fragmento SII: %w", err)
		}
	}
	manifest, err := json.Marshal(siiParameterManifest{Version: version, Parts: parts, SHA256: hash})
	if err != nil {
		return fmt.Errorf("crear manifiesto SII: %w", err)
	}
	if _, err := s.Client.PutParameter(ctx, &ssm.PutParameterInput{Name: aws.String(s.baseName()), Value: aws.String(string(manifest)), Type: ssmtypes.ParameterTypeSecureString, Tier: ssmtypes.ParameterTierStandard, Overwrite: aws.Bool(true)}); err != nil {
		return fmt.Errorf("activar configuración SII: %w", err)
	}
	return nil
}
