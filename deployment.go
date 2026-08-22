package hsync

import (
	"context"
	"fmt"

	"github.com/herdworks/htup"
)

// Deployment is an htup deployment with optional hsync reconciliation and
// finalization. It adds no deployment configuration beyond its PutOperation.
type Deployment struct {
	PutOperation
}

// NewDeployment creates an hsync-enabled wrapper around provider deployment.
func NewDeployment(provider *htup.Deployment) *Deployment {
	return &Deployment{PutOperation: PutOperation{Deployment: provider}}
}

// NewDeploymentWithConfig creates an hsync-enabled wrapper around provider
// deployment using an already resolved repository configuration.
func NewDeploymentWithConfig(provider *htup.Deployment, config RepositoryConfig) *Deployment {
	deployment := NewDeployment(provider)
	deployment.resolvedConfig = &config
	return deployment
}

// Run reconciles, deploys, and finalizes an hsync-enabled provider deployment.
func (deployment *Deployment) Run(ctx context.Context) error {
	if deployment == nil {
		return fmt.Errorf("deployment is required")
	}
	if err := deployment.PutOperation.Prepare(ctx); err != nil {
		return err
	}
	if deployment.ProviderStatus() == htup.DeploymentPending {
		if err := deployment.prepareProvider(ctx); err != nil {
			return err
		}
	}
	return deployment.put(ctx)
}
