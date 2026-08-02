# hsync

hsync is an HTTP sync tool. It reads synchronization state over ordinary HTTP,
reconciles local changes, and writes provider-neutral changeset files into an
existing deployable payload directory.

The low-level reconciliation API is provider-neutral. `Deployment` creates an
hsync-enabled HyperProviders deployment: it prepares sync state, performs
conditional writes, uploads immutable blobs and manifests, and publishes the
mutable synchronization index last.

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
hsync status ./checkout --config hsync.yaml
hsync pull ./checkout --config hsync.yaml
hsync reset ./checkout --config hsync.yaml
hsync push ./checkout --environment production
```

`download` treats a non-root URL as a direct synchronization endpoint. For a
root URL, it first tries `/.hsync/index.json`, then falls back to a website
discovery link. It writes the checkout's base to `.hsync/manifest.json`, but
never creates a configuration file. A usable repository must contain a root
configuration file with `sync.remote`; normally that file is one of the
downloaded source files. `status` reports local changes from that checkout's current base, and
`pull` incorporates a newer published revision without replacing conflicting
local work. Use `push` with a generic provider configuration to publish a
checkout. The configuration can be any YAML or JSON document with top-level
`providers`, `environments`, and `sync` keys; hsync ignores unrelated keys and
has no default scanner policy. Existing manifest entries form the initial
managed set. `sync.include` only selects additional files to discover; an
omitted or empty `sync.include` discovers nothing. `sync.ignore` then excludes
matching entries from the complete managed set, including manifest entries.
Include paths must remain below the repository root. Root-wide patterns such
as `.`, `*`, and `**` are rejected; use an explicit subtree such as
`content/**` when selecting a whole tree.

`push` requires its checkout directory to be the directory containing the
configuration file. It treats that directory as the deployment payload:
hsync prepares any configured synchronization artifacts and HyperProviders
deploys the payload according to the selected environment or explicit provider
names. HyperProviders never deploys its local `.deployment` staging directory.

`reset` restores missing or modified files tracked by the checkout's local
manifest, using immutable blobs from `sync.remote`. It never pulls newer state
or deletes untracked local files. Pass `--interactive` to choose each tracked
file to restore.

Commands resolve configuration with `--config`, then `HSYNC_CONFIG`, then one
of `hsync.yaml`, `hsync.json`, `.hsync.yaml`, or `.hsync.json` at the
repository root. A configuration is required for `status`, `pull`, `reset`,
and `push`.

Manifests contain only immutable synchronization state and are always stored
locally at `.hsync/manifest.json`. `SyncConfig` is external policy: `remote`,
`include`, `ignore`, and either the human-readable `max_file_size` or
normalized `max_file_bytes` limit.
