# HyperSync Initial Go Implementation Spec

## 1. Purpose

Build an initial pure-Go synchronization engine for HyperSync that supports JSON-shaped management documents with property-level merge behavior.

The implementation must use only the Go standard library. No third-party packages are allowed in the initial version.

The prototype does not need to match Automerge's robustness, binary format, text CRDTs, list CRDTs, or sync protocol. It must, however, preserve the same broad architectural shape so the document engine can later be replaced with Automerge-derived components without rewriting the sidecar, storage, discovery, classification, or materialization layers.

## 2. Design Goals

- Implement a sidecar sync engine for static-site-generator source trees.
- Treat the filesystem as a materialized projection, not the source of truth.
- Represent each managed config/data file as an independent JSON-shaped management document.
- Merge concurrent changes at the property level for nested JSON objects.
- Preserve deterministic convergence across devices.
- Preserve losing values in conflict metadata instead of failing sync.
- Use append-only operation logs with actor IDs, Lamport sequence numbers, and parent heads.
- Keep the document engine isolated behind interfaces so it can later be replaced.
- Use JSON and JSONL for all initial storage formats.

## 3. Non-Goals

The initial implementation must not attempt to provide:

- Full Automerge compatibility.
- Automerge binary encoding.
- Character-level text merging.
- Rich list/array CRDT behavior.
- Multi-user permissions.
- End-to-end encryption.
- Schema-aware semantic merges.
- Real-time websocket sync.
- Database-backed storage.
- Third-party dependencies.

## 4. Core Concepts

### 4.1 Site

A site is a synchronized workspace consisting of:

- One Site Index document.
- Config management documents.
- Data management documents.
- Static asset references.
- Static asset content blobs.

### 4.2 Site Index

The Site Index is the authoritative inventory of synchronized files.

It tracks:

- Managed file paths.
- Bucket classification.
- Document IDs.
- Asset IDs or content hashes.
- Deleted/tombstoned entries.
- Optional metadata.

The Site Index itself must be represented as a management document and processed by the same document engine as other JSON documents.

### 4.3 Buckets

Initial buckets:

- `config`
- `content`
- `data`
- `plugins`
- `static`
- `themes`

Bucket classification rules are deterministic and evaluated in precedence order:

1. ignore
2. config
3. content
4. data
5. plugins
6. themes
7. static

A path resolves to exactly one bucket.

### 4.4 Management Document

A management document is a JSON-shaped document associated with one managed source file or system object.

The document value is conceptually:

```go
type JSONValue = any
```

In practice, the implementation must normalize decoded JSON into these Go types only:

- `map[string]any`
- `[]any`
- `string`
- `float64`
- `bool`
- `nil`

Maps are mergeable at the property level.

Arrays are treated as scalar register values in v1.

### 4.5 Static Assets

Static assets are opaque blobs addressed by SHA-256 content hash.

Asset references are stored in JSON documents. Asset content itself is not merged.

## 5. Actor Model

Each device/replica has a stable actor ID.

Actor IDs must be generated using `crypto/rand` and encoded as lowercase hex.

Recommended size:

```text
16 random bytes = 32 hex characters
```

Actor state must be persisted locally:

```json
{
  "actor_id": "...",
  "seq": 42
}
```

Each actor produces a linear sequence of operations.

The same actor ID must never produce duplicate sequence numbers.

## 6. Operation IDs

Each operation has an ID composed of actor ID and actor-local sequence number.

```go
type OpID struct {
    Actor string `json:"actor"`
    Seq   uint64 `json:"seq"`
}
```

The canonical string form is:

```text
<actor>:<seq>
```

Example:

```text
7f9a2c...:12
```

## 7. Parent Heads

Each operation records the document heads known at the time the operation was created.

```go
type Op struct {
    ID      OpID   `json:"id"`
    DocID   string `json:"doc_id"`
    Parents []OpID `json:"parents"`
    Action  string `json:"action"`
    Path    []string `json:"path"`
    Value   any    `json:"value,omitempty"`
}
```

Parent heads provide the causal basis for the operation.

They are used to determine whether two operations are causally ordered or concurrent.

If operation B includes operation A in its ancestry, B supersedes A for the same property.

