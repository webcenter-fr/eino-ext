package dagger

import (
	"context"

	"emperror.dev/errors"
	"dagger.io/dagger"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/egress"
)

type ContainerOpt func(*containerConfig)

type containerConfig struct {
	workdir      string
	cacheVolumes map[string]string
	egressPolicy *egress.Policy
	user         string
	registryAuth map[string]RegistryAuth
}

func WithWorkdir(workdir string) ContainerOpt {
	return func(c *containerConfig) {
		c.workdir = workdir
	}
}

func WithCacheVolume(containerPath, cacheKey string) ContainerOpt {
	return func(c *containerConfig) {
		if c.cacheVolumes == nil {
			c.cacheVolumes = make(map[string]string)
		}
		c.cacheVolumes[containerPath] = cacheKey
	}
}

func WithEgressPolicy(pol *egress.Policy) ContainerOpt {
	return func(c *containerConfig) {
		c.egressPolicy = pol
	}
}

func WithUser(user string) ContainerOpt {
	return func(c *containerConfig) {
		c.user = user
	}
}

func WithRegistryAuth(auth map[string]RegistryAuth) ContainerOpt {
	return func(c *containerConfig) {
		c.registryAuth = auth
	}
}

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

	if cc.workdir != "" {
		hostDir := c.client.Host().Directory(cc.workdir)
		cont = cont.WithMountedDirectory("/workspace", hostDir)
		cont = cont.WithWorkdir("/workspace")
	}

	if cc.user != "" {
		cont = cont.WithUser(cc.user)
	}

	return cont, nil
}
