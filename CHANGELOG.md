# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## Unreleased

* **Added:** `Deployment`, an hsync-enabled HyperProviders deployment. It reads one shared
  repository-root configuration snapshot, prepares optional synchronization,
  delegates provider execution to HyperProviders, and advances the local
  synchronization base only after a successful deployment.
* **Added:** The `hsync` checkout workflow: `download`, `status`, `pull`, `reset`,
  and generic provider-backed `push`. Downloads verify immutable manifests and
  blobs before publishing a checkout; pull performs a local-wins three-way
  merge; reset restores only tracked files and never deletes untracked files.
* **Added:** `SyncConfig` for repository-root `sync` policy, including configurable
  remote index URLs, explicit include/ignore rules, and file-size limits.
  Configuration discovery supports `--config`, `HSYNC_CONFIG`, and the
  conventional YAML or JSON filenames.
* **Changed:** Renamed HyperSync to **hsync**. The module path is now
  `github.com/herdworks/hsync`, the executable is `hsync`, and website
  discovery uses `rel="https://hsync.sh"`.
* **Changed:** Local bases now always live at `.hsync/manifest.json`; synchronization
  policy is no longer embedded in immutable manifests. Existing manifest
  entries remain managed, `sync.include` opts additional files into scanning,
  and `sync.ignore` filters the complete managed set.
* **Changed:** Root remotes use `/.hsync/index.json` by default, while custom index paths
  remain supported. A downloaded checkout must include its own repository-root
  configuration file with `sync.remote`.
* **Removed:** The `hypersync` executable and manifest-embedded scanner
  configuration.
* **Security:** Reject symlinks for configuration, workspace reads, and local writes; never
  add symlinks to manifests or follow them while materializing a checkout.
* **Security:** Reject include patterns that escape or select the repository root, and
  reserve hsync and deployment control directories from synchronization.

## v0.1.0 (2026-07-31)

* Added the manifest/blob synchronization engine with generic HTTP reads,
  scanner-based reconciliation, immutable changesets, and guarded local
  finalization.
* Added the `Sync`, `Repository`, `Payload`, `Changeset`, `SyncStatus`, and
  `Index` public model. `Index` is the mutable publication index for immutable
  manifests.
* Added the standalone `hsync init` command for hsync-unaware site generators
  and CI pipelines.
