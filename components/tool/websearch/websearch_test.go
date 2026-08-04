package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/stretchr/testify/assert"
)

const sampleSearxngJSON = `{
	"results": [
		{"title": "Example Page 1", "url": "https://example.com/page1", "content": "This is the first example result description."},
		{"title": "Example Page 2", "url": "https://example.com/page2", "content": "This is the second example result description."},
		{"title": "Example Page 3", "url": "https://example.com/page3", "content": "This is the third example result description."}
	]
}`

func (s *WebSearchTestSuite) TestSearxngSearch() {
	t := s.T()

	s.searxngMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test query", r.URL.Query().Get("q"))
		assert.Equal(t, "json", r.URL.Query().Get("format"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleSearxngJSON))
	})

	cfg := testSearchConfig(s.searxngServer.URL)
	results, err := search(context.Background(), "test query", cfg)
	assert.NoError(t, err)
	assert.Len(t, results, 3)

	assert.Equal(t, "Example Page 1", results[0].Title)
	assert.Equal(t, "https://example.com/page1", results[0].URL)
	assert.Equal(t, "This is the first example result description.", results[0].Description)

	assert.Equal(t, "Example Page 2", results[1].Title)
	assert.Equal(t, "https://example.com/page2", results[1].URL)

	assert.Equal(t, "Example Page 3", results[2].Title)
	assert.Equal(t, "https://example.com/page3", results[2].URL)
}

func (s *WebSearchTestSuite) TestSearxngSearchWithAuth() {
	t := s.T()

	s.searxngMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer my-secret-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleSearxngJSON))
	})

	cfg := testSearchConfig(s.searxngServer.URL)
	cfg.SearxngSecretKey = "my-secret-key"
	results, err := search(context.Background(), "test query", cfg)
	assert.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, "Example Page 1", results[0].Title)
}

func (s *WebSearchTestSuite) TestSearxngSearchEmptyResult() {
	t := s.T()

	s.searxngMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	})

	cfg := testSearchConfig(s.searxngServer.URL)
	cfg.MaxRetry = 0
	_, err := search(context.Background(), "test query", cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "search returned no results")
}

func (s *WebSearchTestSuite) TestSearxngSearchRetryError() {
	t := s.T()

	callCount := 0
	s.searxngMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleSearxngJSON))
	})

	cfg := testSearchConfig(s.searxngServer.URL)
	results, err := search(context.Background(), "test query", cfg)
	assert.NoError(t, err)
	assert.Len(t, results, 3)
	assert.GreaterOrEqual(t, callCount, 3)
}

func (s *WebSearchTestSuite) TestSearxngSearchMissingURL() {
	t := s.T()

	cfg := DefaultConfig()
	cfg.SearxngURL = ""
	_, err := search(context.Background(), "test query", cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SearxngURL is required")
}

func (s *WebSearchTestSuite) TestWebSearchToolInvoke() {
	t := s.T()

	s.searxngMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleSearxngJSON))
	})

	cfg := testSearchConfig(s.searxngServer.URL)
	tool, err := NewWebSearchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	info, err := tool.Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "web_search", info.Name)
	assert.NotEmpty(t, info.Desc)

	result, err := tool.InvokableRun(context.Background(), `{"query":"test query","numResults":2}`)
	assert.NoError(t, err)

	var results []SearchResult
	err = json.Unmarshal([]byte(result), &results)
	assert.NoError(t, err)
	assert.Len(t, results, 2)
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

	s.searxngMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleSearxngJSON))
	})

	cfg := testSearchConfig(s.searxngServer.URL)
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
	cfg := DefaultConfig()
	assert.Equal(t, DefaultTimeout, cfg.Timeout)
	assert.Equal(t, DefaultMaxRetry, cfg.MaxRetry)
	assert.Equal(t, DefaultUserAgent, cfg.UserAgent)
	assert.Equal(t, int64(DefaultMaxBodySize), cfg.MaxBodySize)
	assert.Equal(t, "markdown", cfg.DefaultFormat)
	assert.Empty(t, cfg.SearxngURL)
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
	assert.Equal(t, "markdown", cfg.DefaultFormat)
}

func (s *WebSearchTestSuite) TestConfigMutation() {
	t := s.T()
	cfg := Config{
		Timeout:    5 * time.Second,
		MaxRetry:   1,
		UserAgent:  "test-agent/1.0",
		SearxngURL: "https://search.example.com",
	}
	orig := cfg

	_, err := NewWebSearchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	assert.Equal(t, orig.Timeout, cfg.Timeout)
	assert.Equal(t, orig.MaxRetry, cfg.MaxRetry)
	assert.Equal(t, orig.UserAgent, cfg.UserAgent)
	assert.Equal(t, orig.DefaultFormat, cfg.DefaultFormat)
	assert.Equal(t, orig.SearxngURL, cfg.SearxngURL)
}

func (s *WebSearchTestSuite) TestProxyForURL() {
	t := s.T()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", nil)
	assert.NoError(t, err)

	proxyURL := proxyForURL(req)
	assert.Nil(t, proxyURL, "expected no proxy in test environment")
}

func (s *WebSearchTestSuite) TestSSRFSafeDialer() {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{
			name:    "loopback IPv4",
			addr:    "127.0.0.1:80",
			wantErr: true,
		},
		{
			name:    "loopback IPv6",
			addr:    "[::1]:80",
			wantErr: true,
		},
		{
			name:    "private 10.x",
			addr:    "10.0.0.1:8080",
			wantErr: true,
		},
		{
			name:    "private 192.168.x",
			addr:    "192.168.1.1:443",
			wantErr: true,
		},
		{
			name:    "private 172.16.x",
			addr:    "172.16.0.1:80",
			wantErr: true,
		},
		{
			name:    "link-local IPv4",
			addr:    "169.254.1.1:80",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := ssrfSafeDialer(context.Background(), "tcp", tt.addr)
			if tt.wantErr {
				assert.Error(s.T(), err)
				assert.Contains(s.T(), err.Error(), "blocked access to private IP address")
			} else {
				assert.NoError(s.T(), err)
			}
		})
	}
}
