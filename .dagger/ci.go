package main

import (
	"context"
	"fmt"

	"dagger/eino-ext/internal/dagger"
)

// Ci runs the full pipeline in fail-fast order and returns the generated
// artifacts. Publishing only happens when publish is true and dryRun is false.
func (m *EinoExt) Ci(
	ctx context.Context,
	// +optional
	publish bool,
	// +optional
	version string,
	// +optional
	// +default="ghcr.io"
	registry string,
	// +optional
	// +default="webcenter-fr/eino-ext"
	repository string,
	// +optional
	registryUsername *dagger.Secret,
	// +optional
	registryPassword *dagger.Secret,
	// +optional
	codecovToken *dagger.Secret,
	// +optional
	dryRun bool,
) (*dagger.Directory, error) {
	if _, err := m.Lint(ctx); err != nil {
		return nil, fmt.Errorf("lint: %w", err)
	}
	if _, err := m.Vet(ctx); err != nil {
		return nil, fmt.Errorf("vet: %w", err)
	}
	if _, err := m.Build(ctx); err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}

	coverage := m.Test(ctx)
	if _, err := coverage.Sync(ctx); err != nil {
		return nil, fmt.Errorf("test: %w", err)
	}
	if codecovToken != nil {
		token, err := codecovToken.Plaintext(ctx)
		if err != nil {
			return nil, fmt.Errorf("read codecov token: %w", err)
		}
		if token != "" {
			coverageDir := dag.Directory().WithFile("coverage.out", coverage)
			if _, err := dag.Codecov().Upload(ctx, coverageDir, codecovToken, dagger.CodecovUploadOpts{
				Files: []string{"coverage.out"},
			}); err != nil {
				return nil, fmt.Errorf("codecov upload: %w", err)
			}
		}
	}

	if _, err := m.Vulncheck(ctx); err != nil {
		return nil, fmt.Errorf("vulncheck: %w", err)
	}
	if _, err := m.CheckComponents(ctx); err != nil {
		return nil, fmt.Errorf("check components: %w", err)
	}
	if _, err := m.UI(ctx); err != nil {
		return nil, fmt.Errorf("ui: %w", err)
	}
	if _, err := m.LintImages(ctx); err != nil {
		return nil, fmt.Errorf("lint images: %w", err)
	}
	if _, err := m.ImageMcp().Sync(ctx); err != nil {
		return nil, fmt.Errorf("build mcp image: %w", err)
	}
	if _, err := m.ImageApproval().Sync(ctx); err != nil {
		return nil, fmt.Errorf("build approval image: %w", err)
	}
	if _, err := m.HelmLint(ctx); err != nil {
		return nil, fmt.Errorf("helm lint: %w", err)
	}

	schema := m.HelmSchema(ctx)
	docs := m.HelmDocs(ctx)
	if err := m.assertFileMatches(ctx, m.Src.File(chartPath+"/values.schema.json"), schema, "helm/eino-ext/values.schema.json"); err != nil {
		return nil, err
	}
	if err := m.assertFileMatches(ctx, m.Src.File(chartPath+"/README.md"), docs, "helm/eino-ext/README.md"); err != nil {
		return nil, err
	}
	if _, err := m.HelmTemplate(ctx); err != nil {
		return nil, fmt.Errorf("helm template: %w", err)
	}

	if publish && !dryRun {
		if _, err := m.PublishImages(ctx, registry, repository, version, registryUsername, registryPassword); err != nil {
			return nil, fmt.Errorf("publish images: %w", err)
		}
		if _, err := m.HelmPublish(ctx, registry, repository+"/charts", version, registryUsername, registryPassword); err != nil {
			return nil, fmt.Errorf("publish helm chart: %w", err)
		}
	}

	return dag.Directory().
		WithFile("coverage.out", coverage).
		WithFile("values.schema.json", schema).
		WithFile("helm/eino-ext/README.md", docs), nil
}

// IntegrationTest runs the opt-in Copilot integration suite.
func (m *EinoExt) IntegrationTest(ctx context.Context, copilotToken *dagger.Secret) (string, error) {
	return m.goContainer().
		WithSecretVariable("COPILOT_TOKEN", copilotToken).
		WithEnvVariable("COPILOT_INTEGRATION", "1").
		WithExec([]string{"go", "test", "-tags=integration", "-v", "./components/model/copilot/..."}).
		Stdout(ctx)
}

func (m *EinoExt) assertFileMatches(ctx context.Context, committed, generated *dagger.File, name string) error {
	if _, err := dag.Container().
		From("alpine:3.20").
		WithFile("/committed", committed).
		WithFile("/generated", generated).
		WithExec([]string{"sh", "-c", "diff -u /committed /generated"}).
		Sync(ctx); err != nil {
		return fmt.Errorf("%s is stale; regenerate it: %w", name, err)
	}
	return nil
}
