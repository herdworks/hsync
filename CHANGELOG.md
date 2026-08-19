# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## Unreleased

* Changed `PutOperation.Prepare` to reconcile state without materializing a
  provider plan, and to be idempotent. `Deployment.Run` performs the full
  deployment lifecycle, while `PushOperation.Run` skips provider planning and
  execution when reconciliation is no-op or adopts an existing remote head.
* Changed no-op pull and push CLI commands to report `Already up to date.` and
  `Everything up-to-date`, respectively.

## v0.6.3 (2026-08-19)

* Added `Deployment` and `NewDeployment` as a thin convenience layer for an
  hsync-enabled `htup.Deployment`.

## v0.6.2 (2026-08-19)

* Updated github.com/herdworks/htup to version 0.27.3

## v0.6.1 (2026-08-19)

* Updated LibReleaseDate

## v0.6.0 (2026-08-19)

* **BREAKING:** Reworked the public synchronization model around `Operation`,
  `GetOperation`, and `PutOperation`. `Download`, `Pull`, `Push`, and `Reset`
  now construct, run, and return their respective operations. Removed the former
  `Sync`, hsync `Deployment`, result, and `Payload` APIs rather than retaining
  compatibility shims.
* **BREAKING:** Manifests now serialize `index_path` in place of `sync_path`.
  Existing pre-release remote synchronization state and local
  `.hsync/manifest.json` files must be removed before reinitializing and
  publishing with this release.
* **BREAKING:** Manifests are now sealed values. Construct them with
  `NewManifest` or `NewSealedManifest`, change unsealed state through setters,
  and use accessors for their index path, entries, raw bytes, and identifier.
* **Added:** Explicit repository and remote reconciliation state:
  `Repository.Base`, `Repository.Local`, and `Remote.Head`. `Lineage` and
  `Reconciliation` expose the verified base/local/head comparison used by pull
  and publication.
* **Added:** `Changeset` as the provider-neutral output of reconciliation. Its
  `Apply(root)` method materializes exact publication files directly into a
  deployment directory.
* **Changed:** Pull, download, reset, and push now share operation status,
  remote, repository, logging, and temporary-workspace state. hsync status is
  separate from the nested htup deployment status.
* **Changed:** Remote endpoints resolve to `Remote.IndexURL`; remote reads
  refresh and retain `Remote.Head` rather than returning loose manifest bytes
  and identifiers.

## v0.5.3 (2026-08-18)

* Updated `github.com/herdworks/htup` to v0.27.2 with improved failure reasons.

## v0.5.2 (2026-08-13)

* **Added:** `hsync/cli.Command`, a fresh factory for the standalone hsync
  command tree. Hosts can mount or remix the root and leaf commands without
  sharing command state.
* **Changed:** Command usage, validation errors, and configuration help now
  render the mounted command path and effective environment-variable prefix.
  The standalone CLI uses `HSYNC_CONFIG`; a host can supply its own prefix.
* Updated `github.com/herdworks/clic` to v0.4.1.

## v0.5.1 (2026-08-12)

* Improved reusable CLI command validation so host-remixed command, option,
  and environment-variable names appear correctly in diagnostics.
* Added `-p` as a short form of `hsync push --providers`.

## v0.5.0 (2026-08-12)

* Added an importable `hsync/cli` package with fresh canonical command
  factories for download, init, pull, push, reset, and status. Hosts can mount
  or remix these commands, and the standalone executable now composes them
  through clic.
* Added `LoadSyncConfig` and `ErrSyncConfigNotFound` for loading an explicit
  or discovered validated synchronization configuration.
* Updated the CLI framework dependency to `github.com/herdworks/clic` v0.3.1.
  Hsync commands now provide `-h` / `--help` through clic's `DefaultHelp`.

## v0.4.0 (2026-08-11)

* **BREAKING:** Updated dependencies to `github.com/herdworks/clif` v0.2.0,
  `github.com/herdworks/hq` v0.24.0, `github.com/herdworks/htup` v0.27.1,
  and `github.com/herdworks/hypermedia` v0.2.0. The hsync `Deployment` API
  now uses `htup` types.

## v0.3.2 (2026-08-11)

* Added public `ParsePathExpression` and `PathExpression.Matches` APIs so
  HyperTexting feed selectors use the same gitignore-like path matching as
  hsync configuration.

## v0.3.1 (2026-08-03)

* Updated `github.com/herdworks/hyperproviders-go` to v0.26.0

## v0.3.0 (2026-08-03)

* **Added:** JSON Schema metadata for `SyncConfig` and `Rules`, including a
  named `sync` schema for website configuration tooling.

## v0.2.0 (2026-08-03)

* **Added:** `Deployment`, an hsync-enabled HyperProviders deployment that
  prepares synchronization, delegates publication, and advances the local base
  only after a successful deployment.
* **Added:** A checkout workflow through `hsync download`, `status`, `pull`,
  `reset`, and provider-backed `push`. Downloads verify immutable content;
  pull retains divergent local work; and reset restores only tracked files.
* **Added:** Repository-root `SyncConfig` policy for remote discovery, file
  selection, and size limits, with YAML/JSON configuration discovery.
* **Changed:** Renamed HyperSync to **hsync**: the module path is now
  `github.com/herdworks/hsync`, the executable is `hsync`, and website
  discovery uses `rel="https://hsync.sh"`.
* **Changed:** Renamed the serialized manifest/index schema constant from
  `Version` to `ManifestVersion`, separating it from library release metadata.
* **Changed:** Local bases are stored at `.hsync/manifest.json`, while external
  configuration defines synchronization policy. Root endpoints default to
  `/.hsync/index.json`; custom index paths remain supported.
* **Changed:** Hsync-enabled deployments use HyperProviders' shared payload
  validation, keeping the project configuration directory and top-level Git
  metadata out of provider deployment plans.
* **Removed:** The `hypersync` executable and manifest-embedded scanner
  configuration.
* **Security:** Reject unsafe configuration, workspace, and checkout paths;
  exclude control directories and `.git` from synchronization and deployment
  payloads.

## v0.1.0 (2026-07-31)

* Added the manifest/blob synchronization engine with generic HTTP reads,
  scanner-based reconciliation, immutable changesets, and guarded local
  finalization.
* Added the `Operation`, `Repository`, `Payload`, `Changeset`, `OperationStatus`, and
  `Index` public model. `Index` is the mutable publication index for immutable
  manifests.
* Added the standalone `hypersync init` and `hypersync reconcile` commands for
  HyperSync-unaware site generators and CI pipelines.
