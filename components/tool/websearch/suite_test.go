package websearch

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"
)

// WebSearchTestSuite provides a shared httptest server and configuration for all
// websearch package tests.
type WebSearchTestSuite struct {
	suite.Suite
	searxngServer *httptest.Server
	searxngMux    *http.ServeMux
	fetchServer   *httptest.Server
	fetchMux      *http.ServeMux
}

func TestWebSearchSuite(t *testing.T) {
	suite.Run(t, new(WebSearchTestSuite))
}

func (s *WebSearchTestSuite) SetupSuite() {
	// Create a mock SearXNG server.
	s.searxngMux = http.NewServeMux()
	s.searxngServer = httptest.NewServer(s.searxngMux)

	// Create a mock fetch server for web_fetch tests.
	s.fetchMux = http.NewServeMux()
	s.fetchServer = httptest.NewServer(s.fetchMux)
}

func (s *WebSearchTestSuite) TearDownSuite() {
	s.searxngServer.Close()
	s.fetchServer.Close()
}

func (s *WebSearchTestSuite) BeforeTest(suiteName, testName string) {
	// Reset handlers between tests.
	s.searxngMux = http.NewServeMux()
	s.searxngServer.Config.Handler = s.searxngMux

	s.fetchMux = http.NewServeMux()
	s.fetchServer.Config.Handler = s.fetchMux
}

func (s *WebSearchTestSuite) AfterTest(suiteName, testName string) {
}

// testSearchConfig returns a Config suitable for httptest.Server-based search tests.
func testSearchConfig(searxngURL string) Config {
	cfg := DefaultConfig()
	cfg.SearxngURL = searxngURL
	cfg.SkipSSRFCheck = true
	return cfg
}
