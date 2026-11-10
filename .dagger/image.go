package main

import (
	"context"
	"fmt"

	"dagger/eino-ext/internal/dagger"
)

// ImageMcp builds the MCP server image.
func (m *EinoExt) ImageMcp() *dagger.Container {
	return dag.Image().Build(m.Src, dagger.ImageBuildOpts{Dockerfile: "Dockerfile.mcp"}).GetContainer()
}

// ImageApproval builds the approval server image.
func (m *EinoExt) ImageApproval() *dagger.Container {
	return dag.Image().Build(m.Src, dagger.ImageBuildOpts{Dockerfile: "Dockerfile.approval"}).GetContainer()
}

// PublishImages builds and pushes both images, returning their pinned refs.
func (m *EinoExt) PublishImages(ctx context.Context, registry, repository, version string, username, password *dagger.Secret) ([]string, error) {
	images := []struct {
		name       string
		dockerfile string
	}{
		{name: "mcp", dockerfile: "Dockerfile.mcp"},
		{name: "approval", dockerfile: "Dockerfile.approval"},
	}
	refs := make([]string, 0, len(images))
	for _, image := range images {
		build := dag.Image().Build(m.Src, dagger.ImageBuildOpts{Dockerfile: image.dockerfile})
		digest, err := build.Push(ctx, repository+"/"+image.name, version, registry, dagger.ImageBuildPushOpts{
			WithRegistryUsername: username,
			WithRegistryPassword: password,
		})
		if err != nil {
			return nil, fmt.Errorf("push %s image: %w", image.name, err)
		}
		refs = append(refs, fmt.Sprintf("%s/%s/%s:%s@%s", registry, repository, image.name, version, digest))
	}
	return refs, nil
}
