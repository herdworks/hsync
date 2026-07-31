# HyperSync Go

HyperSync is a manifest/blob reconciliation engine for static-site source
workspaces. It reads published synchronization state over ordinary HTTP,
reconciles local changes, and writes provider-neutral changeset files into an
existing deployable payload directory.

HyperSync does not import or write through HyperProviders. A deployment layer
performs conditional writes, uploads immutable blobs and manifests, and
publishes the mutable synchronization index last. HyperTexting owns the normal
build → prepare → deploy → finalize orchestration.

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

The `hypersync` command supports HyperSync-unaware static-site generators and
CI pipelines:

```text
hypersync init \
  --manifest .sync/manifest.json \
  --sync-path /.hypersync/revision.json \
  --include site.yaml,content/**,data/**,static/** \
  --max-file-size 5MB

hypersync reconcile \
  --source . \
  --manifest .sync/manifest.json \
  --build builds/release \
  --base-url https://example.com/
```

`reconcile` writes immutable changeset objects and `.deployment.json` into the
payload directory. The selected deployment layer must honor those instructions,
then the calling workflow must finalize the local manifest after it verifies a
clean published index.
