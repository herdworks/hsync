package hsync

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
)

// Changeset is the exact set of hsync files added to a Payload for one
// publication attempt. Its manifest and index bytes must never be reserialized.
type Changeset struct {
	ManifestID    Identifier
	Manifest      []byte
	Blobs         map[Identifier][]byte
	ExpectedIndex []byte
	PendingIndex  []byte
	CleanIndex    []byte
	Instructions  []byte

	syncPath string
	manifest Manifest
}

// Apply writes this changeset's exact files below payload. It performs no
// provider writes and can be called only for a complete, publishable changeset.
func (changeset Changeset) Apply(payload Payload) error {
	if payload.Root == "" {
		return fmt.Errorf("%w: payload root is required", ErrInvalidState)
	}
	if err := changeset.ManifestID.Validate(); err != nil {
		return err
	}
	if NewIdentifier(changeset.Manifest) != changeset.ManifestID {
		return fmt.Errorf("%w: manifest %s has mismatched bytes", ErrInvalidState, changeset.ManifestID)
	}
	if changeset.syncPath == "" {
		return fmt.Errorf("%w: changeset sync path is required", ErrInvalidState)
	}
	syncRelative := strings.TrimPrefix(changeset.syncPath, "/")
	syncRoot := path.Dir(syncRelative)
	if syncRoot == "." {
		syncRoot = ""
	}
	for id, contents := range changeset.Blobs {
		if err := id.Validate(); err != nil {
			return err
		}
		if NewIdentifier(contents) != id {
			return fmt.Errorf("%w: changeset blob %s has mismatched bytes", ErrInvalidState, id)
		}
		if err := writePayloadFile(payload.Root, path.Join(syncRoot, "blobs", id.Filename()), contents); err != nil {
			return err
		}
	}
	if err := writePayloadFile(payload.Root, path.Join(syncRoot, "manifests", changeset.ManifestID.Filename()+".json"), changeset.Manifest); err != nil {
		return err
	}
	if err := writePayloadFile(payload.Root, ".deployment/pending.json", changeset.PendingIndex); err != nil {
		return err
	}
	if len(changeset.ExpectedIndex) > 0 {
		if err := writePayloadFile(payload.Root, ".deployment/expected.json", changeset.ExpectedIndex); err != nil {
			return err
		}
	}
	if err := writePayloadFile(payload.Root, syncRelative, changeset.CleanIndex); err != nil {
		return err
	}
	return writePayloadFile(payload.Root, ".deployment.json", changeset.Instructions)
}

func completeChangeset(changeset Changeset, observedIndexRaw []byte, now time.Time) (*Changeset, error) {
	if err := changeset.ManifestID.Validate(); err != nil {
		return nil, err
	}
	if NewIdentifier(changeset.Manifest) != changeset.ManifestID {
		return nil, fmt.Errorf("%w: manifest %s has mismatched bytes", ErrInvalidState, changeset.ManifestID)
	}
	for id, contents := range changeset.Blobs {
		if err := id.Validate(); err != nil {
			return nil, err
		}
		if NewIdentifier(contents) != id {
			return nil, fmt.Errorf("%w: changeset blob %s has mismatched bytes", ErrInvalidState, id)
		}
	}
	pendingIndex, cleanIndex, err := changesetIndexes(changeset.ManifestID, observedIndexRaw, now)
	if err != nil {
		return nil, err
	}
	pendingRaw, err := json.Marshal(pendingIndex)
	if err != nil {
		return nil, err
	}
	cleanRaw, err := json.Marshal(cleanIndex)
	if err != nil {
		return nil, err
	}
	instructions, err := deploymentInstructions(changeset.syncPath, len(observedIndexRaw) > 0)
	if err != nil {
		return nil, err
	}
	changeset.ExpectedIndex = observedIndexRaw
	changeset.PendingIndex = pendingRaw
	changeset.CleanIndex = cleanRaw
	changeset.Instructions = instructions
	return &changeset, nil
}

func changesetIndexes(changeset Identifier, observedRaw []byte, now time.Time) (Index, Index, error) {
	if len(observedRaw) == 0 {
		return Index{Version: ManifestVersion, UpdatedAt: now, Pending: changeset, History: []Identifier{changeset}}, Index{Version: ManifestVersion, UpdatedAt: now, Published: changeset, History: []Identifier{changeset}}, nil
	}
	observed, err := ParseIndex(observedRaw)
	if err != nil {
		return Index{}, Index{}, err
	}
	if observed.Pending != "" {
		return Index{}, Index{}, observed.PendingError(now)
	}
	history := append([]Identifier{changeset}, observed.History...)
	return Index{Version: ManifestVersion, UpdatedAt: now, Published: observed.Published, Pending: changeset, History: history}, Index{Version: ManifestVersion, UpdatedAt: now, Published: changeset, History: history}, nil
}

func deploymentInstructions(syncPath string, hasExpected bool) ([]byte, error) {
	type precondition struct {
		Target   string `json:"target"`
		Expected string `json:"expected,omitempty"`
		Contents string `json:"contents"`
	}
	type instructions struct {
		Preconditions []precondition `json:"preconditions"`
		Ignore        []string       `json:"ignore"`
		Enqueue       []string       `json:"enqueue"`
	}
	syncRelative := strings.TrimPrefix(syncPath, "/")
	syncRoot := path.Dir(syncRelative)
	manifestPattern := path.Join(syncRoot, "manifests", "*.json")
	if syncRoot == "." {
		manifestPattern = "manifests/*.json"
	}
	condition := precondition{Target: syncRelative, Contents: ".deployment/pending.json"}
	if hasExpected {
		condition.Expected = ".deployment/expected.json"
	}
	bytes, err := json.Marshal(instructions{Preconditions: []precondition{condition}, Ignore: []string{".deployment/"}, Enqueue: []string{manifestPattern, syncRelative}})
	if err != nil {
		return nil, err
	}
	return bytes, nil
}
