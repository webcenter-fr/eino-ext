package main

import (
	"context"
	"fmt"

	"dagger/eino-ext/internal/dagger"
)

// Ci runs the Go gates in fail-fast order and returns the coverage artifact.
func (m *EinoExt) Ci(
	ctx context.Context,
	// +optional
	version string,
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

	return dag.Directory().WithFile("coverage.out", coverage), nil
}

// IntegrationTest runs the opt-in Copilot integration suite.
func (m *EinoExt) IntegrationTest(ctx context.Context, copilotToken *dagger.Secret) (string, error) {
	return m.goContainer().
		WithSecretVariable("COPILOT_TOKEN", copilotToken).
		WithEnvVariable("COPILOT_INTEGRATION", "1").
		WithExec([]string{"go", "test", "-tags=integration", "-v", "./components/model/copilot/..."}).
		Stdout(ctx)
}
