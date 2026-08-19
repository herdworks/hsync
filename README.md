# hsync

hsync is an HTTP synchronization library and CLI. It reads published state over
ordinary HTTP, reconciles a local repository, and materializes provider-neutral
changeset files into an existing deployable directory.

The low-level reconciliation API is provider-neutral. `PutOperation` wraps an
htup `Deployment` with hsync state: it prepares sync state, performs
conditional writes, uploads immutable blobs and manifests, and publishes the
mutable synchronization index last.

## Reconciliation algorithm

hsync uses a **lineage-based three-way divergence algorithm** for repository
reconciliation. A repository is a directory containing a set of managed file
paths. Its persistent base is an immutable manifest, and its ephemeral local
state is an inventory of the selected files currently on disk. A remote's head
is the immutable manifest currently named by its published index. A lineage
consists of at least two distinct but related manifests. A generation is one
parent-to-child manifest link. An N-generation lineage has N+1 manifests. A
lineage containing only a parent and child manifest is a direct parent-child
lineage. The lineage for an individual file path is derived from the
repository's lineage.

When `Repository.Base` and `Remote.Head` are the same immutable revision, the
repository is fully reconciled. Subsequent `Repository.Local` edits are
relative to that shared base and are eligible to be published. For each managed
path, reconciliation compares base, local, and head. When a local value matches
any revision on the verified lineage from base to head, the algorithm selects
the head value. Genuinely divergent local values are retained (local-wins).

## Storage model

Each site has a stable absolute `index_path` for its mutable index and an
immutable namespace beside it. Each manifest records the complete managed file
inventory for one repository revision:

```text
<index_path>
<sync_root>/manifests/<sha256>.json
<sync_root>/blobs/<sha256>
```

Manifest IDs hash their exact serialized JSON bytes. Received or already-hashed
manifest bytes are retained exactly and are never reserialized. A clean index
names one published manifest; pending, stale, malformed, or unverifiable state
blocks publication.

### Pre-release state migration

Manifests now serialize their index location as `index_path`. State created by
earlier pre-release releases that used `sync_path` is intentionally
incompatible. Remove the old remote synchronization state and the local
`.hsync/manifest.json`, then initialize or download the repository and publish
a new revision.

## Library workflow

`Operation` supplies shared repository, remote, logging, temporary-workspace,
and status state. `GetOperation` reads a remote revision into a repository;
`PutOperation` wraps an htup `Deployment` to publish a repository revision.
`Download`, `Pull`, `Push`, and `Reset` each construct, run, and return their
respective operation, so callers can inspect the completed operation directly.

During preparation, `PutOperation` snapshots the repository, reads and
verifies remote head state, creates a `Changeset`, and materializes its exact
files into the nested deployment directory. It leaves `Changeset` nil when the
repository already matches the clean head manifest. It atomically advances the
local base only after the nested deployment succeeds and the head index names
that changeset.

When the local base is behind but reconciliation already matches the head
manifest, preparation safely adopts the exact head bytes without publishing a
redundant revision. `Operation.Status` records the hsync workflow outcome;
htup separately reports provider deployment success or rejection.

## Standalone CLI

The `hsync` command manages downloaded repositories:

```text
hsync download https://example.com/ ./site
hsync status -c ./site/hsync.yaml
hsync pull -c ./site/hsync.yaml
hsync reset -c ./site/hsync.yaml
hsync push
hsync push --config site.yaml
```

`download` treats a non-root URL as a direct synchronization endpoint. For a
root URL, it first tries `/.hsync/index.json`, then falls back to a website
discovery link. It writes the downloaded repository's base to
`.hsync/manifest.json`, but never creates a configuration file. A usable
repository must contain a root configuration file with `sync.remote`; normally
that file is one of the downloaded source files. `status` reports local changes
from that repository's current base, and `pull` incorporates a newer published
revision without replacing conflicting local work. When a local value exactly
matches a revision on the verified lineage from its local base to the head
revision, pull advances that path to the current head value; otherwise
conflicting local work wins. Use `push` with a generic provider configuration
to publish a repository. The configuration can be any YAML or JSON document
with top-level `providers`, `environments`, and `sync` keys; hsync ignores
unrelated keys.
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
`.hsync/tmp/<operation-id>/`, deploys only that temporary directory, and
removes it when finished. It never deploys the repository itself. A repository without a local
manifest may publish only when the remote has no head; that initial manifest
has no parent and is saved locally only after deployment succeeds. If the
remote already has a head, reconcile first.

`reset` restores missing or modified files tracked by the repository's local
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
