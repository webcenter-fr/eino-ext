package dagger

import (
	"crypto/sha256"
	"fmt"

	"emperror.dev/errors"
	"dagger.io/dagger"
)

func (c *Client) CacheVolume(key string) (*dagger.CacheVolume, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}
	return c.client.CacheVolume(key), nil
}

func (c *Client) CacheVolumeLocked(key string) (*dagger.CacheVolume, error) {
	if c.client == nil {
		return nil, errors.New("client not connected")
	}
	return c.client.CacheVolume(key, dagger.CacheVolumeOpts{
		Sharing: dagger.CacheSharingModeLocked,
	}), nil
}

func CacheKeyForProfile(imageName, profileName string) string {
	h := sha256.New()
	h.Write([]byte(imageName + "\x00" + profileName))
	return fmt.Sprintf("shell-%x", h.Sum(nil))[:48]
}

func CacheKeyForTool(imageName, profileName, toolName string) string {
	h := sha256.New()
	h.Write([]byte(imageName + "\x00" + profileName + "\x00" + toolName))
	return fmt.Sprintf("tool-%x", h.Sum(nil))[:48]
}
