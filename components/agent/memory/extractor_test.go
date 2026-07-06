package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseExtractionResponse_JSONArray(t *testing.T) {
	content := `[{"content":"user prefers Go","category":"preference","source":"user","confidence":0.95}]`
	results, err := parseExtractionResponse(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "user prefers Go", results[0].Content)
	assert.Equal(t, "preference", results[0].Category)
	assert.Equal(t, "user", results[0].Source)
	assert.Equal(t, 0.95, results[0].Confidence)
}

func TestParseExtractionResponse_LowConfidenceFiltered(t *testing.T) {
	content := `[
		{"content":"good fact","category":"fact","source":"user","confidence":0.9},
		{"content":"uncertain","category":"fact","source":"observation","confidence":0.5}
	]`
	results, err := parseExtractionResponse(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "good fact", results[0].Content)
}

func TestParseExtractionResponse_LLMWrapping(t *testing.T) {
	content := "Here's the result:\n```json\n[{\"content\":\"fact\",\"category\":\"fact\",\"source\":\"user\",\"confidence\":0.8}]\n```"
	results, err := parseExtractionResponse(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestParseExtractionResponse_MarkdownFenceNoLanguage(t *testing.T) {
	content := "```\n[{\"content\":\"fact\",\"category\":\"fact\",\"source\":\"user\",\"confidence\":0.8}]\n```"
	results, err := parseExtractionResponse(content)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestParseExtractionResponse_InvalidJSON(t *testing.T) {
	content := "not json at all with no brackets or braces anywhere"
	results, err := parseExtractionResponse(content)
	assert.NoError(t, err)
	assert.Empty(t, results)
}

func TestParseExtractionResponse_BrokenJSON(t *testing.T) {
	content := "[{\"bad\":}]"
	_, err := parseExtractionResponse(content)
	assert.Error(t, err)
}

func TestParseExtractionResponse_EmptyString(t *testing.T) {
	results, err := parseExtractionResponse("")
	assert.NoError(t, err)
	assert.Empty(t, results)
}

func TestParseExtractionResponse_EmptyContentFiltered(t *testing.T) {
	content := `[{"content":"","category":"fact","source":"user","confidence":0.9}]`
	results, err := parseExtractionResponse(content)
	assert.NoError(t, err)
	assert.Empty(t, results)
}

func TestExtractJSONBlock_Array(t *testing.T) {
	assert.Equal(t, `[1,2,3]`, extractJSONBlock("some text [1,2,3] more text"))
}

func TestExtractJSONBlock_Object(t *testing.T) {
	assert.Equal(t, `{"a":1}`, extractJSONBlock("data: {\"a\":1} end"))
}

func TestExtractJSONBlock_NoJSON(t *testing.T) {
	assert.Equal(t, "", extractJSONBlock("plain text"))
}

func TestExtractJSONBlock_MarkdownFence(t *testing.T) {
	assert.Equal(t, `[1,2,3]`, extractJSONBlock("```json\n[1,2,3]\n```"))
}

func TestExtractJSONBlock_MarkdownFenceNoLang(t *testing.T) {
	assert.Equal(t, `[1,2,3]`, extractJSONBlock("```\n[1,2,3]\n```"))
}

func TestStripMarkdownFences(t *testing.T) {
	assert.Equal(t, "hello", stripMarkdownFences("```json\nhello\n```"))
	assert.Equal(t, "hello", stripMarkdownFences("```\nhello\n```"))
	assert.Equal(t, "hello", stripMarkdownFences("hello"))
	assert.Equal(t, "hello", stripMarkdownFences("```\nhello"))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "he...", truncate("hello world", 2))
}
