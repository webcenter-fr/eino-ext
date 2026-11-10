package serverconfig

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// OpsHandler returns the unauthenticated operations handler. /healthz is
// always 200 once the process is serving; /readyz calls ready and returns 503
// while it reports an error; /metrics exposes Prometheus metrics.
func OpsHandler(ready func() error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready != nil {
			if err := ready(); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
