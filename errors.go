package hypersync

import "errors"

var (
	// ErrInvalidState reports live or local state that does not satisfy the v1
	// manifest and index invariants.
	ErrInvalidState = errors.New("invalid synchronization state")
	// ErrDeploymentInProgress reports a pending live deployment younger than
	// the configured pending-state threshold.
	ErrDeploymentInProgress = errors.New("deployment in progress")
	// ErrStalePending reports a pending live deployment requiring manual
	// recovery.
	ErrStalePending = errors.New("stale pending deployment")
	// ErrManualResolution reports a manifest relationship that v1 cannot merge.
	ErrManualResolution = errors.New("manual synchronization resolution required")
)
