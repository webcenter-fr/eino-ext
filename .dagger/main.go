// A Dagger module for the eino-ext repository: Go gates, container images,
// Helm chart validation and the CI orchestrator.
package main

import (
	"dagger/eino-ext/internal/dagger"
)

// EinoExt is the repository CI module.
type EinoExt struct {
	Src *dagger.Directory
}

// New creates the module from the repository source.
func New(
	// +defaultPath="/"
	// +ignore=["bin", ".git", "**/node_modules", "*.test", "github.test", "ci-out"]
	src *dagger.Directory,
) *EinoExt {
	return &EinoExt{Src: src}
}
