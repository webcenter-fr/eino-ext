// Package contentcomp defines the shared, dependency-free contracts used by the
// deterministic per-message content compressors (jsoncrush, shellout) and by the
// contextopt middleware that orchestrates them.
//
// Design constraints (see .kilo/plans/…context-opt-backport-plan.md):
//   - Determinism: every compressor is a pure function of its input. No
//     sampling, no statistics (mean/stddev), so the prompt-cache prefix stays
//     byte-stable outside the compressed regions.
//   - Reversibility: any lossy reduction moves the original bytes behind a
//     content-addressed handle (Ref) backed by a Store, never discarding them.
package contentcomp

import "context"

// Ref is a content-addressed handle to data moved out of band by a Store. It is
// deterministic: the same content always yields the same Key.
type Ref struct {
	// Key is the content-addressed identifier (e.g. "sha256:<hex>").
	Key string
	// Size is the original byte length of the stored content.
	Size int
}

// Store persists original content and returns a content-addressed handle so a
// lossy reduction remains reversible. Implementations must be deterministic:
// Put(x) returns the same Key for the same x.
//
// The interface is intentionally minimal so it can be satisfied by an in-memory
// map, a filesystem backend, or adapted from adk/filesystem.Backend.
type Store interface {
	// Put stores content and returns its content-addressed handle. Putting the
	// same content twice is idempotent.
	Put(ctx context.Context, content string) (Ref, error)
	// Get retrieves content previously stored under ref.
	Get(ctx context.Context, ref Ref) (string, error)
}

// Compressor deterministically rewrites a single message content string into a
// smaller, equivalent form. When it cannot meaningfully compress the input it
// must return content unchanged (and changed == false), so unknown payloads pass
// through byte-identically (cache-safe).
//
// A Compressor that performs a lossy reduction is responsible for preserving the
// original bytes behind a Ref (via a Store provided at construction time).
type Compressor interface {
	// Name identifies the compressor (used in markers / diagnostics).
	Name() string
	// Compress returns the compressed form of content. It must be deterministic
	// and idempotent: Compress(Compress(x)) == Compress(x).
	Compress(ctx context.Context, content string) (out string, changed bool, err error)
}
