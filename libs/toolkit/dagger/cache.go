package dagger

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"emperror.dev/errors"
)

var (
	aptCachePaths = []string{"/var/cache/apt", "/var/lib/apt"}
	apkCachePaths = []string{"/var/cache/apk"}
)

// profileToolchainCaches maps a profile name to its language toolchain cache
// paths. All named profiles are Debian-based, so apt paths are appended to
// them; the default Alpine image only gets apk paths.
var profileToolchainCaches = map[string][]string{
	"golang": {"/go/pkg/mod", "/root/.cache/go-build"},
	"node":   {"/root/.npm"},
	"python": {"/root/.cache/pip"},
	"java":   {"/root/.m2", "/root/.gradle/caches"},
	"rust":   {"/usr/local/cargo/registry", "/usr/local/cargo/git"},
	"php":    {"/root/.composer/cache"},
}

// CacheMountsForProfile returns the container paths that should be backed by
// cache volumes for a given profile name.
//
// The returned slice contains the language toolchain cache paths followed by the
// OS package-manager cache paths. Profile names match profile.DefaultImageMap
// keys and are matched case-insensitively. An unknown or empty name returns
// only the package-manager paths for the default (Alpine) image.
//
// Paths are deliberately outside /workspace: a cache mounted inside the
// workspace would shadow part of the tree that ExportWorkspace copies back.
func CacheMountsForProfile(profileName string) []string {
	toolchain, ok := profileToolchainCaches[strings.ToLower(profileName)]
	if !ok {
		return append([]string(nil), apkCachePaths...)
	}

	paths := make([]string, 0, len(toolchain)+len(aptCachePaths))
	paths = append(paths, toolchain...)
	paths = append(paths, aptCachePaths...)
	return paths
}

// WithProfileCaches mounts every CacheMountsForProfile path for profileName as a
// cache volume, deriving a stable per-path key from baseImage, profileName and
// the path. It is the convenience form of calling WithCacheVolume once per path.
func WithProfileCaches(profileName, baseImage string) ContainerOpt {
	return func(c *containerConfig) {
		for _, path := range CacheMountsForProfile(profileName) {
			WithCacheVolume(path, CacheKeyForTool(baseImage, profileName, path))(c)
		}
	}
}

// CacheVolume returns a Dagger cache volume for the given key.
func (c *Client) CacheVolume(key string) (*dagger.CacheVolume, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}
	return c.client.CacheVolume(key), nil
}

// CacheVolumeLocked returns a Dagger cache volume with locked sharing mode for the given key.
func (c *Client) CacheVolumeLocked(key string) (*dagger.CacheVolume, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}
	return c.client.CacheVolume(key, dagger.CacheVolumeOpts{
		Sharing: dagger.CacheSharingModeLocked,
	}), nil
}

// CacheKeyForProfile builds a cache key from image name and profile name.
func CacheKeyForProfile(imageName, profileName string) string {
	h := sha256.New()
	h.Write([]byte(imageName + "\x00" + profileName))
	return fmt.Sprintf("shell-%x", h.Sum(nil))[:48]
}

// CacheKeyForTool builds a cache key from image name, profile name and tool name.
func CacheKeyForTool(imageName, profileName, toolName string) string {
	h := sha256.New()
	h.Write([]byte(imageName + "\x00" + profileName + "\x00" + toolName))
	return fmt.Sprintf("tool-%x", h.Sum(nil))[:48]
}
