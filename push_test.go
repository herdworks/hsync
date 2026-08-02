package hsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	hyperproviders "github.com/herdworks/hyperproviders-go"
)

type unavailableSecrets struct{}

func (unavailableSecrets) Name() string { return "unavailable" }

func (unavailableSecrets) Resolve(context.Context, string) (hyperproviders.SecretValue, error) {
	return nil, fmt.Errorf("%w", hyperproviders.ErrSecretNotFound)
}

func TestPushConfigRegistersHyperctlHostingEngines(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "providers.yaml")
	config := `providers:
  storage:
    kind: s3
    endpoint: https://example.r2.cloudflarestorage.com
    config:
      bucket: website-production
    secrets:
      - name: access_key_id
        key: AWS_ACCESS_KEY_ID
      - name: secret_access_key
        key: AWS_SECRET_ACCESS_KEY
  repository:
    kind: git
    endpoint: https://example.com/site.git
    config:
      branch: main
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDeploymentConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, provider := range loaded.Providers {
		if err := provider.Validate(); err != nil {
			t.Fatalf("provider %q validation = %v", name, err)
		}
	}
	if err := loaded.Providers["storage"].Ready(context.Background(), hyperproviders.SecretsProviders{unavailableSecrets{}}); !errors.Is(err, hyperproviders.ErrSecretNotFound) {
		t.Fatalf("storage readiness error = %v, want missing secret", err)
	}
}
