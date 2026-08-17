// Package profile provides project-type detection and base-image selection.
// It scans a workdir for marker files (go.mod, package.json, etc.) and maps
// the detected project type to a Dagger-compatible OCI base image + tool presets.
package profile

// Profile describes a project type and its associated base image and tool presets.
type Profile struct {
	Name         string
	BaseImage    string
	SystemPrompt string
	InstallCmd   []string
	ToolPresets  []string
	Env          map[string]string
}

// Resolver detects project types from a workdir and maps them to profiles.
type Resolver struct {
	ImageMap map[string]string
}

// ResolverOpt is a functional option for configuring a Resolver.
type ResolverOpt func(*Resolver)

// WithImageOverrides sets custom base images for profile names.
func WithImageOverrides(m map[string]string) ResolverOpt {
	return func(r *Resolver) {
		for k, v := range m {
			r.ImageMap[k] = v
		}
	}
}

// NewResolver creates a new Resolver with the given options.
func NewResolver(opts ...ResolverOpt) *Resolver {
	r := &Resolver{
		ImageMap: DefaultImageMap,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// DefaultImageMap provides the default base image for each known profile name.
var DefaultImageMap = map[string]string{
	"golang": "golang:1.22",
	"node":   "node:20",
	"python": "python:3.12",
	"java":   "eclipse-temurin:21",
	"rust":   "rust:1.82",
	"php":    "php:8.3",
}

var markerToProfile = map[string]string{
	"go.mod":           "golang",
	"package.json":     "node",
	"pyproject.toml":   "python",
	"requirements.txt": "python",
	"setup.py":         "python",
	"pom.xml":          "java",
	"build.gradle":     "java",
	"build.gradle.kts": "java",
	"Cargo.toml":       "rust",
	"composer.json":    "php",
}