If neither operation includes the other in its ancestry, the operations are concurrent.

## 8. Document Heads

A document's heads are the set of operations that have no known child operation in that document's operation graph.

A document may have:

- Zero heads for an empty document.
- One head for a linear history.
- Multiple heads after concurrent edits.

After applying operations, recompute heads as:

```text
all operation ids minus all parent ids referenced by known operations
```

Heads must be sorted deterministically before serialization.

## 9. Operation Format

### 9.1 Operation Struct

```go
type Op struct {
    ID        OpID     `json:"id"`
    DocID     string   `json:"doc_id"`
    Parents   []OpID   `json:"parents"`
    Action    Action   `json:"action"`
    Path      []string `json:"path"`
    Value     any      `json:"value,omitempty"`
    Timestamp int64    `json:"timestamp,omitempty"`
}
```

`Timestamp` is metadata only. It must not be used for merge correctness.

### 9.2 Actions

Initial actions:

```go
type Action string

const (
    ActionPut    Action = "put"
    ActionDelete Action = "delete"
)
```

Optional later actions:

- `move`
- `increment`
- `list_insert`
- `list_delete`

Do not implement optional actions in v1.

### 9.3 Put

A `put` operation sets the value at a JSON path.

Example:

```json
{
  "id": { "actor": "device-a", "seq": 4 },
  "doc_id": "doc-123",
  "parents": [{ "actor": "device-a", "seq": 3 }],
  "action": "put",
  "path": ["frontmatter", "title"],
  "value": "Hello World"
}
```

### 9.4 Delete

A `delete` operation deletes the value at a JSON path.

Example:

```json
{
  "id": { "actor": "device-b", "seq": 9 },
  "doc_id": "doc-123",
  "parents": [{ "actor": "device-a", "seq": 4 }],
  "action": "delete",
  "path": ["frontmatter", "draft"]
}
```

## 10. JSON Path Rules

Paths are represented as string slices.

Examples:

```text
[]                          root
["title"]                   /title
["frontmatter", "title"]    /frontmatter/title
["frontmatter", "tags"]     /frontmatter/tags
```

V1 paths must address object properties only.

Array element paths are not supported in v1.

Invalid path examples:

```text
["tags", "0"]
["items", "12", "name"]
```

Arrays may be assigned as whole values but may not be edited element-by-element.

## 11. Property-Level Merge Semantics

### 11.1 Object Properties

Nested object properties merge independently.

Given base:

```json
{
  "frontmatter": {
    "title": "Old",
    "draft": true
  }
}
```

Concurrent operations:

```text
A puts /frontmatter/title = "New Title"
B puts /frontmatter/draft = false
```

Merged value:

```json
{
  "frontmatter": {
    "title": "New Title",
    "draft": false
  }
}
```

### 11.2 Same Property Conflict

Concurrent writes to the same property are resolved deterministically.

Rule:

1. Prefer the operation with the higher Lamport sequence number.
2. If sequence numbers are equal, prefer the lexicographically greater actor ID.
3. Preserve losing values in conflict metadata.

Example:

```text
A:7 puts /title = "Hello"
B:4 puts /title = "Bonjour"
```

Winner:

```json
{
  "title": "Hello"
}
```

Conflict metadata:

```json
{
  "/title": [
    {
      "op": { "actor": "device-b", "seq": 4 },
      "value": "Bonjour"
    }
  ]
}
```

### 11.3 Causal Overwrite

If operation B causally descends from operation A, B overwrites A without conflict.

Example:

```text
A:1 puts /title = "Draft"
A:2 puts /title = "Final" with parent A:1
```

Result:

```json
{
  "title": "Final"
}
```

No conflict is recorded.

### 11.4 Concurrent Delete vs Put

For v1, `put` beats `delete` when concurrent.

The delete operation is preserved in conflict metadata.

Rationale: accidental data loss is worse than accidental resurrection for the target single-user, multi-device workflow.

### 11.5 Causal Delete

If a delete causally descends from the current winning put, the property is deleted.

No conflict is recorded.

### 11.6 Arrays

Arrays are scalar register values.

Concurrent writes to the same array property are resolved using the same deterministic winner rule as strings, numbers, booleans, and null.

