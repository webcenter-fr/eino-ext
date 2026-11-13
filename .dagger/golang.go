package main

import (
	"context"

	"dagger/eino-ext/internal/dagger"
)

const (
	envtestK8sVersion  = "1.34.0"
	golangciLintTag    = "v2.14.0"
	govulncheckVersion = "v1.9.0"
)

// golang returns the upstream Golang module with the repository source. The
// module derives its base image from the go.mod language version
// (golang:<version>), so go 1.27 uses the latest patched 1.27 toolchain.
// Never call dag.Golang(...).Ci(): it runs gofumpt -w over the whole
// repository.
func (m *EinoExt) golang() *dagger.Golang {
	return dag.Golang(m.Src)
}

func (m *EinoExt) goContainer() *dagger.Container {
	return m.golang().Container()
}

// Build compiles every package.
func (m *EinoExt) Build(ctx context.Context) (*dagger.Directory, error) {
	return m.golang().Build(dagger.GolangBuildOpts{Main: "./..."}).Sync(ctx)
}

// Vet runs go vet.
func (m *EinoExt) Vet(ctx context.Context) (string, error) {
	return m.goContainer().WithExec([]string{"go", "vet", "./..."}).Stdout(ctx)
}

// Lint runs golangci-lint pinned to v2.14. It installs to /usr/local/bin,
// outside the cache-mounted $GOPATH/bin.
func (m *EinoExt) Lint(ctx context.Context) (string, error) {
	return m.goContainer().
		WithExec([]string{"bash", "-c", "GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@" + golangciLintTag}).
		WithExec([]string{"golangci-lint", "run", "--timeout", "10m", "./..."}).
		Stdout(ctx)
}

// Test runs the full test suite with envtest assets and returns coverage.out.
func (m *EinoExt) Test(ctx context.Context) *dagger.File {
	container := m.goContainer().
		WithMountedCache("/envtest-assets", dag.CacheVolume("envtest-"+envtestK8sVersion)).
		WithExec([]string{"bash", "-c", "GOBIN=/usr/local/bin go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.24.1"}).
		WithExec([]string{"bash", "-c", "/usr/local/bin/setup-envtest use " + envtestK8sVersion + " --bin-dir /envtest-assets -p path > /tmp/assets"}).
		// Run as the distroless nonroot UID so permission-sensitive tests (for
		// example a read-only directory check) behave as they do on a normal
		// developer machine instead of as root.
		WithExec([]string{"chown", "-R", "65532:65532", "/src", "/go", "/envtest-assets"}).
		WithUser("65532:65532").
		WithEnvVariable("HOME", "/tmp").
		WithEnvVariable("GOCACHE", "/tmp/go-build").
		WithExec([]string{"bash", "-c", `KUBEBUILDER_ASSETS="$(cat /tmp/assets)" go test -covermode=atomic -coverprofile=coverage.out ./...`})
	return container.File("coverage.out")
}

// Vulncheck installs a pinned govulncheck with the container's Go toolchain.
// The upstream Vulncheck reuses a cached binary built with the go.mod
// toolchain, which fails when the base image is newer. The version is pinned
// (not @latest) so the supply chain is reproducible.
func (m *EinoExt) Vulncheck(ctx context.Context) (string, error) {
	return m.goContainer().
		WithExec([]string{"bash", "-c", "GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@" + govulncheckVersion}).
		WithExec([]string{"/usr/local/bin/govulncheck", "./..."}).
		Stdout(ctx)
}

// CheckComponents verifies every component package has a test and README.
func (m *EinoExt) CheckComponents(ctx context.Context) (string, error) {
	return m.goContainer().WithExec([]string{"bash", "scripts/check_components.sh"}).Stdout(ctx)
}
