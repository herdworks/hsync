package hsync

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	hyperproviders "github.com/herdworks/hyperproviders-go"
	envsecrets "github.com/herdworks/hyperproviders-go/providers/env"
	_ "github.com/herdworks/hyperproviders-go/providers/git"
	_ "github.com/herdworks/hyperproviders-go/providers/s3"
	"gopkg.in/yaml.v3"
)

// ConfigDocument is the hsync-relevant subset of any project YAML or JSON
// document. Unrelated top-level keys are ignored.
type ConfigDocument struct {
	Providers    hyperproviders.HostingProviders        `json:"providers" yaml:"providers"`
	Environments map[string]*hyperproviders.Environment `json:"environments,omitempty" yaml:"environments,omitempty"`
	Sync         *SyncConfig                            `json:"sync,omitempty" yaml:"sync,omitempty"`
}

// LoadConfigDocument parses a YAML or JSON project configuration document.
func LoadConfigDocument(filename string) (ConfigDocument, error) {
	raw, err := hyperproviders.ReadDeploymentConfig(filename)
	if err != nil {
		return ConfigDocument{}, err
	}
	return ParseConfigDocument(filename, raw)
}

// ParseConfigDocument parses the hsync-relevant projection of raw project
// configuration bytes read from filename.
func ParseConfigDocument(filename string, raw []byte) (ConfigDocument, error) {
	var config ConfigDocument
	var err error
	if strings.EqualFold(filepath.Ext(filename), ".yaml") || strings.EqualFold(filepath.Ext(filename), ".yml") {
		err = yaml.Unmarshal(raw, &config)
	} else {
		err = json.Unmarshal(raw, &config)
	}
	if err != nil {
		return ConfigDocument{}, fmt.Errorf("parse configuration: %w", err)
	}
	return config, nil
}

// LoadDeploymentConfig parses a YAML or JSON document containing providers.
func LoadDeploymentConfig(filename string) (ConfigDocument, error) {
	config, err := LoadConfigDocument(filename)
	if err != nil {
		return ConfigDocument{}, err
	}
	if len(config.Providers) == 0 {
		return ConfigDocument{}, fmt.Errorf("deployment config providers is empty")
	}
	return config, nil
}

// LoadSyncConfig loads and validates the top-level sync configuration.
func LoadSyncConfig(filename string) (SyncConfig, error) {
	config, err := LoadConfigDocument(filename)
	if err != nil {
		return SyncConfig{}, err
	}
	if config.Sync == nil {
		return SyncConfig{}, fmt.Errorf("configuration sync is empty")
	}
	if err := config.Sync.Validate(); err != nil {
		return SyncConfig{}, err
	}
	return *config.Sync, nil
}

// PushOptions selects providers from a generic deployment configuration.
type PushOptions struct {
	ConfigPath, Environment string
	Providers               []string
	Secrets                 hyperproviders.SecretsProviders
}

// Push reconciles and publishes a downloaded checkout without requiring an SSG.
func Push(ctx context.Context, directory string, options PushOptions) error {
	if directory == "" || options.ConfigPath == "" {
		return fmt.Errorf("checkout directory and deployment config are required")
	}
	if options.Environment != "" && len(options.Providers) != 0 {
		return fmt.Errorf("environment and providers are mutually exclusive")
	}
	configBytes, err := hyperproviders.ReadDeploymentConfig(options.ConfigPath)
	if err != nil {
		return err
	}
	if filepath.Clean(directory) != filepath.Clean(filepath.Dir(options.ConfigPath)) {
		return fmt.Errorf("checkout directory must be the deployment config directory")
	}
	var providers hyperproviders.HostingProviders
	if len(options.Providers) != 0 {
		config, err := ParseConfigDocument(options.ConfigPath, configBytes)
		if err != nil {
			return err
		}
		providers, err = config.Providers.Select(options.Providers)
		if err != nil {
			return err
		}
	}
	secrets := options.Secrets
	if secrets == nil {
		secrets = hyperproviders.SecretsProviders{envsecrets.New()}
	}
	return (&Deployment{ConfigPath: options.ConfigPath, ConfigBytes: configBytes, PayloadPath: directory, Providers: providers, Environment: options.Environment, Kind: hyperproviders.IncrementalDeployment}).Execute(ctx, secrets, nil)
}