Array elements are not merged independently.

### 11.7 Root Replacement

Replacing the root value is allowed only when the root value is an object.

If a root replacement would make the document root a scalar or array, return an error.

V1 management documents must have object roots.

## 12. Conflict Metadata

Conflicts must not prevent synchronization.

The document engine must always produce a deterministic materialized value.

Losing values must remain available through:

1. Operation history.
2. Materialized conflict metadata returned by the engine.

Do not write `_conflicts` into user-authored JSON by default.

Expose conflicts separately:

```go
type Conflict struct {
    Path   []string `json:"path"`
    Winner OpID     `json:"winner"`
    Loser  OpID     `json:"loser"`
    Value  any      `json:"value"`
    Action Action   `json:"action"`
}
```

Materialization layers may choose whether to render conflict sidecars later.

Initial behavior:

- Sync succeeds.
- Winner is materialized.
- Conflicts are logged.
- Losers remain recoverable from the operation log.

## 13. Causal Graph

The engine must maintain enough operation graph state to answer ancestry questions.

Required functions:

```go
func IsAncestor(ops map[OpID]Op, ancestor OpID, descendant OpID) bool
func AreConcurrent(ops map[OpID]Op, a OpID, b OpID) bool
func RecomputeHeads(ops map[OpID]Op) []OpID
```

Concurrency rule:

```text
A and B are concurrent if A is not an ancestor of B and B is not an ancestor of A.
```

Implementation may use simple graph traversal in v1.

Optimization is not required initially.

## 14. Document Snapshot Format

A snapshot stores the current materialized document plus enough metadata to avoid replaying the entire log for every read.

```go
type Snapshot struct {
    Version   int              `json:"version"`
    DocID     string           `json:"doc_id"`
    Heads     []OpID           `json:"heads"`
    Value     map[string]any   `json:"value"`
    Conflicts []Conflict       `json:"conflicts,omitempty"`
    OpIndex   map[string]OpRef `json:"op_index,omitempty"`
}
```

V1 may omit `OpIndex` if replay performance is acceptable.

Snapshots are an optimization, not the source of truth.

The operation log remains authoritative.

## 15. Operation Log Format

Use JSON Lines.

Each line is one operation encoded as compact JSON.

Example:

```jsonl
{"id":{"actor":"a","seq":1},"doc_id":"doc-1","parents":[],"action":"put","path":["title"],"value":"Hello"}
{"id":{"actor":"b","seq":1},"doc_id":"doc-1","parents":[{"actor":"a","seq":1}],"action":"put","path":["draft"],"value":false}
```

Requirements:

- Ignore blank lines.
- Reject malformed JSON lines.
- Deduplicate operations by operation ID.
- Validate that the same operation ID never appears with different content.
- Sort operations deterministically before applying.

## 16. Deterministic Apply Order

The engine must converge regardless of log line order.

Apply order:

1. Topologically order operations by parent dependencies when possible.
2. For operations not causally related, sort by `Seq`, then `Actor`.
3. Reject operations with missing parents unless caller explicitly allows partial documents.

For initial local operation logs, missing parents should be treated as an error.

## 17. Storage Layout

Initial local filesystem storage:

```text
.hypersync/
  actor.json
  site.json
  logs/
    site-index.jsonl
    docs/
      <docID>.jsonl
  snapshots/
    docs/
      <docID>.json
  assets/
    sha256/
      ab/
        <fullhash>
```

Future HTTP/object-store layout should be compatible with:

```text
/hypersync/site.json
/hypersync/log/<bucket>/<shard>.jsonl
/hypersync/docs/<docID>/snapshot.json
/hypersync/assets/sha256/<prefix>/<hash>
```

## 18. CAS Storage Interface

Define a storage interface that can later support HTTP `If-Match` / `If-None-Match` style writes.

```go
type ObjectStore interface {
    Get(ctx context.Context, key string) (Object, error)
    Put(ctx context.Context, key string, body []byte, opts PutOptions) (Object, error)
    Delete(ctx context.Context, key string, opts DeleteOptions) error
    List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

type Object struct {
    Key  string
    Body []byte
    ETag string
}

type PutOptions struct {
    IfMatch     string
    IfNoneMatch bool
    ContentType string
}

type DeleteOptions struct {
    IfMatch string
}
```

