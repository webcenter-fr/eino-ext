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

func TestDryRunGuidance(t *testing.T) {
	// The guidance text is a contract shared with the MCP server adapter
	// (libs/mcp): it instructs the LLM to show the preview and ask for
	// confirmation before re-calling with confirmed=true.
	assert.Equal(t,
		"\n\nDRY-RUN RESULT: This is a preview of what would happen. Show this to the user and ask for confirmation before re-calling with confirmed=true.",
		DryRunGuidance)
	assert.Contains(t, DryRunGuidance, "DRY-RUN RESULT")
	assert.Contains(t, DryRunGuidance, "confirmed=true")
}
