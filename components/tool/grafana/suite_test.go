package grafana

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

type ToolTestSuite struct {
	suite.Suite
	server  *httptest.Server
	configs Configs
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

	// GET /api/search — all dashboards
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		query := r.URL.Query().Get("query")
		limit := r.URL.Query().Get("limit")

		allDashboards := `[
			{
				"id": 1,
				"uid": "abc123",
				"title": "Production Overview",
				"url": "/d/abc123/production-overview",
				"type": "dash-db",
				"tags": ["prod"],
				"folderTitle": "Infrastructure",
				"folderUid": "folder-1",
				"starred": true
			},
			{
				"id": 2,
				"uid": "def456",
				"title": "Staging Dashboard",
				"url": "/d/def456/staging-dashboard",
				"type": "dash-db",
				"tags": ["staging"],
				"folderTitle": "",
				"folderUid": "folder-2",
				"starred": false
			}
		]`

		w.Header().Set("Content-Type", "application/json")

		if query == "Production" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"id": 1,
					"uid": "abc123",
					"title": "Production Overview",
					"url": "/d/abc123/production-overview",
					"type": "dash-db",
					"tags": ["prod"],
					"folderTitle": "Infrastructure",
					"folderUid": "folder-1",
					"starred": true
				}
			]`))
			return
		}

		if limit == "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"id": 1,
					"uid": "abc123",
					"title": "Production Overview",
					"url": "/d/abc123/production-overview",
					"type": "dash-db",
					"tags": ["prod"],
					"folderTitle": "Infrastructure",
					"folderUid": "folder-1",
					"starred": true
				}
			]`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(allDashboards))
	})

	// GET /api/dashboards/uid/abc123
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"dashboard": {
				"uid": "abc123",
				"title": "Production Overview",
				"tags": ["prod"],
				"panels": [{"id": 1, "title": "CPU Usage", "type": "graph"}]
			},
			"meta": {
				"folderTitle": "Infrastructure",
				"folderUid": "folder-1",
				"folderId": 1,
				"version": 3,
				"createdBy": "admin",
				"updatedBy": "admin"
			}
		}`))
	})

	// GET /api/dashboards/uid/nonexistent → 404
	mux.HandleFunc("/api/dashboards/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "Dashboard not found"}`))
	})

	// GET /api/dashboards/uid/protected-uid
	mux.HandleFunc("/api/dashboards/uid/protected-uid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"dashboard": {
				"uid": "protected-uid",
				"title": "Kubernetes Monitoring",
				"tags": ["infrastructure"]
			},
			"meta": {
				"folderUid": "infra-folder",
				"version": 1,
				"createdBy": "admin",
				"updatedBy": "admin"
			}
		}`))
	})

	// POST /api/dashboards/db
	mux.HandleFunc("/api/dashboards/db", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 10,
			"uid": "new-uid",
			"url": "/d/new-uid/my-new-dashboard",
			"status": "success",
			"version": 1,
			"slug": "my-new-dashboard"
		}`))
	})

	// GET /api/datasources — list
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{
				"id": 1, "uid": "ds-prom", "orgId": 1, "name": "Prometheus",
				"type": "prometheus", "typeName": "Prometheus", "access": "proxy",
				"url": "http://prom:9090", "isDefault": true, "readOnly": false,
				"version": 3, "password": "should-not-leak",
				"jsonData": {"timeInterval":"15s","httpHeaderValue":"secret-bearer"}
			},
			{
				"id": 2, "uid": "ds-loki", "orgId": 1, "name": "Loki",
				"type": "loki", "typeName": "Loki", "access": "proxy",
				"url": "http://loki:3100", "isDefault": false, "readOnly": false,
				"version": 1,
				"jsonData": {"maxLines": 1000}
			}
		]`))
	})

	// GET /api/datasources/uid/ds-prom
	mux.HandleFunc("/api/datasources/uid/ds-prom", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 1, "uid": "ds-prom", "orgId": 1, "name": "Prometheus",
			"type": "prometheus", "typeName": "Prometheus", "typeLogoUrl": "public/app/plugins/datasource/prometheus/img/prom.svg",
			"access": "proxy", "url": "http://prom:9090", "user": "", "database": "",
			"basicAuth": false, "withCredentials": false, "isDefault": true,
			"jsonData": {"timeInterval":"15s","httpHeaderValue":"secret-bearer"},
			"secureJsonFields": {"httpHeaderValue": true},
			"readOnly": false, "version": 3, "password": "should-not-leak", "basicAuthPassword": ""
		}`))
	})

	// GET /api/datasources/uid/nonexistent → 404
	mux.HandleFunc("/api/datasources/uid/nonexistent", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"data source not found"}`))
	})

	t.server = httptest.NewServer(mux)

	t.configs = Configs{
		"test": {
			URL: t.server.URL,
			ProtectedDashboards: ProtectedDashboardsConfig{
				UIDs:          []string{"protected-uid"},
				TitlePrefixes: []string{"Kubernetes "},
				Tags:          []string{"infrastructure"},
			},
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
