// Package profile provides project-type detection and base-image selection.
// It scans a workdir for marker files (go.mod, package.json, etc.) and maps
// the detected project type to a Dagger-compatible OCI base image + tool presets.
package profile

type Profile struct {
	Name         string
	BaseImage    string
	SystemPrompt string
	InstallCmd   []string
	ToolPresets  []string
	Env          map[string]string
}

type Resolver struct {
	ImageMap map[string]string
}

type ResolverOpt func(*Resolver)

func WithImageOverrides(m map[string]string) ResolverOpt {
	return func(r *Resolver) {
		for k, v := range m {
			r.ImageMap[k] = v
		}
	}
}

func NewResolver(opts ...ResolverOpt) *Resolver {
	r := &Resolver{
		ImageMap: DefaultImageMap,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

var DefaultImageMap = map[string]string{
	"golang": "golang:1.22",
	"node":   "node:20",
	"python": "python:3.12",
	"java":   "eclipse-temurin:21",
	"rust":   "rust:1.82",
	"php":    "php:8.3",
}

var markerToProfile = map[string]string{
	"go.mod":          "golang",
	"package.json":    "node",
	"pyproject.toml":  "python",
	"requirements.txt": "python",
	"setup.py":        "python",
	"pom.xml":         "java",
	"build.gradle":    "java",
	"build.gradle.kts": "java",
	"Cargo.toml":      "rust",
	"composer.json":   "php",
}
