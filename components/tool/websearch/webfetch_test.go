package websearch

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/stretchr/testify/assert"
)

// testFetchConfig returns a Config suitable for httptest.Server-based tests.
func testFetchConfig() Config {
	cfg := DefaultConfig()
	cfg.SkipSSRFCheck = true
	cfg.Timeout = 10 * time.Second
	return cfg
}

func (s *WebSearchTestSuite) TestWebFetchToolInvoke() {
	t := s.T()

	s.fetchMux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><h1>Hello World</h1><p>Test content</p></body></html>"))
	})

	cfg := testFetchConfig()
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	info, err := tool.Info(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "web_fetch", info.Name)
	assert.NotEmpty(t, info.Desc)
}

func (s *WebSearchTestSuite) TestFetchFormatMarkdown() {
	t := s.T()

	s.fetchMux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Title</h1><p>This is a paragraph.</p>"))
	})

	cfg := testFetchConfig()
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/page","format":"markdown"}`, s.fetchServer.URL))
	assert.NoError(t, err)
	assert.Contains(t, result, "Title")
}

func (s *WebSearchTestSuite) TestFetchFormatText() {
	t := s.T()

	s.fetchMux.HandleFunc("/textpage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><h1>Title</h1><p>This is text.</p></body></html>"))
	})

	cfg := testFetchConfig()
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/textpage","format":"text"}`, s.fetchServer.URL))
	assert.NoError(t, err)
	assert.Contains(t, result, "Title")
	assert.Contains(t, result, "This is text.")

	// Script/style content should not appear.
	s.fetchMux.HandleFunc("/scriptpage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><script>alert('xss')</script></head><body><p>Safe text</p></body></html>"))
	})

	result2, err := tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/scriptpage","format":"text"}`, s.fetchServer.URL))
	assert.NoError(t, err)
	assert.NotContains(t, result2, "alert")
	assert.Contains(t, result2, "Safe text")
}

func (s *WebSearchTestSuite) TestFetchFormatHTML() {
	t := s.T()

	s.fetchMux.HandleFunc("/rawpage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><p>Raw HTML content</p></body></html>"))
	})

	cfg := testFetchConfig()
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/rawpage","format":"html"}`, s.fetchServer.URL))
	assert.NoError(t, err)
	assert.Contains(t, result, "<p>Raw HTML content</p>")
}

func (s *WebSearchTestSuite) TestFetchDefaultFormat() {
	t := s.T()

	s.fetchMux.HandleFunc("/defaultpage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>Hello</h1>"))
	})

	cfg := testFetchConfig()
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	result, err := tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/defaultpage"}`, s.fetchServer.URL))
	assert.NoError(t, err)
	// Should default to markdown.
	assert.Contains(t, result, "Hello")
}

func (s *WebSearchTestSuite) TestFetchInvalidURL() {
	t := s.T()

	cfg := testFetchConfig()
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	// Test non-http scheme.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"file:///etc/passwd","format":"text"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported URL scheme")

	// Test empty URL.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"","format":"text"}`)
	assert.Error(t, err)

	// Test URL with credentials.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"http://user:pass@example.com","format":"text"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credentials")
}

func (s *WebSearchTestSuite) TestFetchSSRFBlocking() {
	t := s.T()

	// Use a config WITHOUT SkipSSRFCheck to test blocking.
	cfg := DefaultConfig()
	cfg.Timeout = 10 * time.Second
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	// Test localhost blocking.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"http://127.0.0.1:8080/secret","format":"text"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private IP")

	// Test private IP blocking.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"http://10.0.0.1/admin","format":"text"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private IP")

	// Test another private IP blocking.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"http://192.168.1.1/secret","format":"text"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private IP")

	// Test link-local blocking.
	_, err = tool.InvokableRun(context.Background(),
		`{"url":"http://169.254.1.1/secret","format":"text"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private IP")
}

func (s *WebSearchTestSuite) TestFetchSizeLimit() {
	t := s.T()

	s.fetchMux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Write more than the limit.
		largeContent := make([]byte, 2048)
		for i := range largeContent {
			largeContent[i] = 'A'
		}
		_, _ = w.Write(largeContent)
	})

	cfg := testFetchConfig()
	cfg.MaxBodySize = 1024 // Small limit for testing.
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	_, err = tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/large"}`, s.fetchServer.URL))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum allowed size")
}

