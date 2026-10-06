package safety

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWriteToolSet(t *testing.T) {
	set := NewWriteToolSet([]string{"a", "b", "a"})
	assert.Len(t, set, 2)
	assert.True(t, set["a"])
	assert.True(t, set["b"])
	assert.False(t, set["c"])

	assert.Empty(t, NewWriteToolSet(nil))
	assert.Empty(t, NewWriteToolSet([]string{}))
}
