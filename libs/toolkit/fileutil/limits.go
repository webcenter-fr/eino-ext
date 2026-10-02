package fileutil

// Default size limits for file operations. Components may use these directly
// or define their own.
const (
	DefaultMaxReadBytes       = 1 << 20  // 1MB  — truncation threshold for reads.
	DefaultMaxWriteBytes      = 10 << 20 // 10MB — max content size for writes.
	DefaultMaxSearchFileBytes = 10 << 20 // 10MB — skip files larger than this in search.
	DefaultMaxCopyBytes       = 50 << 20 // 50MB — max total bytes for directory copy/move.
)
