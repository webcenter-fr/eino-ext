package shell

import (
	"context"
	"sync"
	"time"

	"dagger.io/dagger"
	"emperror.dev/errors"

	daggerlib "github.com/webcenter-fr/eino-ext/libs/toolkit/dagger"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/fileutil"
)

type sessionKey struct {
	session   string
	profile   string
	baseImage string
	owner     string
	repo      string
}

type session struct {
	container *dagger.Container
	mu        sync.Mutex
	lastUsed  time.Time
}

type sessionManager struct {
	mu       sync.RWMutex
	sessions map[sessionKey]*session
	client   *daggerlib.Client
	cfg      *Config
	idleTTL  time.Duration
	now      func() time.Time
}

func newSessionManager(client *daggerlib.Client, cfg *Config) *sessionManager {
	idleTTL := cfg.SessionIdleTTL
	if idleTTL == 0 {
		idleTTL = time.Hour
	}
	return &sessionManager{
		sessions: make(map[sessionKey]*session),
		client:   client,
		cfg:      cfg,
		idleTTL:  idleTTL,
		now:      time.Now,
	}
}

// key derives the cache key for a call. The session id is only included when a
// workspace layout is configured, so non-workspace hosts keep the previous
// {profile, baseImage} behaviour.
func (sm *sessionManager) key(ctx context.Context, profileName, baseImage, owner, repo string) sessionKey {
	session := ""
	if sm.cfg.Workspace != nil {
		session = fileutil.SessionFromContext(ctx, sm.cfg.Workspace.SessionKey)
	}
	return sm.keyForSession(session, profileName, baseImage, owner, repo)
}

func (sm *sessionManager) keyForSession(session, profileName, baseImage, owner, repo string) sessionKey {
	return sessionKey{
		session:   session,
		profile:   profileName,
		baseImage: baseImage,
		owner:     owner,
		repo:      repo,
	}
}

func (sm *sessionManager) getOrCreate(ctx context.Context, key sessionKey) (*session, error) {
	sm.evictIdle(sm.now())

	sm.mu.RLock()
	s, ok := sm.sessions[key]
	sm.mu.RUnlock()
	if ok {
		s.mu.Lock()
		s.lastUsed = sm.now()
		s.mu.Unlock()
		return s, nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok = sm.sessions[key]
	if ok {
		s.mu.Lock()
		s.lastUsed = sm.now()
		s.mu.Unlock()
		return s, nil
	}

	cacheKey := sm.cfg.CacheKey
	if cacheKey == "" {
		cacheKey = daggerlib.CacheKeyForProfile(key.baseImage, key.profile)
	}

	var opts []daggerlib.ContainerOpt
	opts = append(opts, daggerlib.WithCacheVolume("/var/cache/apt", cacheKey+"-apt"))
	opts = append(opts, daggerlib.WithCacheVolume("/var/lib/apt", cacheKey+"-apt-lib"))

	if sm.cfg.NetworkPolicy != nil {
		opts = append(opts, daggerlib.WithEgressPolicy(sm.cfg.NetworkPolicy))
	}

	if len(sm.cfg.RegistryAuth) > 0 {
		opts = append(opts, daggerlib.WithRegistryAuth(sm.cfg.RegistryAuth))
	}

	cont, err := sm.client.Container(ctx, key.baseImage, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create container for profile %q", key.profile)
	}

	s = &session{container: cont, lastUsed: sm.now()}
	sm.sessions[key] = s
	return s, nil
}

func (sm *sessionManager) exec(ctx context.Context, ses *session, workspacePath string, command []string, stdin string) (string, string, int, error) {
	ses.mu.Lock()
	defer ses.mu.Unlock()

	cont := ses.container
	if workspacePath != "" {
		hostDir := sm.client.Dagger().Host().Directory(workspacePath)
		cont = cont.WithMountedDirectory("/workspace", hostDir).WithWorkdir("/workspace")
	}

	cont = cont.WithExec(command, dagger.ContainerWithExecOpts{
		Expect: dagger.ReturnTypeAny,
		Stdin:  stdin,
	})

	synced, err := cont.Sync(ctx)
	if err != nil {
		return "", "", -1, errors.Wrap(err, "failed to sync container after exec")
	}
	ses.container = synced
	ses.lastUsed = sm.now()

	stdout, err := synced.Stdout(ctx)
	if err != nil {
		return "", "", -1, errors.Wrap(err, "failed to read stdout")
	}

	stderr, err := synced.Stderr(ctx)
	if err != nil {
		return stdout, "", -1, errors.Wrap(err, "failed to read stderr")
	}

	exitCode, err := synced.ExitCode(ctx)
	if err != nil {
		return stdout, stderr, -1, errors.Wrap(err, "failed to read exit code")
	}

	return stdout, stderr, exitCode, nil
}

// evict removes a cached session, e.g. when its workspace was swept.
func (sm *sessionManager) evict(key sessionKey) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, key)
}

// export writes the session container's /workspace back to workspacePath,
// excluding .git.
func (sm *sessionManager) export(ctx context.Context, ses *session, workspacePath string) error {
	if workspacePath == "" {
		return errors.New("persistChanges requires a workspace path")
	}
	ses.mu.Lock()
	defer ses.mu.Unlock()
	if err := sm.client.ExportWorkspace(ctx, ses.container, workspacePath); err != nil {
		return errors.Wrapf(err, "failed to persist container changes to %q", workspacePath)
	}
	return nil
}

// evictIdle removes sessions that have not been used for longer than idleTTL.
func (sm *sessionManager) evictIdle(now time.Time) {
	if sm.idleTTL <= 0 {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for key, ses := range sm.sessions {
		ses.mu.Lock()
		idle := now.Sub(ses.lastUsed)
		ses.mu.Unlock()
		if idle > sm.idleTTL {
			delete(sm.sessions, key)
		}
	}
}
