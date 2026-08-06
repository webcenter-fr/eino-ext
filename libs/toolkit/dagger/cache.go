package dagger

import (
	"crypto/sha256"
	"fmt"

	"emperror.dev/errors"
	"dagger.io/dagger"
)

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
