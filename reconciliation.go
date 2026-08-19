package hsync

import (
	"context"
	"fmt"
	"reflect"
	"sort"
)

type Reconciliation struct {
	Diffs []ReconciliationDiff

	indexPath string
	parent    Identifier
	headBlobs map[Identifier]struct{}
	blobs     map[Identifier][]byte
}

type ReconciliationDiff struct {
	Path     string
	Base     *ManifestEntry
	Local    *ManifestEntry
	Head     *ManifestEntry
	Resolved *ManifestEntry
}

type ReconciliationPlan struct {
	Writes   []ManifestEntry
	Removes  []string
	Retained []string
}

type reconciliationEntries interface {
	Entries() []ManifestEntry
}

// ManifestReader retrieves sealed immutable manifests by Identifier.
type ManifestReader interface {
	ReadManifest(context.Context, Identifier) (Manifest, error)
}

// Lineage holds the verified remote manifests from a repository's local base
// to the head revision. Revisions begins with head and ends with the direct
// child of base when they differ.
type Lineage struct {
	Revisions []Manifest
}

// ReadLineage proves that baseID is an immutable ancestor of headID and
// retains every newer verified revision for the lineage-based three-way
// divergence algorithm used during repository reconciliation.
func ReadLineage(ctx context.Context, reader ManifestReader, baseID, headID Identifier) (Lineage, error) {
	if err := baseID.Validate(); err != nil {
		return Lineage{}, err
	}
	if err := headID.Validate(); err != nil {
		return Lineage{}, err
	}
	if baseID == headID {
		manifest, err := reader.ReadManifest(ctx, headID)
		if err != nil {
			return Lineage{}, err
		}
		return Lineage{Revisions: []Manifest{manifest}}, nil
	}
	lineage := Lineage{}
	visited := map[Identifier]struct{}{}
	for current := headID; current != ""; {
		if current == baseID {
			return lineage, nil
		}
		if _, seen := visited[current]; seen {
			return Lineage{}, fmt.Errorf("%w: manifest ancestry loop at %s", ErrInvalidState, current)
		}
		visited[current] = struct{}{}
		manifest, err := reader.ReadManifest(ctx, current)
		if err != nil {
			return Lineage{}, err
		}
		lineage.Revisions = append(lineage.Revisions, manifest)
		current = manifest.Parent()
	}
	return Lineage{}, fmt.Errorf("%w: base manifest %s is not reachable from head %s", ErrManualResolution, baseID, headID)
}

// Reconcile applies the lineage-based three-way divergence algorithm to this
// repository's Base and Local state against a verified remote head. Both
// deployment preparation and pull use this single file-selection implementation.
func (repository Repository) Reconcile(headID Identifier, lineage Lineage) (Reconciliation, error) {
	base, local := repository.Base, repository.Local
	if err := base.Validate(); err != nil {
		return Reconciliation{}, err
	}
	if err := headID.Validate(); err != nil {
		return Reconciliation{}, err
	}
	if len(lineage.Revisions) == 0 {
		return Reconciliation{}, fmt.Errorf("%w: reconciliation lineage is required", ErrInvalidState)
	}
	head := lineage.Revisions[0]
	if err := head.Validate(); err != nil {
		return Reconciliation{}, err
	}
	if base.IndexPath() != head.IndexPath() {
		return Reconciliation{}, fmt.Errorf("%w: base and head index paths differ", ErrManualResolution)
	}
	for _, revision := range lineage.Revisions[1:] {
		if err := revision.Validate(); err != nil {
			return Reconciliation{}, err
		}
		if revision.IndexPath() != head.IndexPath() {
			return Reconciliation{}, fmt.Errorf("%w: lineage index path differs", ErrManualResolution)
		}
	}

	baseEntries := entriesByPath(base)
	headEntries := entriesByPath(head)
	localEntries := entriesByPath(local)
	paths := make(map[string]struct{}, len(baseEntries)+len(headEntries)+len(localEntries))
	for name := range baseEntries {
		paths[name] = struct{}{}
	}
	for name := range headEntries {
		paths[name] = struct{}{}
	}
	for name := range localEntries {
		paths[name] = struct{}{}
	}
	for _, revision := range lineage.Revisions {
		for name := range entriesByPath(revision) {
			paths[name] = struct{}{}
		}
	}

	diffs := make([]ReconciliationDiff, 0, len(paths))
	for name := range paths {
		baseEntry, baseOK := baseEntries[name]
		localEntry, localOK := localEntries[name]
		headEntry, headOK := headEntries[name]
		selected, ok := mergeEntry(name, baseEntry, baseOK, localEntry, localOK, headEntry, headOK, lineage.Revisions)
		diffs = append(diffs, ReconciliationDiff{Path: name, Base: manifestEntry(baseEntry, baseOK), Local: manifestEntry(localEntry, localOK), Head: manifestEntry(headEntry, headOK), Resolved: manifestEntry(selected, ok)})
	}
	sort.Slice(diffs, func(left, right int) bool { return diffs[left].Path < diffs[right].Path })
	headEntriesSlice := head.Entries()
	headBlobs := make(map[Identifier]struct{}, len(headEntriesSlice))
	for _, entry := range headEntriesSlice {
		headBlobs[entry.Blob] = struct{}{}
	}
	return Reconciliation{Diffs: diffs, indexPath: head.IndexPath(), parent: headID, headBlobs: headBlobs, blobs: local.Blobs}, nil
}

