package profile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolver_Resolve_Golang(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644))

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "golang", profiles[0].Name)
	assert.Equal(t, "golang:1.22", profiles[0].BaseImage)
}

func TestResolver_Resolve_Node(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644))

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "node", profiles[0].Name)
	assert.Equal(t, "node:20", profiles[0].BaseImage)
}

func TestResolver_Resolve_Python(t *testing.T) {
	tests := []struct {
		name   string
		marker string
	}{
		{name: "pyproject.toml", marker: "pyproject.toml"},
		{name: "requirements.txt", marker: "requirements.txt"},
		{name: "setup.py", marker: "setup.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.marker), []byte(""), 0644))

			r := NewResolver()
			profiles, err := r.Resolve(context.Background(), dir)
			require.NoError(t, err)
			require.Len(t, profiles, 1)
			assert.Equal(t, "python", profiles[0].Name)
			assert.Equal(t, "python:3.12", profiles[0].BaseImage)
		})
	}
}

func TestResolver_Resolve_Java(t *testing.T) {
	tests := []struct {
		name   string
		marker string
	}{
		{name: "pom.xml", marker: "pom.xml"},
		{name: "build.gradle", marker: "build.gradle"},
		{name: "build.gradle.kts", marker: "build.gradle.kts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.marker), []byte(""), 0644))

			r := NewResolver()
			profiles, err := r.Resolve(context.Background(), dir)
			require.NoError(t, err)
			require.Len(t, profiles, 1)
			assert.Equal(t, "java", profiles[0].Name)
			assert.Equal(t, "eclipse-temurin:21", profiles[0].BaseImage)
		})
	}
}

func TestResolver_Resolve_Rust(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]"), 0644))

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "rust", profiles[0].Name)
	assert.Equal(t, "rust:1.82", profiles[0].BaseImage)
}

func TestResolver_Resolve_PHP(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "composer.json"), []byte("{}"), 0644))

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "php", profiles[0].Name)
	assert.Equal(t, "php:8.3", profiles[0].BaseImage)
}

func TestResolver_Resolve_Polyglot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(""), 0644))

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 3)

	names := make(map[string]bool)
	for _, p := range profiles {
		names[p.Name] = true
	}
	assert.True(t, names["golang"])
	assert.True(t, names["node"])
	assert.True(t, names["python"])
}

func TestResolver_Resolve_Unknown_Default(t *testing.T) {
	dir := t.TempDir()

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "default", profiles[0].Name)
	assert.Equal(t, "alpine:3.20", profiles[0].BaseImage)
}

func TestResolver_Resolve_EmptyWorkdir(t *testing.T) {
	r := NewResolver()
	_, err := r.Resolve(context.Background(), "")
	assert.Error(t, err)
}

func TestResolver_Resolve_ImageOverride(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644))

	r := NewResolver(WithImageOverrides(map[string]string{
		"golang": "golang:1.23",
	}))
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "golang:1.23", profiles[0].BaseImage)
}

func TestResolver_Resolve_DedupPythonMarkers(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setup.py"), []byte(""), 0644))

	r := NewResolver()
	profiles, err := r.Resolve(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, "python", profiles[0].Name)
}

func TestProfile_SystemPrompt(t *testing.T) {
	profiles := []struct {
		name   string
		marker string
		expect string
	}{
		{name: "golang", marker: "go.mod", expect: "go build"},
		{name: "node", marker: "package.json", expect: "npm install"},
		{name: "python", marker: "pyproject.toml", expect: "pip install"},
		{name: "java", marker: "pom.xml", expect: "mvn test"},
		{name: "rust", marker: "Cargo.toml", expect: "cargo build"},
		{name: "php", marker: "composer.json", expect: "composer install"},
	}

	for _, tt := range profiles {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildSystemPrompt(tt.name, tt.marker)
			assert.Contains(t, prompt, tt.expect)
			assert.Contains(t, prompt, tt.name)
			assert.Contains(t, prompt, tt.marker)
		})
	}
}
