# docid — Deterministic document ID helpers

`docid` provides shared helpers to compute deterministic document base IDs
and content hashes using xxh3 hashing. These are usable both inside indexing
graphs and in pre-checks so the two never diverge.

## Functions

```go
func ComputeBaseID(identifier string) (string, error)
func ComputeContentHash(content []byte) string
```

- `ComputeBaseID` — computes a deterministic UUID from a stable identifier
  (e.g. a repo-relative path or a source URL).
- `ComputeContentHash` — computes a hex-encoded 128-bit xxh3 hash from raw
  bytes. Used to decide whether a source has changed.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/libs/docid"

id, err := docid.ComputeBaseID("docs/getting-started.md")
hash := docid.ComputeContentHash(content)
```
