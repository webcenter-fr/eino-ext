package dagger

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheMountsForProfile(t *testing.T) {
	apt := []string{"/var/cache/apt", "/var/lib/apt"}
	apk := []string{"/var/cache/apk"}

	tests := []struct {
		name    string
		profile string
		want    []string
	}{
		{
			name:    "golang",
			profile: "golang",
			want:    append([]string{"/go/pkg/mod", "/root/.cache/go-build"}, apt...),
		},
		{
			name:    "node",
			profile: "node",
			want:    append([]string{"/root/.npm"}, apt...),
		},
		{
			name:    "python",
			profile: "python",
			want:    append([]string{"/root/.cache/pip"}, apt...),
		},
		{
			name:    "java",
			profile: "java",
			want:    append([]string{"/root/.m2", "/root/.gradle/caches"}, apt...),
		},
		{
			name:    "rust",
			profile: "rust",
			want:    append([]string{"/usr/local/cargo/registry", "/usr/local/cargo/git"}, apt...),
		},
		{
			name:    "php",
			profile: "php",
			want:    append([]string{"/root/.composer/cache"}, apt...),
		},
		{
			name:    "default",
			profile: "default",
			want:    apk,
		},
		{
			name:    "empty",
			profile: "",
			want:    apk,
		},
		{
			name:    "unknown",
			profile: "nonexistent",
			want:    apk,
		},
		{
			name:    "case insensitive",
			profile: "GOLANG",
			want:    append([]string{"/go/pkg/mod", "/root/.cache/go-build"}, apt...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CacheMountsForProfile(tt.profile))
		})
	}
}

func TestCacheMountsForProfile_NoWorkspacePaths(t *testing.T) {
	for _, profile := range []string{"golang", "node", "python", "java", "rust", "php", "default", ""} {
		for _, path := range CacheMountsForProfile(profile) {
			assert.False(t, strings.HasPrefix(path, "/workspace"),
				"profile %q returned a workspace path: %s", profile, path)
		}
	}
}

func TestCacheMountsForProfile_ReturnsFreshSlice(t *testing.T) {
	first := CacheMountsForProfile("golang")
	require.NotEmpty(t, first)
	first[0] = "/corrupted"
	extended := append(first, "/extra")
	assert.Contains(t, extended, "/extra")

	second := CacheMountsForProfile("golang")
	assert.Equal(t, "/go/pkg/mod", second[0])
	assert.NotContains(t, second, "/extra")
}

func TestWithProfileCaches(t *testing.T) {
	const (
		profile   = "golang"
		baseImage = "golang:1.22"
	)

	cc := &containerConfig{}
	WithProfileCaches(profile, baseImage)(cc)

	paths := CacheMountsForProfile(profile)
	require.Len(t, cc.cacheVolumes, len(paths))

	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		key, ok := cc.cacheVolumes[path]
		require.True(t, ok, "missing cache volume for %s", path)
		assert.Equal(t, CacheKeyForTool(baseImage, profile, path), key)
		_, duplicate := seen[key]
		assert.False(t, duplicate, "duplicate cache key for %s", path)
		seen[key] = struct{}{}
	}

	other := &containerConfig{}
	WithProfileCaches(profile, baseImage)(other)
	assert.Equal(t, cc.cacheVolumes, other.cacheVolumes)
}
