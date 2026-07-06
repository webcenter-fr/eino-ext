package docid

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeBaseID(t *testing.T) {
	id1, err := ComputeBaseID("test/path/doc.md")
	assert.NoError(t, err)
	assert.NotEmpty(t, id1)

	// Same input produces same output
	id2, err := ComputeBaseID("test/path/doc.md")
	assert.NoError(t, err)
	assert.Equal(t, id1, id2)

	// Different input produces different output
	id3, err := ComputeBaseID("test/path/other.md")
	assert.NoError(t, err)
	assert.NotEqual(t, id1, id3)

	// Empty input is valid
	id4, err := ComputeBaseID("")
	assert.NoError(t, err)
	assert.NotEmpty(t, id4)
}

func TestComputeContentHash(t *testing.T) {
	h1 := ComputeContentHash([]byte("hello"))
	assert.NotEmpty(t, h1)
	assert.Len(t, h1, 32) // 128 bit = 32 hex chars

	// Same content produces same hash
	h2 := ComputeContentHash([]byte("hello"))
	assert.Equal(t, h1, h2)

	// Different content produces different hash
	h3 := ComputeContentHash([]byte("world"))
	assert.NotEqual(t, h1, h3)

	// Empty content is valid
	h4 := ComputeContentHash([]byte{})
	assert.NotEmpty(t, h4)
}
