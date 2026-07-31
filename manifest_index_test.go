package hypersync

import (
	"errors"
	"testing"
	"time"
)

func TestIndexPendingError(t *testing.T) {
	t.Parallel()
	id := NewIdentifier([]byte("manifest"))
	index := Index{Version: Version, UpdatedAt: time.Now(), Published: id, Pending: id, History: []Identifier{id, id}}
	if !errors.Is(index.PendingError(time.Now()), ErrDeploymentInProgress) {
		t.Fatal("pending deployment was not blocked")
	}
	if !errors.Is(index.PendingError(time.Now().Add(301*time.Second)), ErrStalePending) {
		t.Fatal("stale deployment was not blocked")
	}
}

func TestParseIndexRejectsTrailingValue(t *testing.T) {
	t.Parallel()
	id := NewIdentifier([]byte("manifest"))
	raw := []byte(`{"version":1,"updated_at":"2026-07-30T12:00:00Z","published":"` + id + `","history":["` + id + `"]} {}`)
	if _, err := ParseIndex(raw); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ParseIndex() error = %v, want invalid state", err)
	}
}