Initial implementation may provide only local filesystem storage, but the interface should preserve CAS semantics.

## 19. Package Layout

Recommended package layout:

```text
cmd/hypersync/
  main.go

internal/actor/
  actor.go
  store.go

internal/doc/
  op.go
  id.go
  engine.go
  graph.go
  merge.go
  diff.go
  snapshot.go

internal/index/
  index.go
  entry.go

internal/classify/
  rules.go
  classify.go

internal/materialize/
  render.go
  scan.go

internal/assets/
  hash.go
  store.go

internal/store/
  object.go
  fs.go

internal/jsonx/
  canonical.go
  path.go
  normalize.go
```

## 20. Public Internal Interfaces

### 20.1 Document Engine

```go
type Engine interface {
    Apply(base Document, ops []Op) (Document, error)
    Diff(docID string, oldValue, newValue map[string]any, parents []OpID) ([]Op, error)
    Materialize(doc Document) (map[string]any, []Conflict, error)
}
```

### 20.2 Document

```go
type Document struct {
    ID        string
    Heads     []OpID
    Ops       map[OpID]Op
    Value     map[string]any
    Conflicts []Conflict
}
```

If `map[OpID]Op` is awkward because Go map keys require comparable values and JSON marshaling needs string keys, use:

```go
map[string]Op
```

with canonical `OpID.String()` keys.

## 21. Diff Behavior

The diff engine compares two JSON object values and produces property-level operations.

Rules:

- Added property => `put`
- Removed property => `delete`
- Changed scalar value => `put`
- Changed array value => `put`
- Changed object property => recursively diff nested object
- Replaced object with scalar/array => `put` at object path
- Replaced scalar/array with object => `put` at property path

Do not produce array element operations.

## 22. Canonical JSON

Implement canonical JSON helpers using only the standard library.

Goals:

- Deterministic hashing.
- Deterministic operation identity validation.
- Stable tests.

Rules:

- Sort map keys lexicographically.
- Encode arrays in order.
- Use standard `encoding/json` string/number/bool/null behavior.
- Reject unsupported Go value types before encoding.

## 23. Operation Validation

Validate every operation before applying.

Rules:

- `DocID` must not be empty.
- `ID.Actor` must not be empty.
- `ID.Seq` must be greater than zero.
- `Action` must be known.
- `Path` must not contain empty components.
- `Path` must not address array elements.
- `put` must include a valid JSON value.
- `delete` must not require a value.
- Parent IDs must not contain duplicates.
- An operation must not list itself as a parent.

## 24. Failure Behavior

Synchronization should prefer deterministic recovery over hard failure.

Hard failures:

- Malformed JSONL.
- Duplicate operation ID with different canonical content.
- Missing parent in strict mode.
- Invalid operation shape.
- Unsupported JSON value type.
- Filesystem write failure.

Soft conflicts:

- Concurrent same-property writes.
- Concurrent put/delete.
- Concurrent array replacements.
- Concurrent scalar replacements.

Soft conflicts must not stop sync.

## 25. Materialization

Materialization converts management documents into normal files.

Initial supported materializers:

### 25.1 JSON

Write canonical pretty JSON for `.json` files.

### 25.2 Markdown with JSON Frontmatter Model

Represent Markdown documents internally as:

```json
{
  "frontmatter": {},
  "body": "Markdown body..."
}
```

For v1, support a simple JSON frontmatter format only, or defer Markdown parsing entirely.

Recommended v1 simplification:

- Treat Markdown files as JSON-shaped documents only if they are created through the engine.
- Otherwise represent Markdown files as:

```json
{
  "body": "entire file contents"
}
```

This keeps the initial parser pure standard library and avoids YAML dependency.

### 25.3 Opaque Text

For unsupported text formats:

```json
{
  "body": "entire file contents"
}
```

The `body` field is a scalar string register.

## 26. Assets

Asset storage must compute SHA-256 content hashes using `crypto/sha256`.

Asset reference shape:

```go
type AssetRef struct {
    Hash        string `json:"hash"`
    Size        int64  `json:"size"`
    ContentType string `json:"content_type,omitempty"`
}
```

