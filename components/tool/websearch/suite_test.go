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
	searchServer  *httptest.Server
	searchMux     *http.ServeMux
	fetchServer   *httptest.Server
	fetchMux      *http.ServeMux
}

func TestWebSearchSuite(t *testing.T) {
	suite.Run(t, new(WebSearchTestSuite))
}

func (s *WebSearchTestSuite) SetupSuite() {
	// Create a mock DuckDuckGo server.
	s.searchMux = http.NewServeMux()
	s.searchServer = httptest.NewServer(s.searchMux)

	// Create a mock fetch server for web_fetch tests.
	s.fetchMux = http.NewServeMux()
	s.fetchServer = httptest.NewServer(s.fetchMux)
}

func (s *WebSearchTestSuite) TearDownSuite() {
	s.searchServer.Close()
	s.fetchServer.Close()
}

func (s *WebSearchTestSuite) BeforeTest(suiteName, testName string) {
	// Reset handlers between tests.
	s.searchMux = http.NewServeMux()
	s.searchServer.Config.Handler = s.searchMux

	s.fetchMux = http.NewServeMux()
	s.fetchServer.Config.Handler = s.fetchMux
}

func (s *WebSearchTestSuite) AfterTest(suiteName, testName string) {
}
