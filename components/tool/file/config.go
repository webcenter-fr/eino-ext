package file

import "time"

// Config holds configuration for the file tools.
type Config struct {
	// Workdir is the base directory for session-scoped file operations.
	// Must be an absolute path, not a system directory. Required.
	Workdir string `validate:"required" jsonschema:"description=Base directory for session-scoped file operations (must be an absolute path)"`

	// MaxReadBytes is the maximum number of bytes to read from a file.
	// Files larger than this are truncated with a note. Defaults to 1MB.
	MaxReadBytes int `validate:"omitempty,gte=1" jsonschema:"description=Maximum bytes to read from a file (default 1MB)"`

	// MaxWriteBytes is the maximum content size accepted for file_write.
	// Defaults to 10MB.
	MaxWriteBytes int `validate:"omitempty,gte=1" jsonschema:"description=Maximum content size for file_write (default 10MB)"`

	// SessionTTL is the maximum age of a session directory before it is
	// eligible for garbage collection. When set, the GC (started via
	// StartGC) removes session subdirectories whose modification time is
	// older than this duration. Active sessions are kept fresh by
	// TouchSession. Zero means no GC.
	SessionTTL time.Duration `validate:"omitempty,gte=60000000000" jsonschema:"description=Maximum age of session directories before GC cleanup (minimum 1 minute)"`

	// RequireSession fails closed when no session id is present in the invocation
	// context: instead of falling back to the "session" namespace, tools return an
	// error. Multi-user hosts MUST enable this. Default false.
	RequireSession bool `validate:"omitempty" jsonschema:"description=Fail closed when no session id is present (multi-user hosts should enable)"`

	// MaxCopyBytes caps the total bytes for a directory copy/move. Default 50MB.
	MaxCopyBytes int64 `validate:"omitempty,gte=1" jsonschema:"description=Maximum total bytes for directory copy (default 50MB)"`
}
