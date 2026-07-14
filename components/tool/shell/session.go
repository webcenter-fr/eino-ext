package shell

import (
	"context"
	"sync"

	"emperror.dev/errors"
	"dagger.io/dagger"

	daggerlib "github.com/webcenter-fr/eino-ext/libs/toolkit/dagger"
)

type sessionKey struct {
	profile   string
	baseImage string
}

type session struct {
	container *dagger.Container
	mu        sync.Mutex
}

type sessionManager struct {
	mu       sync.RWMutex
	sessions map[sessionKey]*session
	client   *daggerlib.Client
	cfg      *Config
}

func newSessionManager(client *daggerlib.Client, cfg *Config) *sessionManager {
	return &sessionManager{
		sessions: make(map[sessionKey]*session),
		client:   client,
		cfg:      cfg,
	}
}

func (sm *sessionManager) getOrCreate(ctx context.Context, profileName, baseImage string) (*session, error) {
	key := sessionKey{profile: profileName, baseImage: baseImage}

	sm.mu.RLock()
	s, ok := sm.sessions[key]
	sm.mu.RUnlock()
	if ok {
		return s, nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	s, ok = sm.sessions[key]
	if ok {
		return s, nil
	}

	cacheKey := sm.cfg.CacheKey
	if cacheKey == "" {
		cacheKey = daggerlib.CacheKeyForProfile(baseImage, profileName)
	}

	var opts []daggerlib.ContainerOpt
	opts = append(opts, daggerlib.WithWorkdir(sm.cfg.Workdir))
	opts = append(opts, daggerlib.WithCacheVolume("/var/cache/apt", cacheKey+"-apt"))
	opts = append(opts, daggerlib.WithCacheVolume("/var/lib/apt", cacheKey+"-apt-lib"))

	if sm.cfg.NetworkPolicy != nil {
		opts = append(opts, daggerlib.WithEgressPolicy(sm.cfg.NetworkPolicy))
	}

	if len(sm.cfg.RegistryAuth) > 0 {
		opts = append(opts, daggerlib.WithRegistryAuth(sm.cfg.RegistryAuth))
	}

	cont, err := sm.client.Container(ctx, baseImage, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create container for profile %q", profileName)
	}

	s = &session{container: cont}
	sm.sessions[key] = s
	return s, nil
}

func (sm *sessionManager) exec(ctx context.Context, ses *session, command []string) (string, string, int, error) {
	ses.mu.Lock()
	defer ses.mu.Unlock()

	ses.container = ses.container.WithExec(command, dagger.ContainerWithExecOpts{
		Expect: dagger.ReturnTypeAny,
	})

	synced, err := ses.container.Sync(ctx)
	if err != nil {
		return "", "", -1, errors.Wrap(err, "failed to sync container after exec")
	}
	ses.container = synced

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

func (sm *sessionManager) close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions = make(map[sessionKey]*session)
}
