package hsync

import "github.com/herdworks/htup"

// Deployment is an htup deployment with optional hsync reconciliation and
// finalization. It adds no deployment configuration beyond its PutOperation.
type Deployment struct {
	PutOperation
}

// NewDeployment creates an hsync-enabled wrapper around provider deployment.
func NewDeployment(provider *htup.Deployment) *Deployment {
	return &Deployment{PutOperation: PutOperation{Deployment: provider}}
}
