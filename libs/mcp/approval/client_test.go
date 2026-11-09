package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRegister(t *testing.T) {
	var gotAuth string
	var gotBody RegisterRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"approvalId":"a1","url":"https://approve.example/a1","expiresAt":"2030-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token"})
	require.NoError(t, err)
	resp, err := c.Register(context.Background(), RegisterRequest{
		ToolName:  "grafana_dashboard_write",
		Instance:  "prod",
		Args:      json.RawMessage(`{"a":1}`),
		ArgsHash:  "hash",
		Requester: Requester{Sub: "alice"},
	})
	require.NoError(t, err)
	assert.Equal(t, "a1", resp.ApprovalID)
	assert.Equal(t, "Bearer token", gotAuth)
	assert.Equal(t, "grafana", gotBody.ServerName)
	assert.Equal(t, "grafana_dashboard_write", gotBody.ToolName)
}

func TestClientWaitDecisionSlices(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			_, _ = w.Write([]byte(`{"state":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"state":"approved","receipt":"signed"}`))
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token", PollInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	d, err := c.WaitDecision(context.Background(), "a1", 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "approved", d.State)
	assert.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestClientWaitDecisionRejectedAndExpired(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  error
	}{
		{state: "rejected", want: ErrDenied},
		{state: "expired", want: ErrExpired},
	} {
		t.Run(tc.state, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"state":"` + tc.state + `"}`))
			}))
			defer srv.Close()
			c, err := NewClient(Config{BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token"})
			require.NoError(t, err)
			_, err = c.WaitDecision(context.Background(), "a1", time.Second)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestClientWaitDecisionTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"state":"pending"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token", PollInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	_, err = c.WaitDecision(context.Background(), "a1", 50*time.Millisecond)
	assert.ErrorIs(t, err, ErrTimeout)
}

func TestClientStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{status: http.StatusNotFound, want: ErrNotFound},
		{status: http.StatusUnauthorized, want: ErrUnauthorized},
		{status: http.StatusForbidden, want: ErrForbidden},
		{status: http.StatusBadRequest, want: ErrBadRequest},
		{status: http.StatusInternalServerError, want: ErrServiceUnavailable},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			c, err := NewClient(Config{BaseURL: srv.URL, ServerName: "grafana", ServerToken: "token"})
			require.NoError(t, err)
			_, err = c.Decision(context.Background(), "a1", 0)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestClientUnreachable(t *testing.T) {
	c, err := NewClient(Config{BaseURL: "http://127.0.0.1:1", ServerName: "grafana", ServerToken: "token"})
	require.NoError(t, err)
	_, err = c.Decision(context.Background(), "a1", 0)
	assert.ErrorIs(t, err, ErrServiceUnavailable)
}
