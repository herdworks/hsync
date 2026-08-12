# hsync

hsync is an HTTP sync tool. It reads synchronization state over ordinary HTTP,
reconciles local changes, and writes provider-neutral changeset files into an
existing deployable payload directory.

The low-level reconciliation API is provider-neutral. `Deployment` creates an
hsync-enabled htup deployment: it prepares sync state, performs
conditional writes, uploads immutable blobs and manifests, and publishes the
mutable synchronization index last.

## Reconciliation algorithm

HSync uses a **lineage-based three-way divergence algorithm** for repository
reconciliation. An HSync repository is a directory containing a set of managed
file paths. Repository state is captured in immutable manifests, which are
snapshots of managed paths and their revisions. A lineage consists of at least
two distinct but related manifests. A generation is one parent-to-child
manifest link. An N-generation lineage has N+1 manifests. A lineage containing
only a parent and child manifest is a direct parent-child lineage. The lineage
for an individual file path is derived from the repository's lineage.

When a repository’s base and live manifests are the same immutable revision, it
is fully reconciled. Subsequent local-workspace edits are relative to that
shared base and are eligible to be published in the next sync event.
Reconciliation is a repository-level operation performed when local (base) and
remote (live) manifests differ. For each managed file path, the algorithm
compares its local workspace revision with the verified lineage from base to
live. When a local workspace revision matches any revision on that lineage, the
algorithm selects the live revision for that path. Genuinely divergent local
workspace revisions are always retained (i.e. local-wins).

## Storage model

Each site has a stable absolute `sync_path` for its mutable index and an
immutable namespace beside it:

```text
<sync_path>
<sync_root>/manifests/<sha256>.json
<sync_root>/blobs/<sha256>
```

Manifest IDs hash their exact serialized JSON bytes. Received or already-hashed
manifest bytes are retained exactly and are never reserialized. A clean index
names one published manifest; pending, stale, malformed, or unverifiable state
blocks publication.

## Library workflow

`Sync` is one unit of work. Its `Repository` identifies managed source files
and the persistent local manifest; its `Payload` identifies an arbitrary
deployable directory. `Sync.Prepare` scans the repository, reads and verifies
live HTTP state, creates a `Changeset`, and applies its exact files to the
payload. It leaves `Changeset` nil when the repository already matches the
clean live manifest. `Sync.Finalize` atomically advances the local manifest
only after deployment has succeeded and the live index names that changeset.

When the local base is behind but reconciliation already matches the live
manifest, `Prepare` safely adopts the exact live bytes without publishing a
redundant revision. `Sync.Status` reports `prepared`, `noop`, `adopted`, or
`finalized`; provider success and rejection remain external outcomes.

## Standalone CLI

The `hsync` command manages downloaded checkouts:

```text
hsync download https://example.com/ ./checkout
hsync status -c ./checkout/hsync.yaml
hsync pull -c ./checkout/hsync.yaml
hsync reset -c ./checkout/hsync.yaml
hsync push
hsync push --config site.yaml
```

`download` treats a non-root URL as a direct synchronization endpoint. For a
root URL, it first tries `/.hsync/index.json`, then falls back to a website
discovery link. It writes the checkout's base to `.hsync/manifest.json`, but
never creates a configuration file. A usable repository must contain a root
configuration file with `sync.remote`; normally that file is one of the
downloaded source files. `status` reports local changes from that checkout's current base, and
`pull` incorporates a newer published revision without replacing conflicting
local work. When a local value exactly matches a revision on the verified
lineage from its local base to the live revision, pull advances that path to
the current live value; otherwise conflicting local work wins. Use `push` with
a generic provider configuration to publish a
checkout. The configuration can be any YAML or JSON document with top-level
`providers`, `environments`, and `sync` keys; hsync ignores unrelated keys.
Existing manifest entries form the initial managed set. `sync.include` selects
additional files to discover and a parentless initial publication requires at
least one positive include-path rule. `sync.ignore` then excludes matching
entries from the complete managed set, including manifest entries.

Path rules and the repository-root `.gitignore` use the same deliberately
small, gitignore-like language: bare names match at any depth, `/` anchors a
rule to the root, a trailing `/` selects directories and descendants, and the
last matching rule wins (including `!` negation). Nested `.gitignore` files,
Git's exact `**` semantics, `.git/info/exclude`, and global Git excludes are
not interpreted. The root `.gitignore` is a final safety exclusion; its
negations cannot override `sync.ignore`. `.git` is always excluded.

`push` uses the directory containing its configuration file as the repository
root and is sync-only. With a detected default configuration and an `hsync`
environment, it requires no arguments; use `--config site.yaml` to select a
custom configuration. It stages the generated changeset under
`.hsync/snapshot/`, deploys only that temporary directory, and removes it when
finished. It never deploys the checkout itself. A repository without a local
manifest may publish only when the remote has no head; that initial manifest
has no parent and is saved locally only after deployment succeeds. If the
remote already has a head, reconcile first.

`reset` restores missing or modified files tracked by the checkout's local
manifest, using immutable blobs from `sync.remote`. It never pulls newer state
or deletes untracked local files. Pass `--interactive` to choose each tracked
file to restore.

Commands resolve configuration with `--config`, then `HSYNC_CONFIG`, then one
of `hsync.yaml`, `hsync.json`, `.hsync.yaml`, or `.hsync.json`. An explicit
configuration file's parent is the repository root. Without an explicit file,
the command discovers a default config in its directory (the current working
directory when none is supplied). A positional directory cannot be combined
with `--config` or `HSYNC_CONFIG`. A configuration is required for `status`,
`pull`, `reset`, and `push`. Provider-backed hsync deployments select the
`hsync` environment by default; `--environment` chooses another environment
and `--providers` selects providers directly.

Manifests contain only immutable synchronization state and are always stored
locally at `.hsync/manifest.json`. `SyncConfig` is external policy: `remote`,
`include`, `ignore`, and either the human-readable `max_file_size` or
normalized `max_file_bytes` limit.
