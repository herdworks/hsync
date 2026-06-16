# SSG Sidecar Sync Engine — Abstract Architecture

## Core Philosophy

The sync system is a **sidecar** to an existing static site generator.

The SSG remains completely unaware that synchronization exists.

The sync engine is responsible for discovering, tracking, synchronizing, and materializing source files.

The SSG simply reads and writes a normal directory tree.

---

## Source of Truth

The source of truth is **not** the filesystem.

The source of truth is a collection of synchronized management records ("documents") maintained by the sync engine.

The filesystem is a materialized projection of those documents.

Conceptually:

```text
Management Documents
        ↓
Filesystem
        ↓
SSG
```

The sync engine is responsible for keeping the management documents and filesystem synchronized.

---

## Site Model

A site consists of:

```text
Site
├── Site Index
├── Config Documents
├── Data Documents
└── Asset References
```

The Site Index is the root object for the site.

---

## Site Index

Every site contains exactly one Site Index.

The Site Index describes:

- Which files are under management
- Which bucket each file belongs to
- The management document associated with each file
- Asset references
- Site-level metadata

The Site Index acts as the authoritative inventory of synchronized content.

A device can reconstruct a site workspace entirely from the Site Index and associated documents.

---

## Buckets

Every synchronized source file belongs to exactly one bucket.

### Config Bucket

The Config Bucket contains site configuration files.

Characteristics:

- Explicit file list
- Non-recursive
- Structured content
- Synchronized via management documents

Examples:

```text
config.yaml
site.yaml
hugo.yaml
```

### Data Bucket

The Data Bucket contains user-authored content and structured data.

Characteristics:

- Recursive discovery
- Rule-based inclusion
- Synchronized via management documents

Examples:

```text
content/**/*.md
data/**/*.yaml
data/**/*.json
```

The sync engine does not understand the semantics of the content.

A blog post, page, feed item, or product record are all simply managed data files.

### Static Asset Bucket

The Static Asset Bucket contains opaque binary assets.

Characteristics:

- Catch-all bucket
- Everything not classified as config, data, or ignored
- Synchronized by reference

Examples:

```text
png
jpg
gif
webp
svg
css
js
woff2
mp4
pdf
```

The sync engine manages metadata and references for assets, but not their internal structure.

---

## Ignore Rules

Ignore rules exclude files from synchronization.

Ignored files are not represented in the Site Index.

Examples:

```text
.git
node_modules
build outputs
generated artifacts
cache directories
```

---

## Classification Model

Files are classified using deterministic precedence rules.

Order:

```text
ignore
config
data
static
```

Earlier matches take precedence over later matches.

A file may match multiple rules, but ultimately resolves to a single bucket.

Configuration overlaps produce warnings.

---

## Management Documents

Each synchronized config or data file is represented by an independent management document.

Conceptually:

```text
File
    ↔
Management Document
```

The management document contains the canonical synchronized representation of the file.

Each file is managed independently.

There is no monolithic site document.

---

## Asset References

Static assets are represented by asset references.

Conceptually:

```text
Asset Reference
      ↓
Asset Content
```

The synchronization system tracks the existence and identity of assets separately from their contents.

Assets may be stored, transferred, cached, or retrieved independently of the Site Index.

---

## Materialization

The sync engine materializes management documents into ordinary files.

Examples:

```text
Management Document
        ↓
content/blog/post.md
```

```text
Management Document
        ↓
config.yaml
```

```text
Asset Reference
        ↓
image.png
```

The SSG interacts only with materialized files.

---

## Discovery

The sync engine discovers changes to the workspace and updates the corresponding management documents.

Similarly, when synchronized state changes, the sync engine updates the materialized filesystem.

Synchronization is therefore bidirectional:

```text
Filesystem
     ↔
Management Documents
```

---

## Device Independence

Any device can reconstruct a complete workspace from synchronized state.

A newly provisioned device requires only:

```text
Site Index
Management Documents
Asset References
Asset Content
```

to recreate the full site workspace.

No SSG-specific knowledge is required.

---

## SSG Independence

The synchronization model is intentionally decoupled from any specific static site generator.

The sync engine understands:

- Files
- Directories
- Buckets
- Documents
- Assets

It does not understand:

- Blog posts
- Themes
- Pages
- Collections
- Taxonomies
- Frontend frameworks

All SSG-specific behavior remains outside the synchronization layer.

---

## Replication Model

The system is designed around independently synchronized documents.

Conceptually:

```text
Site Index
    ↓
Document Inventory
    ↓
Individual Documents
```

Each document can evolve, synchronize, merge, and resolve conflicts independently.

This aligns naturally with document-oriented synchronization systems such as CRDTs while remaining implementation-agnostic.

---

## Design Goal

The primary design goal is:

> Allow existing static site generators and file-based workflows to participate in local-first synchronization without requiring any modification to the SSG itself.

The synchronization layer owns synchronization.

The SSG owns site generation.

The filesystem remains the compatibility boundary between them.
