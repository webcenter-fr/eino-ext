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

	// GET /api/dashboards/uid/abc123 and DELETE /api/dashboards/uid/abc123
	mux.HandleFunc("/api/dashboards/uid/abc123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"Production Overview","message":"Dashboard deleted","id":1}`))
			return
		}
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

	// GET /api/datasources/uid/ds-loki — Loki datasource describe
	mux.HandleFunc("/api/datasources/uid/ds-loki", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 2, "uid": "ds-loki", "orgId": 1, "name": "Loki",
			"type": "loki", "typeName": "Loki", "access": "proxy",
			"url": "http://loki:3100", "isDefault": false, "readOnly": false, "version": 1,
			"jsonData": {"maxLines": 1000}
		}`))
	})

	// GET /api/datasources/uid/ds-mysql — unsupported-type datasource
	mux.HandleFunc("/api/datasources/uid/ds-mysql", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 3, "uid": "ds-mysql", "orgId": 1, "name": "MySQL",
			"type": "mysql", "typeName": "MySQL", "access": "proxy",
			"url": "http://mysql:3306", "isDefault": false, "readOnly": false, "version": 1
		}`))
	})

	// ─── Datasource proxy query endpoints ─────────────────────────────────

	// Prometheus instant query: keyed on the query param.
	mux.HandleFunc("/api/datasources/proxy/uid/ds-prom/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Query().Get("query") {
		case "empty":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		case "bad":
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"invalid parameter 'query': parse error"}`))
		case "up":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"__name__":"up","instance":"a"},"value":[1700000000,"1"]},
				{"metric":{"__name__":"up","instance":"b"},"value":[1700000000,"1"]},
				{"metric":{"__name__":"up","instance":"c"},"value":[1700000000,"1"]}
			]}}`))
		default:
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"__name__":"metric","instance":"a"},"value":[1700000000,"1"]}
			]}}`))
		}
	})

	// Prometheus range query: matrix.
	mux.HandleFunc("/api/datasources/proxy/uid/ds-prom/api/v1/query_range", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"up","instance":"a"},"values":[[1700000000,"1"],[1700000060,"2"]]}
		]}}`))
	})

	// Loki instant query: metric (vector) response.
	mux.HandleFunc("/api/datasources/proxy/uid/ds-loki/loki/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"__name__":"metric","instance":"a"},"value":[1700000000,"1"]}
		]}}`))
	})

	// Loki range query: streams response.
	mux.HandleFunc("/api/datasources/proxy/uid/ds-loki/loki/api/v1/query_range", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"app":"checkout"},"values":[[1700000000000000000,"first log line"],[1700000001000000000,"second log line"]]}
		]}}`))
	})

	// Multi-panel dashboard fixture for grafana_dashboard_validate.
	mux.HandleFunc("/api/dashboards/uid/validate-dash", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"dashboard": {
				"uid": "validate-dash",
				"title": "Validate Dashboard",
				"panels": [
					{"id": 1, "title": "CPU Usage", "type": "timeseries",
					 "datasource": {"uid": "ds-prom", "type": "prometheus"},
					 "targets": [{"refId": "A", "expr": "up", "datasource": {"uid": "ds-prom", "type": "prometheus"}}]},
					{"id": 2, "title": "Empty Panel", "type": "timeseries",
					 "datasource": {"uid": "ds-prom", "type": "prometheus"},
					 "targets": [{"refId": "A", "expr": "empty", "datasource": {"uid": "ds-prom", "type": "prometheus"}}]},
					{"id": 3, "title": "Logs", "type": "logs",
					 "datasource": {"uid": "ds-loki", "type": "loki"},
					 "targets": [{"refId": "A", "expr": "{app=\"checkout\"}", "datasource": {"uid": "ds-loki", "type": "loki"}}]},
					{"id": 4, "title": "MySQL", "type": "table",
					 "datasource": {"uid": "ds-mysql", "type": "mysql"},
					 "targets": [{"refId": "A", "expr": "SELECT 1", "datasource": {"uid": "ds-mysql", "type": "mysql"}}]},
					{"id": 5, "title": "No DS", "type": "timeseries",
					 "targets": [{"refId": "A", "expr": "up"}]}
				]
			},
			"meta": {"folderUid": "folder-1", "version": 1}
		}`))
	})

	// v2 dashboard fixture (elements map) for grafana_dashboard_validate.
	mux.HandleFunc("/api/dashboards/uid/v2-dash", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"dashboard": {
				"uid": "v2-dash",
				"title": "V2 Dashboard",
				"elements": {
					"panel-1": {
						"kind": "Panel",
						"spec": {
							"id": 1,
							"title": "V2 CPU",
							"type": "timeseries",
							"data": {
								"spec": {
									"queries": [
										{
											"refId": "A",
											"spec": {
												"query": {"group": "prometheus", "spec": {"expr": "up"}},
												"datasource": {"name": "ds-prom"}
											}
										}
									]
								}
							}
						}
					}
				}
			},
			"meta": {"folderUid": "folder-1", "version": 1}
		}`))
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