Content type may be detected with `net/http.DetectContentType` when useful.

## 27. Initial CLI

Provide a minimal CLI.

Commands:

```text
hypersync init <path>
hypersync scan <path>
hypersync status <path>
hypersync materialize <path>
hypersync log <path> <docID>
```

No daemon is required for v1.

### 27.1 init

Creates:

```text
.hypersync/actor.json
.hypersync/logs/
.hypersync/snapshots/
.hypersync/assets/
```

### 27.2 scan

Scans the workspace, classifies files, detects changes, updates management documents, and appends operations.

### 27.3 status

Shows:

- Actor ID.
- Managed document count.
- Asset count.
- Documents with conflicts.
- Current heads per document.

### 27.4 materialize

Renders current management documents to the workspace.

### 27.5 log

Prints known operations for a document.

## 28. Testing Requirements

Use only Go's standard `testing` package.

Required tests:

### 28.1 Operation IDs

- Generate monotonic sequence numbers.
- String parse/format roundtrip.
- Sort order is deterministic.

### 28.2 Heads

- Linear history has one head.
- Concurrent branches produce multiple heads.
- Merge operation with both parents returns to one head.

### 28.3 Property Merge

- Concurrent edits to different top-level properties merge.
- Concurrent edits to different nested properties merge.
- Concurrent edits to same property produce deterministic winner and conflict.
- Causal overwrite does not produce conflict.
- Causal delete deletes property.
- Concurrent put/delete chooses put and records conflict.

### 28.4 Arrays

- Concurrent array replacement chooses deterministic winner.
- Losing array is recorded as conflict.

### 28.5 JSON Diff

- Added property creates put.
- Removed property creates delete.
- Nested property change creates nested put.
- Array element changes create whole-array put.

### 28.6 JSONL

- Blank lines ignored.
- Malformed lines fail.
- Duplicate identical ops dedupe.
- Duplicate conflicting ops fail.

### 28.7 Canonical JSON

- Map key ordering is deterministic.
- Equal logical values produce equal bytes.

### 28.8 Materialization

- JSON document materializes to expected file bytes.
- Unsupported text file roundtrips through `body`.

## 29. Implementation Sequence

Implement in this order:

1. `OpID` parse, format, compare, sort.
2. Actor state load/save and sequence generation.
3. JSON normalization and canonical JSON encoding.
4. JSON path helpers.
5. Operation validation.
6. JSONL read/write.
7. Causal graph and head recomputation.
8. Document apply engine.
9. Property-level merge and conflict tracking.
10. Diff engine.
11. Local filesystem object store.
12. Asset hashing/storage.
13. Site index model.
14. Classification rules.
15. Scan command.
16. Materialize command.
17. Status/log commands.
18. Snapshots.

## 30. Automerge Replacement Boundary

The following packages should be considered replaceable later:

```text
internal/doc
internal/jsonx/path.go, if Automerge paths replace local paths
```

The following packages should survive an Automerge replacement:

```text
internal/actor
internal/index
internal/classify
internal/materialize
internal/assets
internal/store
cmd/hypersync
```

Do not let other packages depend on document-engine internals such as conflict selection or graph traversal.

They should depend only on:

- document ID
- heads
- materialized value
- conflicts
- changes/operations as opaque records where possible

## 31. Acceptance Criteria

The initial implementation is complete when:

1. A workspace can be initialized.
2. Files can be scanned into management documents.
3. JSON-shaped documents can be changed and represented as operations.
4. Concurrent operations on different properties merge cleanly.
5. Concurrent operations on the same property converge deterministically.
6. Losing values are preserved in conflict metadata.
7. Document heads are computed correctly.
8. Operations are stored as JSONL.
9. Snapshots can be generated and loaded.
10. Managed documents can be materialized back to normal files.
11. The entire project builds and tests with only the Go standard library.

## 32. Coding Standards

- Prefer simple, idiomatic Go.
- Keep functions small and focused.
- Return errors with context.
- Do not log and return the same error.
- Avoid global mutable state.
- Use early returns to avoid deep nesting.
- Keep serialization explicit and tested.
- Use deterministic ordering anywhere output is serialized.
- Do not introduce third-party dependencies.