func (reconciliation Reconciliation) Changeset() (Changeset, error) {
	entries := make([]ManifestEntry, 0, len(reconciliation.Diffs))
	blobs := map[Identifier][]byte{}
	for _, diff := range reconciliation.Diffs {
		if diff.Resolved == nil {
			continue
		}
		entries = append(entries, *diff.Resolved)
		if _, ok := reconciliation.headBlobs[diff.Resolved.Blob]; ok {
			continue
		}
		contents, ok := reconciliation.blobs[diff.Resolved.Blob]
		if !ok {
			return Changeset{}, fmt.Errorf("%w: missing local bytes for required blob %s", ErrInvalidState, diff.Resolved.Blob)
		}
		blobs[diff.Resolved.Blob] = contents
	}
	manifest, err := NewSealedManifest(reconciliation.indexPath, reconciliation.parent, entries)
	if err != nil {
		return Changeset{}, err
	}
	return Changeset{Manifest: manifest, Blobs: blobs}, nil
}

func (reconciliation Reconciliation) Plan() ReconciliationPlan {
	plan := ReconciliationPlan{}
	for _, diff := range reconciliation.Diffs {
		localChanged := !entriesEqualPointers(diff.Local, diff.Base)
		headChanged := !entriesEqualPointers(diff.Head, diff.Base)
		if entriesEqualPointers(diff.Resolved, diff.Local) {
			if localChanged && headChanged && !entriesEqualPointers(diff.Local, diff.Head) {
				plan.Retained = append(plan.Retained, diff.Path)
			}
			continue
		}
		if diff.Resolved == nil {
			plan.Removes = append(plan.Removes, diff.Path)
		} else {
			plan.Writes = append(plan.Writes, *diff.Resolved)
		}
	}
	return plan
}

func changesetMatchesHead(changeset Changeset, head Manifest) bool {
	return changeset.Manifest.Version() == head.Version() &&
		changeset.Manifest.IndexPath() == head.IndexPath() &&
		reflect.DeepEqual(changeset.Manifest.Entries(), head.Entries())
}

func mergeEntry(name string, base ManifestEntry, baseOK bool, local ManifestEntry, localOK bool, head ManifestEntry, headOK bool, history []Manifest) (ManifestEntry, bool) {
	if entriesEqual(local, localOK, base, baseOK) {
		return head, headOK
	}
	if entriesEqual(local, localOK, head, headOK) {
		return local, localOK
	}
	for _, revision := range history {
		entry, ok := entriesByPath(revision)[name]
		if entriesEqual(local, localOK, entry, ok) {
			return head, headOK
		}
	}
	if entriesEqual(head, headOK, base, baseOK) {
		return local, localOK
	}
	return local, localOK
}

func entriesEqual(left ManifestEntry, leftOK bool, right ManifestEntry, rightOK bool) bool {
	return leftOK == rightOK && (!leftOK || left.Blob == right.Blob)
}

func manifestEntry(entry ManifestEntry, ok bool) *ManifestEntry {
	if !ok {
		return nil
	}
	return &entry
}

func entriesEqualPointers(left, right *ManifestEntry) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && left.Blob == right.Blob)
}

func entriesByPath(source reconciliationEntries) map[string]ManifestEntry {
	entries := source.Entries()
	result := make(map[string]ManifestEntry, len(entries))
	for _, entry := range entries {
		result[entry.Path] = entry
	}
	return result
}