func (s *WebSearchTestSuite) TestFetchTimeout() {
	t := s.T()

	s.fetchMux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	cfg := testFetchConfig()
	cfg.Timeout = 100 * time.Millisecond // Very short timeout.
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	_, err = tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/slow"}`, s.fetchServer.URL))
	assert.Error(t, err)
}

func (s *WebSearchTestSuite) TestFetchPerRequestTimeout() {
	t := s.T()

	s.fetchMux.HandleFunc("/veryslow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	cfg := testFetchConfig()
	cfg.Timeout = 30 * time.Second // Config has generous timeout.
	tool, err := NewWebFetchTool(context.Background(), &cfg)
	assert.NoError(t, err)

	// Per-request timeout of 100ms should override the config's 30s.
	_, err = tool.InvokableRun(context.Background(),
		fmt.Sprintf(`{"url":"%s/veryslow","timeout":1}`, s.fetchServer.URL))
	assert.Error(t, err)
}

func (s *WebSearchTestSuite) TestFetchCloudflareDetection() {
	// Case variations should all be detected.
	body := []byte(`<html><head><title>Just a moment...</title></head><body>Cloudflare challenge ray id: abc123</body></html>`)
	assert.True(s.T(), isCloudflareBlock(body))

	body = []byte(`<html><head><title>Just a moment...</title></head><body>CLOUDFLARE challenge ray id: abc123</body></html>`)
	assert.True(s.T(), isCloudflareBlock(body))

	body = []byte(`<html><head><title>Just a moment...</title></head><body>cloudflare challenge ray id: abc123</body></html>`)
	assert.True(s.T(), isCloudflareBlock(body))

	normalBody := []byte(`<html><body><h1>Normal Page</h1></body></html>`)
	assert.False(s.T(), isCloudflareBlock(normalBody))

	emptyBody := []byte{}
	assert.False(s.T(), isCloudflareBlock(emptyBody))
}

func (s *WebSearchTestSuite) TestHTMLToText() {
	t := s.T()

	html := `<html><head><script>var x = 1;</script><style>.css{}</style></head><body><h1>Heading</h1><p>Paragraph text here.</p><div>More content.</div></body></html>`
	text, err := stripTagsToText(html)
	assert.NoError(t, err)
	assert.Contains(t, text, "Heading")
	assert.Contains(t, text, "Paragraph text here")
	assert.Contains(t, text, "More content")
	assert.NotContains(t, text, "var x = 1")
	assert.NotContains(t, text, ".css{}")
}

func (s *WebSearchTestSuite) TestHTMLToMarkdown() {
	t := s.T()

	html := `<h1>Title</h1><p>This is <strong>bold</strong> text.</p><ul><li>Item 1</li><li>Item 2</li></ul>`
	md, err := htmlToMarkdown(html)
	assert.NoError(t, err)
	assert.Contains(t, md, "Title")
	assert.Contains(t, md, "**bold**")
}

func (s *WebSearchTestSuite) TestValidateFetchURL() {
	t := s.T()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errText string
	}{
		{"valid http", "http://example.com", false, ""},
		{"valid https", "https://example.com/path?q=1", false, ""},
		{"invalid scheme", "ftp://example.com", true, "unsupported URL scheme"},
		{"javascript scheme", "javascript:alert(1)", true, "unsupported URL scheme"},
		{"file scheme", "file:///etc/passwd", true, "unsupported URL scheme"},
		{"with credentials", "http://user:pass@example.com", true, "credentials"},
		{"empty", "", true, "required"},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := validateFetchURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errText != "" {
					assert.Contains(t, err.Error(), tt.errText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func (s *WebSearchTestSuite) TestSSRFCheckPrivateIPs() {
	t := s.T()

	tests := []struct {
		name  string
		url   string
		block bool
	}{
		{"localhost v4", "http://127.0.0.1/test", true},
		{"localhost v6", "http://[::1]/test", true},
		{"private 10.x", "http://10.0.0.1/test", true},
		{"private 172.16.x", "http://172.16.0.1/test", true},
		{"private 192.168.x", "http://192.168.1.1/test", true},
		{"link-local v4", "http://169.254.1.1/test", true},
		{"link-local v6", "http://[fe80::1]/test", true},
		{"ULA v6", "http://[fc00::1]/test", true},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := checkSSRF(tt.url)
			if tt.block {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
