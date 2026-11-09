package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/workspace"
)

func TestSessionKeyDerivation(t *testing.T) {
	withWorkspace := newSessionManager(nil, &Config{
		Workspace: &workspace.Layout{Root: t.TempDir(), SessionKey: "session"},
	})
	key := withWorkspace.keyForSession("abc", "golang", "golang:1.22", "owner", "repo")
	assert.Equal(t, sessionKey{
		session:   "abc",
		profile:   "golang",
		baseImage: "golang:1.22",
		owner:     "owner",
		repo:      "repo",
	}, key)

	// A plain context has no adk run session, so the session component is empty.
	key = withWorkspace.key(context.Background(), "golang", "golang:1.22", "owner", "repo")
	assert.Empty(t, key.session)

	withoutWorkspace := newSessionManager(nil, &Config{})
	key = withoutWorkspace.key(context.Background(), "golang", "golang:1.22", "", "")
	assert.Equal(t, sessionKey{profile: "golang", baseImage: "golang:1.22"}, key)
}

func TestSessionEvict(t *testing.T) {
	sm := newSessionManager(nil, &Config{})
	key := sessionKey{profile: "golang", baseImage: "golang:1.22"}
	sm.sessions[key] = &session{lastUsed: time.Now()}

	sm.evict(key)
	_, ok := sm.sessions[key]
	assert.False(t, ok)
}

func TestSessionEvictIdle(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sm := newSessionManager(nil, &Config{SessionIdleTTL: time.Hour})
	sm.now = func() time.Time { return now }

	staleKey := sessionKey{profile: "stale"}
	freshKey := sessionKey{profile: "fresh"}
	sm.sessions[staleKey] = &session{lastUsed: now.Add(-2 * time.Hour)}
	sm.sessions[freshKey] = &session{lastUsed: now.Add(-30 * time.Minute)}

	sm.evictIdle(now)

	_, staleOK := sm.sessions[staleKey]
	_, freshOK := sm.sessions[freshKey]
	assert.False(t, staleOK, "idle session must be evicted")
	assert.True(t, freshOK, "recent session must survive")
}

func TestSessionIdleTTLDefault(t *testing.T) {
	sm := newSessionManager(nil, &Config{})
	assert.Equal(t, time.Hour, sm.idleTTL)
}

func TestResolveWorkspacePathFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("falls back to workdir", func(t *testing.T) {
		tool := &Tool{cfg: &Config{Workdir: "/tmp/project"}}
		path, err := tool.resolveWorkspacePath(ctx, "", "")
		require.NoError(t, err)
		assert.Equal(t, "/tmp/project", path)
	})

	t.Run("owner and repo resolve the session workspace", func(t *testing.T) {
		root := t.TempDir()
		layout := &workspace.Layout{Root: root, SessionKey: "session"}
		tool := &Tool{cfg: &Config{Workspace: layout}}
		path, err := tool.resolveWorkspacePath(ctx, "owner", "repo")
		require.NoError(t, err)
		assert.Equal(t, layout.RepoDirFor("", "owner", "repo"), path)
	})

	t.Run("owner without repo fails", func(t *testing.T) {
		tool := &Tool{cfg: &Config{Workdir: "/tmp/project"}}
		_, err := tool.resolveWorkspacePath(ctx, "owner", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "together")
	})

	t.Run("owner/repo without workspace fails", func(t *testing.T) {
		tool := &Tool{cfg: &Config{Workdir: "/tmp/project"}}
		_, err := tool.resolveWorkspacePath(ctx, "owner", "repo")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Workspace")
	})
}

func TestPrepareWorkspaceStatAndEvict(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	layout := &workspace.Layout{Root: root, SessionKey: "session"}
	tool := &Tool{
		cfg:      &Config{Workspace: layout},
		sessions: newSessionManager(nil, &Config{Workspace: layout}),
	}

	key := tool.sessions.key(ctx, "golang", "golang:1.22", "owner", "repo")
	tool.sessions.sessions[key] = &session{lastUsed: time.Now()}

	workspacePath := layout.RepoDirFor("", "owner", "repo")
	err := tool.prepareWorkspace(ctx, key, workspacePath, "owner", "repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace no longer exists")
	assert.Contains(t, err.Error(), "github_repo_clone")

	_, ok := tool.sessions.sessions[key]
	assert.False(t, ok, "stale cache entry must be evicted")

	// Once the workspace exists, the call succeeds and keeps the cache entry.
	require.NoError(t, os.MkdirAll(workspacePath, 0o755))
	tool.sessions.sessions[key] = &session{lastUsed: time.Now()}
	require.NoError(t, tool.prepareWorkspace(ctx, key, workspacePath, "owner", "repo"))
	_, ok = tool.sessions.sessions[key]
	assert.True(t, ok)
}

func TestBaseWorkdir(t *testing.T) {
	assert.Equal(t, "/tmp/project", (&Tool{cfg: &Config{Workdir: "/tmp/project"}}).baseWorkdir())

	root := t.TempDir()
	assert.Equal(t, root, (&Tool{cfg: &Config{Workspace: &workspace.Layout{Root: root}}}).baseWorkdir())
	assert.Equal(t, "", (&Tool{cfg: &Config{}}).baseWorkdir())
}

func TestPrepareWorkspaceNoopWithoutOwnerRepo(t *testing.T) {
	tool := &Tool{
		cfg:      &Config{Workspace: &workspace.Layout{Root: t.TempDir(), SessionKey: "session"}},
		sessions: newSessionManager(nil, &Config{}),
	}
	require.NoError(t, tool.prepareWorkspace(context.Background(), sessionKey{}, filepath.Join(t.TempDir(), "missing"), "", ""))
}
