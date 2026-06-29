# contentcomp — Deterministic per-message content compressors

`contentcomp` provides the shared, dependency-free contracts and a small set of
deterministic, opt-in compressors used to shrink noisy message content (mostly
tool outputs) **without** breaking prompt caching.

Design constraints (see the backport plan):

- **Determinism** — every compressor is a pure function of its input. No
  sampling, no statistics (mean/stddev). Unchanged regions stay byte-stable, so
  the cacheable prefix is preserved (Anthropic ~90% / OpenAI ~50% discounts).
- **Reversibility** — any lossy reduction moves the original bytes behind a
  content-addressed handle (`Ref`) backed by a `Store`; nothing is discarded.

## Contracts

```go
type Ref struct { Key string; Size int }

type Store interface {
    Put(ctx context.Context, content string) (Ref, error)
    Get(ctx context.Context, ref Ref) (string, error)
}

type Compressor interface {
    Name() string
    Compress(ctx context.Context, content string) (out string, changed bool, err error)
}
```

`NewMemoryStore()` returns a deterministic, content-addressed (sha256) in-memory
`Store` suitable for tests and single-process use.

## Sub-packages

### `jsoncrush` — lossless JSON crush

Hoists keys common to every row of a JSON **array of objects** into a shared
`_defaults` block, leaving only per-row deviations. Lossless and deterministic
(sorted keys, number fidelity). Non-array / non-object input passes through
unchanged.

```go
out, refs, err := jsoncrush.Crush(ctx, content)       // lossless
expanded, err := jsoncrush.Expand(out)                // canonical JSON

// opt-in lossy stage: near-unique high-entropy columns offloaded to a Store
out, refs, err = jsoncrush.Crush(ctx, content, jsoncrush.WithStore(store))
expanded, err = jsoncrush.ExpandWithStore(ctx, out, store)

c := jsoncrush.NewCompressor() // contentcomp.Compressor adapter
```

The lossy stage is **off by default** (opt-in via `WithStore`).

> Excluded by design: headroom's statistical `SmartCrusher` (mean/stddev
> sampling) — non-deterministic, breaks the prompt cache.

### `shellout` — CLI/log/diff compaction

A small, declarative, deterministic pattern table that collapses noisy terminal
output (carriage-return progress redraws, percentage progress bars, runs of
blank or identical lines). Unmatched content passes through byte-identically.
When a `Store` is configured, the original is preserved and a `Ref` returned.

```go
out, ref, err := shellout.Compress(ctx, content)                 // default table
out, ref, err = shellout.Compress(ctx, content, shellout.WithStore(store))
c := shellout.NewCompressor() // contentcomp.Compressor adapter
```

Extend the table by appending `shellout.Pattern` entries via `WithPatterns`.

## Excluded (see plan)

- ML/Kompress (torch) — heavy, non-deterministic.
- Entropy/density filtering of prose history — destroys semantically important
  text.
- Provider-specific byte mutations (`cache_control`, `prompt_cache_key`, volatile
  tail relocation) — belong in a proxy.
