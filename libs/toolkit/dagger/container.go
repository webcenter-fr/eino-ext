package dagger

import (
	"context"
	"maps"
	"slices"

	"dagger.io/dagger"
	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
)

// ContainerOpt is a functional option for configuring a Dagger container.
type ContainerOpt func(*containerConfig)

type containerConfig struct {
	workspace    string
	cacheVolumes map[string]string
	egressPolicy *egress.Policy
	user         string
	registryAuth map[string]RegistryAuth
}

// WithWorkspace mounts hostPath at /workspace and sets it as the container
// working directory.
func WithWorkspace(hostPath string) ContainerOpt {
	return func(c *containerConfig) {
		c.workspace = hostPath
	}
}

// WithWorkdir is kept for backward compatibility; it mounts the host directory
// at /workspace exactly like WithWorkspace.
func WithWorkdir(workdir string) ContainerOpt {
	return WithWorkspace(workdir)
}

// WithCacheVolume adds a cache volume mount to the container.
func WithCacheVolume(containerPath, cacheKey string) ContainerOpt {
	return func(c *containerConfig) {
		if c.cacheVolumes == nil {
			c.cacheVolumes = make(map[string]string)
		}
		c.cacheVolumes[containerPath] = cacheKey
	}
}

// WithEgressPolicy sets an egress filtering policy for the container.
func WithEgressPolicy(pol *egress.Policy) ContainerOpt {
	return func(c *containerConfig) {
		c.egressPolicy = pol
	}
}

// WithUser sets the user inside the container.
func WithUser(user string) ContainerOpt {
	return func(c *containerConfig) {
		c.user = user
	}
}

// WithRegistryAuth sets registry authentication for the container.
func WithRegistryAuth(auth map[string]RegistryAuth) ContainerOpt {
	return func(c *containerConfig) {
		c.registryAuth = auth
	}
}

// Container builds a new Dagger container from the given base image with the provided options.
func (c *Client) Container(ctx context.Context, baseImage string, opts ...ContainerOpt) (*dagger.Container, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}

	cc := &containerConfig{}
	for _, opt := range opts {
		opt(cc)
	}

	cont := c.client.Container().From(baseImage)

	for host, auth := range cc.registryAuth {
		password := c.client.SetSecret(host+"-password", auth.Password)
		cont = cont.WithRegistryAuth(host, auth.Username, password)
	}

	for containerPath, cacheKey := range cc.cacheVolumes {
		cv := c.client.CacheVolume(cacheKey, dagger.CacheVolumeOpts{
			Sharing: dagger.CacheSharingModeLocked,
		})
		cont = cont.WithMountedCache(containerPath, cv)
	}

	if cc.workspace != "" {
		hostDir := c.client.Host().Directory(cc.workspace)
		cont = cont.WithMountedDirectory("/workspace", hostDir)
		cont = cont.WithWorkdir("/workspace")
	}

	if cc.egressPolicy != nil {
		proxySvc, err := c.EgressProxyService(ctx, cc.egressPolicy)
		if err != nil {
			return nil, errors.Wrap(err, "failed to build egress proxy service")
		}
		cont = cont.WithServiceBinding(egressServiceAlias, proxySvc)
		env := cc.egressPolicy.ProxyEnv(egressProxyURL)
		for _, key := range slices.Sorted(maps.Keys(env)) {
			cont = cont.WithEnvVariable(key, env[key])
		}
	}

	if cc.user != "" {
		cont = cont.WithUser(cc.user)
	}

	return cont, nil
}
