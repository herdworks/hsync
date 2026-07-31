# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## Unreleased

## v0.1.0 (2026-07-31)

* Added the manifest/blob synchronization engine with generic HTTP reads,
  scanner-based reconciliation, immutable changesets, and guarded local
  finalization.
* Added the `Sync`, `Repository`, `Payload`, `Changeset`, `SyncStatus`, and
  `Index` public model. `Index` is the mutable publication index for immutable
  manifests.
* Added the standalone `hypersync init` and `hypersync reconcile` commands for
  HyperSync-unaware site generators and CI pipelines.
