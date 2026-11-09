package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

func TestNewLayout(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *Layout
		wantErr    bool
		wantKey    string
		wantRoot   string
		wantErrSub string
	}{
		{
			name:     "defaults session key",
			cfg:      &Layout{Root: t.TempDir()},
			wantKey:  DefaultSessionKey,
			wantRoot: "",
		},
		{
			name:       "nil config fails",
			cfg:        nil,
			wantErr:    true,
			wantErrSub: "Root",
		},
		{
			name:       "relative root fails",
			cfg:        &Layout{Root: "relative/path"},
			wantErr:    true,
			wantErrSub: "absolute",
		},
		{
			name:       "system root fails",
			cfg:        &Layout{Root: "/tmp"},
			wantErr:    true,
			wantErrSub: "system directory",
		},
		{
			name:    "explicit session key is preserved",
			cfg:     &Layout{Root: t.TempDir(), SessionKey: "custom"},
			wantKey: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewLayout(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrSub != "" {
					assert.Contains(t, err.Error(), tt.wantErrSub)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantKey, got.SessionKey)
			if tt.wantRoot != "" {
				assert.Equal(t, tt.wantRoot, got.Root)
			}
		})
	}
}

func TestSessionDirHashing(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{Root: root, SessionKey: "session"}

	assert.Equal(t, filepath.Join(root, DefaultSessionSegment), layout.SessionDirFor(""))
	assert.Equal(t, filepath.Join(root, fileutil.SessionDirName("s1")), layout.SessionDirFor("s1"))

	// Distinct ids never collide after hashing.
	assert.NotEqual(t, layout.SessionDirFor("ab\x1fc"), layout.SessionDirFor("a\x1fbc"))

	// A traversal-ish id hashes to a fixed hex segment: no separator and no "..".
	seg := layout.SessionDirFor("sess/../x")
	assert.NotContains(t, seg, "..")
	assert.NotContains(t, seg, "sess")
	assert.Contains(t, seg, fileutil.SessionDirName("sess/../x"))
}

func TestSessionDirFromContext(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{Root: root, SessionKey: "session"}

	// A plain context has no adk run session, so the default namespace is used.
	dir, err := layout.SessionDir(context.Background())
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, DefaultSessionSegment), dir)
}

func TestSessionDirFailsClosed(t *testing.T) {
	layout := &Layout{Root: t.TempDir(), SessionKey: "session", RequireSession: true}

	_, err := layout.SessionDir(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is required")
	assert.Contains(t, err.Error(), "session")

	_, err = layout.RepoDir(context.Background(), "o", "r")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is required")

	err = layout.Touch(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is required")
}

func TestRepoDirSanitizesSegments(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{Root: root, SessionKey: "session"}

	tests := []struct {
		name  string
		owner string
		repo  string
	}{
		{name: "plain", owner: "owner", repo: "repo"},
		{name: "traversal", owner: "../../etc", repo: "../passwd"},
		{name: "absolute", owner: "/etc", repo: "/passwd"},
		{name: "empty", owner: "", repo: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := layout.RepoDirFor("", tt.owner, tt.repo)
			assert.True(t, fileutil.IsWithinPath(layout.SessionDirFor(""), dir),
				"repo dir %q must stay within the session namespace", dir)
			assert.NotContains(t, dir, "..")
		})
	}
}

func TestResolvePathRejectsTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "file.txt"), []byte("ok"), 0o644))

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	tests := []struct {
		name       string
		relPath    string
		createDirs bool
		wantErrSub string
	}{
		{name: "valid file", relPath: "sub/file.txt"},
		{name: "valid new file", relPath: "sub/new.txt", createDirs: true},
		{name: "parent traversal", relPath: "../escape.txt", wantErrSub: "escapes root"},
		{name: "absolute path", relPath: "/etc/passwd", wantErrSub: "absolute"},
		{name: "symlink component", relPath: "link/secret.txt", wantErrSub: "symlink"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolvePath(root, tt.relPath, tt.createDirs)
			if tt.wantErrSub == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrSub)
		})
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	ok, err := Exists(dir)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = Exists(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestTouchAndGC(t *testing.T) {
	root := t.TempDir()
	layout := &Layout{Root: root, SessionKey: "session"}

	namespace := layout.SessionDirFor("")
	require.NoError(t, os.MkdirAll(namespace, 0o755))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(namespace, old, old))

	require.NoError(t, layout.Touch(context.Background()))
	fi, err := os.Stat(namespace)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().After(old.Add(time.Minute)), "mtime was not refreshed")

	// Touch is a no-op when the namespace does not exist.
	require.NoError(t, (&Layout{Root: t.TempDir(), SessionKey: "session"}).Touch(context.Background()))

	// GC sweeps stale namespaces and keeps fresh ones.
	stale := layout.SessionDirFor("stale")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.Chtimes(stale, old, old))
	fresh := layout.SessionDirFor("fresh")
	require.NoError(t, os.MkdirAll(fresh, 0o755))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	layout.StartGC(ctx, time.Hour, 5*time.Millisecond)

	assert.Eventually(t, func() bool {
		_, statErr := os.Stat(stale)
		return os.IsNotExist(statErr)
	}, 2*time.Second, 5*time.Millisecond, "stale namespace must be swept")

	_, err = os.Stat(fresh)
	require.NoError(t, err, "fresh namespace must survive the sweep")
}

func TestStartGCNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	layout := &Layout{Root: t.TempDir(), SessionKey: "session"}
	layout.StartGC(ctx, 0, time.Millisecond)
	layout.StartGC(ctx, time.Hour, 0)
	layout.StartGC(ctx, time.Hour, -time.Second)
}
