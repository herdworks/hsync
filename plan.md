# HyperSync v1 — Initial Go Implementation Plan

## Handoff — start here (fresh session)

You are implementing this plan from scratch. Before writing code:

1. **Read the two specs in the repo root** — they are the authoritative source of
   truth and this plan only summarizes them:
   - `hypersync.md` — abstract architecture (buckets, materialization, source-of-
     truth inversion, SSG/device independence).
   - `hypersync_initial_go_spec.md` — the detailed v1 spec; section numbers (§N)
     referenced throughout this plan point into it. Note its §29 build order.
2. **Read `hypersync-v1-decisions.md`** in the repo root (also summarized below
   under "Resolved design decisions") — it records the user-approved choices that
   override or disambiguate the spec.
3. **Current repo state**: docs only — the two specs, this `plan.md`, and
   `hypersync-v1-decisions.md`. No `go.mod`, no Go code yet. Go 1.26.x installed.
   Module path is `github.com/herdworks/hypersync-go`.
4. **Where the spec and these decisions disagree, the decisions below win** (e.g.
   Lamport clock replaces the spec's "higher seq" wording; deps policy relaxes the
   spec's strict stdlib-only rule for the materialize layer).
5. **Begin at Phase 0** and work bottom-up; keep `go build ./...` / `go test ./...`
   green at every phase. Use a TaskCreate checklist mirroring the phases below.

## Context

This is a greenfield repo: only design docs exist (`hypersync.md` — abstract
architecture; `hypersync_initial_go_spec.md` — concrete v1 build order;
`hypersync-v1-decisions.md` — resolved decisions; this `plan.md`) plus no Go code
and no `go.mod`. Go 1.26.2 is installed.

We're building **HyperSync**: a *sidecar* sync engine for static-site-generator
source trees. The SSG stays oblivious and reads/writes a normal directory tree;
the engine owns the real source of truth as **append-only JSONL operation logs**
of independent **management documents** and materializes them into files. The v1
goal is a pure-Go, property-level-merge JSON CRDT engine whose document core
(`internal/doc`) can later be swapped for Automerge **without** rewriting the
sidecar, storage, classification, or materialization layers.

The intended outcome of v1 is the spec's §31 acceptance criteria: init a
workspace, scan files into documents, merge concurrent edits deterministically,
preserve conflict losers, compute heads, store ops as JSONL, snapshot, and
materialize back to files.

## Resolved design decisions (user-approved)

1. **Lamport clock (good-enough, not zealot).** Add `Lamport uint64` to `Op`.
   On op creation: `lamport = max(localCounter, max(parentLamports)) + 1`; persist
   the counter in `actor.json`; when ingesting external ops bump
   `counter = max(counter, op.lamport)`. This replaces the spec's ambiguous
   "higher seq" rule (§11.2). Actor-local `seq` is kept **only** for op identity
   and dedup. Conflict winner per property = max `(lamport, actor)`.
2. **String-keyed op maps.** Use `map[string]Op` keyed by canonical
   `OpID.String()` (`<actor>:<seq>`) everywhere — never `map[OpID]Op`.
3. **Markdown = YAML frontmatter + body.** Internal model:
   `{ "frontmatter": { ... }, "body": "..." }`. Frontmatter keys are independent
   mergeable properties; `body` is a scalar string register.
4. **Dependency policy.** The spec's stdlib-only rule holds for the doc engine and
   everything else, with these approved exceptions, all **scoped to
   `internal/materialize`**: `gopkg.in/yaml.v3` (frontmatter) and
   `github.com/yuin/goldmark` (markdown parsing, *as needed*). Plus
   `golang.org/x/...` packages, treated as stdlib and acceptable anywhere.
   Note: v1 keeps the markdown `body` as an opaque string register (the SSG, not
   the engine, renders markdown), so goldmark is likely **not** required for v1 —
   it's available if frontmatter detection or body handling turns out to need a
   real parser rather than `---`-delimiter scanning.

## Package layout (per spec §19, module `github.com/herdworks/hypersync-go`)

```
go.mod                         // go 1.26; require gopkg.in/yaml.v3
cmd/hypersync/main.go          // CLI: init, scan, status, materialize, log
internal/doc/                  // REPLACEABLE Automerge boundary — stdlib only
  id.go        // OpID, String(), Parse, Compare, sort
  op.go        // Op, Action, validation (§23)
  graph.go     // IsAncestor, AreConcurrent, RecomputeHeads, Lamport helpers
  engine.go    // Engine iface, deterministic Apply (§16), Materialize
  merge.go     // property-level merge + conflict tracking (§11)
  diff.go      // value→ops diff (§21)
  snapshot.go  // Snapshot (§14)
internal/actor/                // SURVIVES Automerge swap
  actor.go     // ActorID gen (crypto/rand, 16B hex), Lamport counter
  store.go     // actor.json load/save: {actor_id, seq, lamport}
internal/jsonx/                // stdlib only
  normalize.go // decoded JSON/YAML → {map,[]any,string,float64,bool,nil}
  canonical.go // sorted-key deterministic JSON bytes for hashing/identity
  path.go      // []string property paths (object props only, §10)
internal/index/                // Site Index AS a management document (§4.2)
  entry.go, index.go
internal/classify/             // deterministic bucket precedence (§4.3)
  rules.go, classify.go
internal/store/                // CAS ObjectStore iface + local FS impl (§17,§18)
  object.go, fs.go
internal/assets/               // SHA-256 content-addressed blobs (§26)
  hash.go, store.go
internal/materialize/          // ONLY place yaml.v3 / goldmark may be imported
  scan.go      // workspace → documents
  render.go    // documents → files (json / markdown+frontmatter / opaque body)
```

Hard rule (spec §30): no package outside `internal/doc` may depend on engine
internals (conflict selection, graph traversal). Others depend only on doc ID,
heads, materialized value, conflicts, and ops-as-opaque-records.

## Implementation phases

Bottom-up so the CRDT core is proven before any CLI exists. Each phase ships with
its stdlib `testing` tests (spec §28 matrix).

**Phase 0 — Scaffold.** `go mod init github.com/herdworks/hypersync-go`; add
yaml.v3; commit empty package skeleton; `go build ./... && go test ./...` green.

**Phase 1 — Primitives** (spec steps 1–6)
- `doc/id.go`: `OpID{Actor string; Seq uint64}`, `String()`/`ParseOpID`, total
  order (Lamport-aware compare lives in graph.go; id.go gives the `seq,actor`
  tiebreak). Roundtrip + sort tests.
- `actor/`: `crypto/rand` 16-byte hex actor IDs; `actor.json` =
  `{actor_id, seq, lamport}`; `NextSeq()` monotonic; never reuse seq.
- `jsonx/normalize.go`: coerce decoded values to the 6 allowed types; reject
  others. Handles YAML extras (int→float64, time.Time→RFC3339 string).
- `jsonx/canonical.go`: lexicographically sorted map keys, ordered arrays,
  stdlib number/string/bool/null encoding; reject unsupported types.
- `jsonx/path.go`: `[]string` paths, reject empty components / array-index paths.
- JSONL read/write (in `store` or a small `jsonx`/`doc` helper): skip blanks,
  reject malformed, dedupe by OpID, reject same-ID-different-canonical-content.

**Phase 2 — CRDT engine** (spec steps 7–10, the heart)
- `doc/op.go`: `Op{ID, DocID, Parents, Action, Path, Value, Lamport, Timestamp}`,
  `Action` (put/delete), full `Validate()` (§23). Timestamp is metadata only.
- `doc/graph.go`: `IsAncestor`, `AreConcurrent`, `RecomputeHeads` (all-ids minus
  referenced-parent-ids, sorted), Lamport assignment/ingest helpers.
- `doc/engine.go`: deterministic apply order — topo by parents, then
  `(lamport, actor)`; missing parents = hard error in strict mode (§16).
- `doc/merge.go`: §11 semantics — different props merge; same-prop winner by
  `(lamport, actor)` with losers recorded as `Conflict`; causal overwrite/delete
  produce no conflict; concurrent put-beats-delete (delete kept as conflict);
  arrays as whole-value registers; enforce object root (§11.7).
- `doc/diff.go`: two object values → property ops (§21); recurse into nested
  objects; whole-array `put`; **never** array-element ops.
- `doc/snapshot.go`: `Snapshot{Version,DocID,Heads,Value,Conflicts,OpIndex?}`;
  regenerable from log; log stays authoritative.

**Phase 3 — Storage & assets** (steps 11–12)
- `store/object.go`: `ObjectStore` iface with CAS `PutOptions{IfMatch,
  IfNoneMatch,ContentType}` / `DeleteOptions{IfMatch}`; `store/fs.go` local impl
  using ETag = content hash; layout per §17.
- `assets/`: `crypto/sha256` hashing, `AssetRef{Hash,Size,ContentType}`,
  `net/http.DetectContentType`, sharded `assets/sha256/ab/<hash>` store.

**Phase 4 — Site model & classification** (steps 13–14)
- `index/`: Site Index represented as a management document, processed by the
  same engine; entries track path, bucket, doc ID, asset hash, tombstones.
- `classify/`: deterministic precedence ignore→config→content→data→plugins→
  themes→static; one bucket per path; emit warnings on overlaps.

**Phase 5 — Materialize & CLI** (steps 15–17)
- `materialize/scan.go`: walk workspace, classify, detect changes vs current
  doc value, diff→ops, append to log. `render.go`: doc→file with three
  materializers — canonical pretty JSON (`.json`); Markdown
  (`---`-delimited YAML frontmatter via yaml.v3 + body); opaque
  `{ "body": ... }` for everything else. Note: YAML round-trip won't preserve
  comments/original key order (yaml.v3 sorts map keys → deterministic, accepted).
- `cmd/hypersync/main.go`: `init`, `scan`, `status` (actor id, doc/asset counts,
  conflicted docs, heads), `materialize`, `log <docID>`. No daemon.

**Phase 6 — Snapshots & acceptance** (step 18)
- Wire snapshot read/write into engine load to avoid full replay; final pass
  against §31 acceptance criteria.

## Files to create first (critical path)

`go.mod`, `internal/doc/id.go`, `internal/actor/actor.go` + `store.go`,
`internal/jsonx/{normalize,canonical,path}.go` — these unblock everything and are
each independently unit-testable.

## Verification

- `go build ./...` and `go test ./...` stay green at every phase (stdlib
  `testing` only; spec §28 matrix: opids, heads, property merge, arrays, diff,
  JSONL, canonical JSON, materialization).
- Targeted merge tests assert the §11 worked examples (e.g. `A:7` vs `B:4`
  same-`/title` write → deterministic winner by `(lamport, actor)`, loser in
  `Conflict`; concurrent put/delete → put wins).
- End-to-end smoke in a temp dir:
  `hypersync init` → drop sample `config.yaml` + `content/post.md` (with YAML
  frontmatter) + an image → `hypersync scan` → `hypersync status` shows correct
  buckets/counts → mutate a doc, `scan` again → `hypersync log <docID>` shows
  ops → wipe workspace files → `hypersync materialize` reconstructs identical
  files (proves filesystem-is-a-projection + device-independence).
- Determinism check: feed the same op set in shuffled JSONL line order to the
  engine and assert identical materialized value + heads + conflicts.

## Open notes (non-blocking)

- YAML re-materialization is lossy on comments/formatting — acceptable for v1
  "good enough"; flagged so it isn't a surprise.
- Optional actions (`move`, `increment`, `list_insert/delete`) and real text/list
  CRDTs are explicit non-goals; not implemented.
