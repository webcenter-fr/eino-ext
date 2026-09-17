package argocd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

type ToolTestSuite struct {
	suite.Suite
	server  *httptest.Server
	configs Configs

	// lastClusterGet records the most recent cluster-get request line as
	// "METHOD /path?query" so tests can assert the name-based id encoding
	// (GET /api/v1/clusters/<name>?id.type=name) required by issue #6.
	lastClusterGet string
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

	// Applications list
	mux.HandleFunc("/api/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Flat JSON — goargocdclient embeds ObjectMeta directly
			_, _ = w.Write([]byte(`{
			"items": [
					{
						"name": "my-app",
						"namespace": "argocd",
						"spec": {"project": "default", "source": {"repoURL": "https://git.example.com/repo", "path": "overlays/prod"}},
						"status": {"health": {"status": "Healthy"}, "sync": {"status": "Synced", "revision": "abc123"}}
					},
					{
						"name": "other-app",
						"namespace": "argocd",
						"spec": {"project": "production", "source": {"repoURL": "https://git.example.com/other"}},
						"status": {"health": {"status": "Degraded"}, "sync": {"status": "OutOfSync"}}
					}
				]
			}`))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"name": "my-new-app",
				"spec": {"project": "default"}
			}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Applications get/delete
	mux.HandleFunc("/api/v1/applications/my-app", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"name": "my-app",
				"namespace": "argocd",
				"spec": {"project": "default", "source": {"repoURL": "https://git.example.com/repo", "path": "overlays/prod"}},
				"status": {"health": {"status": "Healthy"}, "sync": {"status": "Synced", "revision": "abc123"}}
			}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Application sync
	mux.HandleFunc("/api/v1/applications/my-app/sync", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"name": "my-app",
			"status": {"sync": {"status": "Synced"}}
		}`))
	})

	// Application not found
	mux.HandleFunc("/api/v1/applications/non-existent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "not found", "message": "application not found"}`))
	})

	// Projects list
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items": [
				{"name": "default", "spec": {"description": "Default project"}},
				{"name": "production", "spec": {"description": "Production project"}}
			]
		}`))
	})

	// Project describe
	mux.HandleFunc("/api/v1/projects/default", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"name": "default",
			"spec": {"description": "Default project", "sourceRepos": ["*"]}
		}`))
	})

	// Certificates
	mux.HandleFunc("/api/v1/certificates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items": [
				{
					"certInfo": "SSL certificate for *.example.com",
					"certType": "https",
					"serverName": "https://argocd.example.com",
					"certSubType": "server"
				},
				{
					"certInfo": "Git SSH host key",
					"certType": "ssh",
					"serverName": "github.com",
					"certSubType": "ssh-known-hosts"
				}
			]
		}`))
	})

	// Clusters list
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items": [
				{
					"name": "my-cluster",
					"server": "https://cluster1.example.com",
					"project": "production",
					"connectionState": {"status": "Successful"},
					"serverVersion": "1.30"
				},
				{
					"name": "in-cluster",
					"server": "https://kubernetes.default.svc",
					"project": "default",
					"connectionState": {"status": "Successful"},
					"serverVersion": "1.29"
				}
			]
		}`))
	})

	// Cluster get. ArgoCD's REST route is GET /api/v1/clusters/{id.value}; a name
	// lookup is expressed as the path segment plus ?id.type=name. The broken form
	// GET /api/v1/clusters/?name=... (issue #6) is rejected with 400.
	mux.HandleFunc("/api/v1/clusters/", func(w http.ResponseWriter, r *http.Request) {
		// Record the request line using the escaped path so tests can assert the
		// exact wire encoding (r.URL.Path is already percent-decoded).
		t.lastClusterGet = r.Method + " " + r.URL.EscapedPath() + "?" + r.URL.RawQuery

		if r.URL.Query().Get("id.type") != "name" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": "invalid id.type", "message": "expected id.type=name"}`))
			return
		}

		// r.URL.Path is already percent-decoded by net/http, so the path segment
		// is the cluster name as grpc-gateway would see it. Do NOT call
		// url.PathUnescape here: that would double-decode names containing a
		// literal '%' (e.g. "a%2Fb" would wrongly become "a/b").
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/clusters/")
		if name == "non-existent" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "not found", "message": "cluster not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Echo the requested name back so tests can assert that the path
		// segment round-trips (including names with URL-special characters).
		_, _ = fmt.Fprintf(w, `{
			"name": %s,
			"server": "https://cluster1.example.com",
			"project": "production",
			"connectionState": {"status": "Successful"},
			"serverVersion": "1.30",
			"info": {
				"applicationsCount": 42,
				"connectionState": {"status": "Successful"}
			}
		}`, strconv.Quote(name))
	})

	// Repositories list
	mux.HandleFunc("/api/v1/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items": [
				{
					"repo": "https://github.com/myorg/myapp.git",
					"type": "git",
					"name": "myapp-repo",
					"project": "default",
					"connectionState": {"status": "Successful"}
				},
				{
					"repo": "https://github.com/myorg/otherapp.git",
					"type": "git",
					"name": "otherapp-repo",
					"project": "production",
					"connectionState": {"status": "Failed", "message": "auth error"}
				}
			]
		}`))
	})

	// Repository get
	mux.HandleFunc("/api/v1/repositories/https%3A%2F%2Fgithub.com%2Fmyorg%2Fmyapp.git", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"repo": "https://github.com/myorg/myapp.git",
			"type": "git",
			"name": "myapp-repo",
			"project": "default",
			"enableLfs": false,
			"enableOCI": false,
			"insecure": false,
			"connectionState": {"status": "Successful", "message": "Connected"}
		}`))
	})

	t.server = httptest.NewServer(mux)

	t.configs = Configs{
		"test": {
			URL: t.server.URL,
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
