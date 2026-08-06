// Package convertor provides eino tools for data format conversion.
package convertor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertorTool_Invoke(t *testing.T) {
	ctx := context.Background()

	makeTool := func() *ConvertorTool {
		return &ConvertorTool{}
	}

	t.Run("yaml to json", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      "key: value\nlist:\n  - a\n  - b\n",
			InputType:  "yaml",
			OutputType: "json",
		}

		result, err := tool.Invoke(ctx, params)
		assert.NoError(t, err)

		var parsed map[string]any
		assert.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.Equal(t, "value", parsed["key"])
	})

	t.Run("json to yaml", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      `{"key":"value","list":["a","b"]}`,
			InputType:  "json",
			OutputType: "yaml",
		}

		result, err := tool.Invoke(ctx, params)
		assert.NoError(t, err)
		assert.Contains(t, result, "key:")
		assert.Contains(t, result, "value")
	})

	t.Run("invalid input type", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      "some data",
			InputType:  "xml",
			OutputType: "json",
		}

		_, err := tool.Invoke(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("invalid output type", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      `{"key":"value"}`,
			InputType:  "json",
			OutputType: "xml",
		}

		_, err := tool.Invoke(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("input too large", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      strings.Repeat("x", maxInputSize+1),
			InputType:  "json",
			OutputType: "json",
		}

		_, err := tool.Invoke(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "input too large")
		assert.Contains(t, err.Error(), fmt.Sprintf("%d", maxInputSize+1))
	})

	t.Run("invalid yaml input", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      "\tinvalid: yaml: with: tabs\n",
			InputType:  "yaml",
			OutputType: "json",
		}

		_, err := tool.Invoke(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal YAML input")
	})

	t.Run("invalid json input", func(t *testing.T) {
		tool := makeTool()
		params := &ConvertorParams{
			Input:      `{invalid json}`,
			InputType:  "json",
			OutputType: "yaml",
		}

		_, err := tool.Invoke(ctx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal JSON input")
	})
}
