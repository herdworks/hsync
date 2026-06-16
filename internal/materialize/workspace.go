package materialize

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/herdworks/hypersync-go/internal/actor"
	"github.com/herdworks/hypersync-go/internal/assets"
	"github.com/herdworks/hypersync-go/internal/classify"
	"github.com/herdworks/hypersync-go/internal/doc"
	"github.com/herdworks/hypersync-go/internal/index"
	"github.com/herdworks/hypersync-go/internal/store"
)

type ScanResult struct {
	DocumentsChanged int
	OpsWritten       int
	AssetsStored     int
	Warnings         []string
}

type Status struct {
	ActorID        string
	DocumentCount  int
	AssetCount     int
	ConflictedDocs []string
	Heads          map[string][]doc.OpID
}

func Init(root string) error {
	hs := hypersyncDir(root)
	if err := os.MkdirAll(filepath.Join(hs, "logs", "docs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(hs, "snapshots", "docs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(hs, "assets"), 0o755); err != nil {
		return err
	}
	actorPath := filepath.Join(hs, "actor.json")
	if _, err := os.Stat(actorPath); os.IsNotExist(err) {
		state, err := actor.NewState()
		if err != nil {
			return err
		}
		if err := actor.Save(actorPath, state); err != nil {
			return err
		}
	}
	if _, err := os.Stat(sitePath(root)); os.IsNotExist(err) {
		return saveIndex(root, index.New())
	}
	return nil
}

func Scan(root string) (ScanResult, error) {
	state, idx, err := loadWorkspace(root)
	if err != nil {
		return ScanResult{}, err
	}
	result := ScanResult{}
	seen := map[string]bool{}
	objects := store.NewFS(hypersyncDir(root))
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		cr := classify.Classify(rel)
		if cr.Ignored {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		result.Warnings = append(result.Warnings, cr.Warnings...)
		seen[rel] = true
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		if classify.IsDocumentBucket(cr.Bucket) {
			entry, ok := idx.Get(rel)
			if !ok || entry.DocID == "" {
				id, err := newDocID()
				if err != nil {
					return err
				}
				entry = index.Entry{Path: rel, Bucket: string(cr.Bucket), DocID: id}
			}
			entry.Bucket = string(cr.Bucket)
			entry.Tombstone = false
			ops, current, err := loadDocument(root, entry.DocID)
			if err != nil {
				return err
			}
			nextValue, err := ParseFile(rel, body)
			if err != nil {
				return err
			}
			changes, err := doc.DiffValues(current.Value, nextValue)
			if err != nil {
				return fmt.Errorf("diff %s: %w", rel, err)
			}
			if len(changes) > 0 {
				newOps, err := createOps(&state, entry.DocID, current.Heads, ops, changes)
				if err != nil {
					return err
				}
				if err := appendOps(root, entry.DocID, newOps); err != nil {
					return err
				}
				ops = append(ops, newOps...)
				updated, err := applyOps(entry.DocID, ops)
				if err != nil {
					return err
				}
				if err := saveSnapshot(root, updated); err != nil {
					return err
				}
				result.DocumentsChanged++
				result.OpsWritten += len(newOps)
			}
			idx.Set(entry)
			return nil
		}
		ref, err := assets.Put(context.Background(), objects, body)
		if err != nil {
			return fmt.Errorf("store asset %s: %w", rel, err)
		}
		idx.Set(index.Entry{Path: rel, Bucket: string(cr.Bucket), AssetHash: ref.Hash})
		result.AssetsStored++
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}
	for _, entry := range idx.List() {
		if !seen[entry.Path] && !entry.Tombstone {
			entry.Tombstone = true
			idx.Set(entry)
		}
	}
	if err := actor.Save(actorPath(root), state); err != nil {
		return ScanResult{}, err
	}
	if err := saveIndex(root, idx); err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

func Materialize(root string) error {
	_, idx, err := loadWorkspace(root)
	if err != nil {
		return err
	}
	for _, entry := range idx.List() {
		if entry.Tombstone || entry.DocID == "" {
			continue
		}
		_, current, err := loadDocument(root, entry.DocID)
		if err != nil {
			return err
		}
		body, err := RenderFile(entry.Path, current.Value)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", entry.Path, err)
		}
	}
	return nil
}

func LoadStatus(root string) (Status, error) {
	state, idx, err := loadWorkspace(root)
	if err != nil {
		return Status{}, err
	}
	st := Status{ActorID: state.ActorID, Heads: map[string][]doc.OpID{}}
	assetHashes := map[string]bool{}
	for _, entry := range idx.List() {
		if entry.Tombstone {
			continue
		}
		if entry.DocID != "" {
			st.DocumentCount++
			_, current, err := loadDocument(root, entry.DocID)
			if err != nil {
				return Status{}, err
			}
			st.Heads[entry.DocID] = current.Heads
			if len(current.Conflicts) > 0 {
				st.ConflictedDocs = append(st.ConflictedDocs, entry.DocID)
			}
		}
		if entry.AssetHash != "" {
			assetHashes[entry.AssetHash] = true
		}
	}
	st.AssetCount = len(assetHashes)
	sort.Strings(st.ConflictedDocs)
	return st, nil
}

func LoadLog(root, docID string) ([]doc.Op, error) {
	ops, err := readOps(root, docID)
	if err != nil {
		return nil, err
	}
	return ops, nil
}

func createOps(state *actor.State, docID string, heads []doc.OpID, existing []doc.Op, changes []doc.Change) ([]doc.Op, error) {
	all := map[string]doc.Op{}
	for _, op := range existing {
		all[op.ID.String()] = op
	}
	parents := append([]doc.OpID(nil), heads...)
	newOps := make([]doc.Op, 0, len(changes))
	for _, change := range changes {
		lamports, err := doc.ParentLamports(all, parents)
		if err != nil && len(parents) > 0 {
			return nil, err
		}
		id := doc.OpID{Actor: state.ActorID, Seq: state.NextSeq()}
		op := doc.Op{
			ID:        id,
			DocID:     docID,
			Parents:   append([]doc.OpID(nil), parents...),
			Action:    change.Action,
			Path:      append([]string(nil), change.Path...),
			Value:     change.Value,
			Lamport:   state.NextLamport(lamports...),
			Timestamp: time.Now().Unix(),
		}
		if err := op.Validate(); err != nil {
			return nil, err
		}
		newOps = append(newOps, op)
		all[op.ID.String()] = op
		parents = []doc.OpID{op.ID}
	}
	return newOps, nil
}

func loadWorkspace(root string) (actor.State, index.Index, error) {
	state, err := actor.Load(actorPath(root))
	if err != nil {
		return actor.State{}, index.Index{}, fmt.Errorf("workspace is not initialized: %w", err)
	}
	idx, err := loadIndex(root)
	if err != nil {
		return actor.State{}, index.Index{}, err
	}
	return state, idx, nil
}

func loadDocument(root, docID string) ([]doc.Op, doc.Document, error) {
	ops, err := readOps(root, docID)
	if err != nil {
		return nil, doc.Document{}, err
	}
	current, err := applyOps(docID, ops)
	if err != nil {
		return nil, doc.Document{}, err
	}
	return ops, current, nil
}

func applyOps(docID string, ops []doc.Op) (doc.Document, error) {
	engine := doc.NewEngine()
	return engine.Apply(doc.Document{ID: docID, Ops: map[string]doc.Op{}, Value: map[string]any{}}, ops)
}

func readOps(root, docID string) ([]doc.Op, error) {
	path := docLogPath(root, docID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open log %s: %w", docID, err)
	}
	defer f.Close()
	return doc.ReadJSONL(f)
}

func appendOps(root, docID string, ops []doc.Op) error {
	if len(ops) == 0 {
		return nil
	}
	path := docLogPath(root, docID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log %s: %w", docID, err)
	}
	defer f.Close()
	return doc.WriteJSONL(f, ops)
}

func loadIndex(root string) (index.Index, error) {
	b, err := os.ReadFile(sitePath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return index.New(), nil
		}
		return index.Index{}, fmt.Errorf("read site index: %w", err)
	}
	var idx index.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return index.Index{}, fmt.Errorf("decode site index: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = map[string]index.Entry{}
	}
	return idx, nil
}

func saveIndex(root string, idx index.Index) error {
	if err := os.MkdirAll(hypersyncDir(root), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(sitePath(root), b, 0o644)
}

func saveSnapshot(root string, d doc.Document) error {
	path := filepath.Join(hypersyncDir(root), "snapshots", "docs", d.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc.NewSnapshot(d), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func newDocID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "doc-" + hex.EncodeToString(b[:]), nil
}

func actorPath(root string) string {
	return filepath.Join(hypersyncDir(root), "actor.json")
}

func sitePath(root string) string {
	return filepath.Join(hypersyncDir(root), "site.json")
}

func docLogPath(root, docID string) string {
	clean := strings.ReplaceAll(docID, string(filepath.Separator), "_")
	return filepath.Join(hypersyncDir(root), "logs", "docs", clean+".jsonl")
}

func hypersyncDir(root string) string {
	return filepath.Join(root, ".hypersync")
}
