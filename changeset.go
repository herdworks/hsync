package hsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

// Changeset is the exact set of hsync files for one publication attempt. Its
// sealed manifest and index bytes are never reserialized.
type Changeset struct {
	Manifest      Manifest
	Blobs         map[Identifier][]byte
	ExpectedIndex []byte
	PendingIndex  []byte
	CleanIndex    []byte
	Instructions  []byte
}

// Apply materializes this changeset's exact files below root. It performs no
// provider writes; callers supply a complete, publishable changeset.
func (changeset Changeset) Apply(root string) error {
	if root == "" {
		return fmt.Errorf("%w: deployment root is required", ErrInvalidState)
	}
	manifestFilename, err := changeset.Manifest.Filename()
	if err != nil {
		return err
	}
	indexPath := changeset.Manifest.IndexPath()
	if indexPath == "" {
		return fmt.Errorf("%w: changeset index path is required", ErrInvalidState)
	}
	syncRelative := strings.TrimPrefix(indexPath, "/")
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
		if err := writePayloadFile(root, path.Join(syncRoot, "blobs", id.Filename()), contents); err != nil {
			return err
		}
	}
	if err := writePayloadFile(root, path.Join(syncRoot, "manifests", manifestFilename), changeset.Manifest.Raw()); err != nil {
		return err
	}
	if err := writePayloadFile(root, ".deployment/pending.json", changeset.PendingIndex); err != nil {
		return err
	}
	if len(changeset.ExpectedIndex) > 0 {
		if err := writePayloadFile(root, ".deployment/expected.json", changeset.ExpectedIndex); err != nil {
			return err
		}
	}
	if err := writePayloadFile(root, syncRelative, changeset.CleanIndex); err != nil {
		return err
	}
	return writePayloadFile(root, ".deployment.json", changeset.Instructions)
}

func completeChangeset(changeset Changeset, observedIndexRaw []byte, now time.Time) (*Changeset, error) {
	if !changeset.Manifest.Sealed() {
		return nil, fmt.Errorf("%w: changeset manifest is not sealed", ErrInvalidState)
	}
	manifestID := changeset.Manifest.ID()
	for id, contents := range changeset.Blobs {
		if err := id.Validate(); err != nil {
			return nil, err
		}
		if NewIdentifier(contents) != id {
			return nil, fmt.Errorf("%w: changeset blob %s has mismatched bytes", ErrInvalidState, id)
		}
	}
	pendingIndex, cleanIndex, err := changesetIndexes(manifestID, observedIndexRaw, now)
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
	instructions, err := deploymentInstructions(changeset.Manifest.IndexPath(), len(observedIndexRaw) > 0)
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

func deploymentInstructions(indexPath string, hasExpected bool) ([]byte, error) {
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
	syncRelative := strings.TrimPrefix(indexPath, "/")
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

func writePayloadFile(root, relative string, contents []byte) error {
	if err := validateRelativePath(relative); err != nil {
		return err
	}
	filename, err := safeWriteTarget(root, relative)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, contents, 0o644)
}
