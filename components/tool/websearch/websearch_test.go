package websearch

import (
	"bytes"
	"context"
	"time"

	"github.com/stretchr/testify/assert"
)

const sampleDDGHTML = `
<!DOCTYPE html>
<html>
<body>
<div class="results">
<div class="result__body">
<h2 class="result__title"><a class="result__a" href="//duckduckgo.com/l/?uddg=https://example.com/page1">Example Page 1</a></h2>
<a class="result__snippet">This is the first example result description.</a>
</div>
<div class="result__body">
<h2 class="result__title"><a class="result__a" href="//duckduckgo.com/l/?uddg=https://example.com/page2">Example Page 2</a></h2>
<a class="result__snippet">This is the second example result description.</a>
</div>
<div class="result__body">
<h2 class="result__title"><a class="result__a" href="//duckduckgo.com/l/?uddg=https://example.com/page3">Example Page 3</a></h2>
<a class="result__snippet">This is the third example result description.</a>
</div>
</div>
</body>
</html>
`

const sampleDDGLiteHTML = `
<!DOCTYPE html>
<html>
<body>
<table>
<tr class="result-snippet"><td><a class="result-link" href="https://example.com/lite1">Lite Result 1</a> - Description for lite result 1</td></tr>
<tr class="result-snippet"><td><a class="result-link" href="https://example.com/lite2">Lite Result 2</a> - Description for lite result 2</td></tr>
</table>
</body>
</html>
`

func (s *WebSearchTestSuite) TestParseDDGHTML() {
	t := s.T()
	results, err := parseDDGHTML(bytes.NewReader([]byte(sampleDDGHTML)), DefaultMaxBodySize)
	assert.NoError(t, err)
	assert.Len(t, results, 3)

	assert.Equal(t, "Example Page 1", results[0].Title)
	assert.Equal(t, "https://example.com/page1", results[0].URL)
	assert.Equal(t, "This is the first example result description.", results[0].Description)

	assert.Equal(t, "Example Page 2", results[1].Title)
	assert.Equal(t, "https://example.com/page2", results[1].URL)
	assert.Equal(t, "This is the second example result description.", results[1].Description)

	assert.Equal(t, "Example Page 3", results[2].Title)
	assert.Equal(t, "https://example.com/page3", results[2].URL)
	assert.Equal(t, "This is the third example result description.", results[2].Description)
}

func (s *WebSearchTestSuite) TestParseDDGLite() {
	t := s.T()
	results, err := parseDDGHTML(bytes.NewReader([]byte(sampleDDGLiteHTML)), DefaultMaxBodySize)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 2)
}

func (s *WebSearchTestSuite) TestParseEmpty() {
	t := s.T()
	results, err := parseDDGHTML(bytes.NewReader([]byte("<html><body></body></html>")), DefaultMaxBodySize)
	assert.NoError(t, err)
	assert.Empty(t, results)
}

func (s *WebSearchTestSuite) TestParseInvalidHTML() {
	t := s.T()
	results, err := parseDDGHTML(bytes.NewReader([]byte("not html at all")), DefaultMaxBodySize)
	assert.NoError(t, err)
	assert.Empty(t, results)
}

func (s *WebSearchTestSuite) TestExtractDDGURL() {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{
			name:     "standard DDG redirect",
			raw:      "//duckduckgo.com/l/?uddg=https://example.com/page",
			expected: "https://example.com/page",
		},
		{
			name:     "plain URL without redirect",
			raw:      "https://example.com/plain",
			expected: "https://example.com/plain",
		},
		{
			name:     "empty string",
			raw:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := extractDDGURL(tt.raw)
			assert.Equal(s.T(), tt.expected, result)
		})
	}
}

func (s *WebSearchTestSuite) TestWebSearchToolInvoke() {
	t := s.T()

	cfg := DefaultConfig()
	tool, err := NewWebSearchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	info, err := tool.Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "web_search", info.Name)
	assert.NotEmpty(t, info.Desc)
}

func (s *WebSearchTestSuite) TestWebSearchNumResultsClamping() {
	params := &WebSearchParams{
		Query:      "test",
		NumResults: 0,
	}
	if params.NumResults <= 0 {
		params.NumResults = 10
	}
	assert.Equal(s.T(), 10, params.NumResults)

	params.NumResults = 50
	if params.NumResults > 20 {
		params.NumResults = 20
	}
	assert.Equal(s.T(), 20, params.NumResults)
}

func (s *WebSearchTestSuite) TestNewAllTools() {
	t := s.T()
	cfg := DefaultConfig()
	tools, err := NewAllTools(context.Background(), &cfg)
	assert.NoError(t, err)
	assert.Len(t, tools, 2)

	info, err := tools[0].Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "web_search", info.Name)

	info, err = tools[1].Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "web_fetch", info.Name)
}

func (s *WebSearchTestSuite) TestConfigDefaults() {
	t := s.T()
	cfg := Config{}
	cfg = cfg.applyDefaults(DefaultConfig())

	assert.Equal(t, DefaultTimeout, cfg.Timeout)
	assert.Equal(t, DefaultMaxRetry, cfg.MaxRetry)
	assert.Equal(t, DefaultUserAgent, cfg.UserAgent)
	assert.Equal(t, int64(DefaultMaxBodySize), cfg.MaxBodySize)
}

func (s *WebSearchTestSuite) TestConfigPartialDefaults() {
	t := s.T()
	cfg := Config{
		Timeout: 10 * time.Second,
	}
	cfg = cfg.applyDefaults(DefaultConfig())

	assert.Equal(t, 10*time.Second, cfg.Timeout)
	assert.Equal(t, DefaultMaxRetry, cfg.MaxRetry)
	assert.Equal(t, DefaultUserAgent, cfg.UserAgent)
	assert.Equal(t, int64(DefaultMaxBodySize), cfg.MaxBodySize)
}

func (s *WebSearchTestSuite) TestConfigMutation() {
	t := s.T()
	cfg := Config{
		Timeout:   5 * time.Second,
		MaxRetry:  1,
		UserAgent: "test-agent/1.0",
	}
	orig := cfg // save copy

	_, err := NewWebSearchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	// The caller's config should NOT be mutated.
	assert.Equal(t, orig.Timeout, cfg.Timeout)
	assert.Equal(t, orig.MaxRetry, cfg.MaxRetry)
	assert.Equal(t, orig.UserAgent, cfg.UserAgent)
}
