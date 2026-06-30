package argocd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

type ToolTestSuite struct {
	suite.Suite
	server   *httptest.Server
	configs  Configs
}

func TestToolSuite(t *testing.T) {
	suite.Run(t, new(ToolTestSuite))
}

func (t *ToolTestSuite) SetupSuite() {
	logrus.SetLevel(logrus.TraceLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		DisableQuote: true,
	})

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"items": [
					{
						"metadata": {"name": "my-app", "namespace": "argocd"},
						"spec": {"project": "default", "source": {"repoURL": "https://git.example.com/repo", "path": "overlays/prod"}},
						"status": {"health": {"status": "Healthy"}, "sync": {"status": "Synced", "revision": "abc123"}}
					},
					{
						"metadata": {"name": "other-app", "namespace": "argocd"},
						"spec": {"project": "production", "source": {"repoURL": "https://git.example.com/other"}},
						"status": {"health": {"status": "Degraded"}, "sync": {"status": "OutOfSync"}}
					}
				]
			}`))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"metadata": {"name": "my-new-app"},
				"spec": {"project": "default"}
			}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/applications/my-app", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"metadata": {"name": "my-app", "namespace": "argocd"},
				"spec": {"project": "default", "source": {"repoURL": "https://git.example.com/repo", "path": "overlays/prod"}},
				"status": {"health": {"status": "Healthy"}, "sync": {"status": "Synced", "revision": "abc123"}}
			}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/applications/my-app/sync", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"metadata": {"name": "my-app"},
			"status": {"sync": {"status": "Synced"}}
		}`))
	})

	mux.HandleFunc("/api/v1/applications/non-existent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "not found", "message": "application not found"}`))
	})

	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"items": [
				{"metadata": {"name": "default"}, "spec": {"description": "Default project"}},
				{"metadata": {"name": "production"}, "spec": {"description": "Production project"}}
			]
		}`))
	})

	mux.HandleFunc("/api/v1/projects/default", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"metadata": {"name": "default"},
			"spec": {"description": "Default project", "sourceRepos": ["*"]}
		}`))
	})

	t.server = httptest.NewServer(mux)

	t.configs = Configs{
		"test": {
			ServerURL: t.server.URL,
			Token:     "test-token",
			Insecure:  false,
		},
	}
}

func (t *ToolTestSuite) TearDownSuite() {
	if t.server != nil {
		t.server.Close()
	}
}

func (t *ToolTestSuite) BeforeTest(suiteName, testName string) {
}

func (t *ToolTestSuite) AfterTest(suiteName, testName string) {
}