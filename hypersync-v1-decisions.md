# HyperSync v1 — Resolved Design Decisions

These are user-approved decisions that **override or disambiguate** the spec in
`hypersync_initial_go_spec.md` where they disagree. Read this alongside that spec
and the architecture doc `hypersync.md`.

## Context

HyperSync is a sidecar sync engine for static-site-generator (SSG) source trees.
v1 is a pure-Go JSON-document CRDT engine with property-level merge, shaped so the
document core (`internal/doc`) can later be swapped for Automerge **without**
rewriting the sidecar, storage, classification, or materialization layers.

## Decisions

### 1. Lamport clock ("good enough", not CRDT-zealot)

Add a `Lamport uint64` field to `Op`.

- On op creation: `lamport = max(localCounter, max(parentLamports)) + 1`.
- Persist the running counter in `actor.json`.
- When ingesting external ops, bump `counter = max(counter, op.lamport)`.
- **Conflict winner per property = max `(lamport, actor)`** (higher Lamport wins;
  ties broken by lexicographically greater actor ID).

This replaces the spec's ambiguous "higher sequence number" wording (§11.2). The
actor-local `seq` is retained **only** for operation identity and dedup, not for
merge ordering.

### 2. String-keyed operation maps

Standardize on `map[string]Op` keyed by the canonical `OpID.String()` form
(`<actor>:<seq>`) everywhere. Do **not** use `map[OpID]Op`.

### 3. Markdown = YAML frontmatter + body

Markdown files are modeled internally as:

```json
{ "frontmatter": { ... }, "body": "..." }
```

Frontmatter keys are independent, mergeable object properties. `body` is a scalar
string register (the engine does not parse/render markdown — the SSG does).

### 4. Dependency policy

The spec's strict "stdlib-only, no third-party deps" rule holds for the document
engine and every other package, with these approved exceptions:

- **Scoped to `internal/materialize` only:**
  - `gopkg.in/yaml.v3` — YAML frontmatter parse/emit.
  - `github.com/yuin/goldmark` — markdown parsing, *as needed* (likely not
    required for v1, since `body` is kept as an opaque string register).
- **Anywhere:** `golang.org/x/...` packages are treated as part of the stdlib.

The document engine (`internal/doc`) and all non-materialize packages remain
stdlib-pure so the Automerge replacement boundary (spec §30) is preserved.
