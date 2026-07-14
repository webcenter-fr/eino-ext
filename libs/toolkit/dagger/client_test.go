package dagger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCacheKeyForProfile(t *testing.T) {
	key1 := CacheKeyForProfile("golang:1.22", "golang")
	key2 := CacheKeyForProfile("golang:1.22", "golang")
	key3 := CacheKeyForProfile("node:20", "node")

	assert.Equal(t, key1, key2, "same inputs should produce same key")
	assert.NotEqual(t, key1, key3, "different inputs should produce different keys")
	assert.NotEmpty(t, key1)
}

func TestCacheKeyForTool(t *testing.T) {
	key1 := CacheKeyForTool("golang:1.22", "golang", "go-test")
	key2 := CacheKeyForTool("golang:1.22", "golang", "go-test")
	key3 := CacheKeyForTool("golang:1.22", "golang", "go-build")

	assert.Equal(t, key1, key2)
	assert.NotEqual(t, key1, key3)
	assert.NotEmpty(t, key1)
}
