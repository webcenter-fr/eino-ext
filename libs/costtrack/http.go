package costtrack

import (
	"encoding/json"
	"net/http"
)

// Mount registers /metrics (Prometheus) and /cost/usage (JSON Snapshot)
// handlers on the given mux.
func (t *Tracker) Mount(mux *http.ServeMux) {
	mux.Handle("/metrics", t.PrometheusHandler())
	mux.HandleFunc("/cost/usage", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session")
		if sessionID != "" {
			snap := t.Snapshot(sessionID)
			if snap.Steps == 0 && snap.Totals.Cost == 0 {
				http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, snap)
			return
		}
		writeJSON(w, http.StatusOK, t.AllSnapshots())
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"failed to marshal response"}`, http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}
