package prometheus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const amExistingAlertJSON = `[{
	"labels":{"alertname":"HighCPU","instance":"srv1"},
	"annotations":{"summary":"old summary"},
	"generatorURL":"http://gen/1",
	"startsAt":"2026-08-17T10:00:00Z",
	"updatedAt":"2026-08-17T10:01:00Z",
	"endsAt":"2026-08-17T10:30:00Z",
	"fingerprint":"fp1",
	"receivers":[{"name":"slack"}],
	"status":{"state":"active","silencedBy":[],"inhibitedBy":[],"mutedBy":[]}
}]`

const amTwoExistingAlertsJSON = `[
	{
		"labels":{"alertname":"HighCPU","instance":"srv1"},
		"annotations":{"summary":"old summary"},
		"generatorURL":"http://gen/1",
		"startsAt":"2026-08-17T10:00:00Z",
		"updatedAt":"2026-08-17T10:01:00Z",
		"endsAt":"2026-08-17T10:30:00Z",
		"fingerprint":"fp1",
		"receivers":[{"name":"slack"}],
		"status":{"state":"active","silencedBy":[],"inhibitedBy":[],"mutedBy":[]}
	},
	{
		"labels":{"alertname":"HighCPU","instance":"srv2"},
		"annotations":{"summary":"old summary 2"},
		"generatorURL":"http://gen/2",
		"startsAt":"2026-08-17T10:05:00Z",
		"updatedAt":"2026-08-17T10:06:00Z",
		"endsAt":"2026-08-17T10:35:00Z",
		"fingerprint":"fp2",
		"receivers":[{"name":"email"}],
		"status":{"state":"active","silencedBy":[],"inhibitedBy":[],"mutedBy":[]}
	}
]`

func newWriteToolConfig(t *testing.T, cfg AlertmanagerConfig, handler http.HandlerFunc) *AlertWriteTool {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg.Address = server.URL
	tool, err := NewAlertWriteTool(context.Background(), Configs{
		"t": {Alertmanager: &cfg},
	})
	require.NoError(t, err)
	return tool
}

func newWriteTool(t *testing.T, handler http.HandlerFunc) *AlertWriteTool {
	t.Helper()
	return newWriteToolConfig(t, AlertmanagerConfig{}, handler)
}

func validCreateParams(overrides func(*AlertWriteParams)) *AlertWriteParams {
	p := &AlertWriteParams{
		Instance:    "t",
		Operation:   "create",
		Labels:      map[string]string{"alertname": "HighCPU", "instance": "srv1"},
		Annotations: map[string]string{"summary": "high cpu"},
		Confirmed:   true,
	}
	if overrides != nil {
		overrides(p)
	}
	return p
}

func TestAlertWriteCreate(t *testing.T) {
	var gotBody []byte
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	result, err := tool.Invoke(context.Background(), validCreateParams(nil))
	require.NoError(t, err)

	var posted []postableAlert
	require.NoError(t, json.Unmarshal(gotBody, &posted))
	require.Len(t, posted, 1)
	assert.Equal(t, model.LabelValue("HighCPU"), posted[0].Labels["alertname"])
	assert.Equal(t, model.LabelValue("high cpu"), posted[0].Annotations["summary"])
	assert.True(t, posted[0].EndsAt.After(time.Now()))
	assert.True(t, posted[0].EndsAt.After(*posted[0].StartsAt))

	var out AlertWriteOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.Equal(t, "success", out.Status)
	assert.Equal(t, "created", out.Action)
}

func TestAlertWriteUpdate(t *testing.T) {
	var gotMethod string
	var gotBody []byte
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gotMethod = http.MethodGet
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(amExistingAlertJSON))
			return
		}
		gotMethod = http.MethodPost
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	result, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:    "t",
		Operation:   "update",
		Labels:      map[string]string{"alertname": "HighCPU", "instance": "srv1"},
		Annotations: map[string]string{"summary": "new summary"},
		Confirmed:   true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)

	var posted []postableAlert
	require.NoError(t, json.Unmarshal(gotBody, &posted))
	require.Len(t, posted, 1)
	assert.Equal(t, model.LabelValue("new summary"), posted[0].Annotations["summary"])
	assert.Equal(t, "2026-08-17T10:00:00Z", posted[0].StartsAt.Format(time.RFC3339))
	assert.Equal(t, "2026-08-17T10:30:00Z", posted[0].EndsAt.Format(time.RFC3339))
	assert.Equal(t, "http://gen/1", posted[0].GeneratorURL)

	var out AlertWriteOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.Equal(t, "success", out.Status)
	assert.Equal(t, "updated", out.Action)
	assert.Equal(t, "fp1", out.Fingerprint)
}

func TestAlertWriteUpdateFilterParams(t *testing.T) {
	var gotQuery string
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(amExistingAlertJSON))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	_, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "update",
		Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
		Confirmed: true,
	})
	require.NoError(t, err)

	// Each label must be sent as a separate `filter` param (one matcher each),
	// not a single comma-joined value.
	assert.Contains(t, gotQuery, "active=true")
	assert.Contains(t, gotQuery, "silenced=true")
	assert.Contains(t, gotQuery, "inhibited=true")
	assert.Contains(t, gotQuery, "filter=alertname%3D%22HighCPU%22")
	assert.Contains(t, gotQuery, "filter=instance%3D%22srv1%22")
}

func TestAlertWriteUpdateKeepsExistingAnnotationsWhenOmitted(t *testing.T) {
	var gotBody []byte
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(amExistingAlertJSON))
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	_, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "update",
		Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
		Confirmed: true,
	})
	require.NoError(t, err)

	var posted []postableAlert
	require.NoError(t, json.Unmarshal(gotBody, &posted))
	require.Len(t, posted, 1)
	assert.Equal(t, model.LabelValue("old summary"), posted[0].Annotations["summary"])
}

func TestAlertWriteDelete(t *testing.T) {
	var methods []string
	var gotBody []byte
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	result, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "delete",
		Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
		Confirmed: true,
	})
	require.NoError(t, err)

	// Delete is idempotent: no pre-existence GET, only a single POST.
	assert.Equal(t, []string{http.MethodPost}, methods)

	var posted []postableAlert
	require.NoError(t, json.Unmarshal(gotBody, &posted))
	require.Len(t, posted, 1)
	assert.Equal(t, model.LabelValue("HighCPU"), posted[0].Labels["alertname"])
	assert.True(t, !posted[0].EndsAt.After(time.Now()), "endsAt must be <= now (resolve)")
	assert.True(t, posted[0].StartsAt.Before(*posted[0].EndsAt))
	assert.Nil(t, posted[0].Annotations)

	var out AlertWriteOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.Equal(t, "success", out.Status)
	assert.Equal(t, "deleted", out.Action)
	assert.NotEmpty(t, out.EndsAt)
}

func TestAlertWriteDryRun(t *testing.T) {
	t.Run("create previews without POST", func(t *testing.T) {
		var posted bool
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			posted = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		})

		result, err := tool.Invoke(context.Background(), validCreateParams(func(p *AlertWriteParams) {
			p.DryRun = true
			p.Confirmed = false
		}))
		require.NoError(t, err)
		assert.False(t, posted)

		var preview map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &preview))
		assert.Equal(t, true, preview["dryRun"])
		assert.Equal(t, "create", preview["operation"])
		assert.NotNil(t, preview["alert"])
	})

	t.Run("update previews existing and merged without POST", func(t *testing.T) {
		var posted bool
		var gets int
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(amExistingAlertJSON))
				return
			}
			posted = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		})

		result, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:    "t",
			Operation:   "update",
			Labels:      map[string]string{"alertname": "HighCPU", "instance": "srv1"},
			Annotations: map[string]string{"summary": "new"},
			DryRun:      true,
		})
		require.NoError(t, err)
		assert.False(t, posted)
		assert.Equal(t, 1, gets)

		var preview map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &preview))
		assert.Equal(t, true, preview["dryRun"])
		assert.Equal(t, "update", preview["operation"])
		assert.NotNil(t, preview["existing"])
		assert.NotNil(t, preview["merged"])
	})

	t.Run("delete previews resolve without POST", func(t *testing.T) {
		var posted bool
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			posted = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		})

		result, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "delete",
			Labels:    map[string]string{"alertname": "HighCPU"},
			DryRun:    true,
		})
		require.NoError(t, err)
		assert.False(t, posted)

		var preview map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &preview))
		assert.Equal(t, true, preview["dryRun"])
		assert.Equal(t, "delete", preview["operation"])
		assert.NotNil(t, preview["resolve"])
	})
}

func TestAlertWriteRequiresConfirmation(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("no request should be made without confirmation")
			})
			_, err := tool.Invoke(context.Background(), &AlertWriteParams{
				Instance:  "t",
				Operation: op,
				Labels:    map[string]string{"alertname": "HighCPU"},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "confirmed must be true to execute")
		})
	}
}

func TestAlertWriteRequiresAlertname(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("no request should be made when alertname is missing")
			})
			_, err := tool.Invoke(context.Background(), &AlertWriteParams{
				Instance:  "t",
				Operation: op,
				Labels:    map[string]string{"instance": "srv1"},
				Confirmed: true,
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "labels must include 'alertname'")
		})
	}
}

func TestAlertWriteInvalidOperation(t *testing.T) {
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for an invalid operation")
	})
	_, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "foo",
		Labels:    map[string]string{"alertname": "HighCPU"},
		Confirmed: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid parameters")
}

func TestAlertWriteInvalidTimes(t *testing.T) {
	t.Run("create invalid startsAt", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("no request should be made for invalid time")
		})
		_, err := tool.Invoke(context.Background(), validCreateParams(func(p *AlertWriteParams) {
			p.StartsAt = "not-a-time"
		}))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid startsAt/endsAt, expected RFC3339")
	})

	t.Run("update invalid endsAt", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(amExistingAlertJSON))
				return
			}
			t.Error("no POST should be made for invalid time")
		})
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "update",
			Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
			EndsAt:    "not-a-time",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid startsAt/endsAt, expected RFC3339")
	})

	t.Run("create endsAt in the past", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("no request should be made for a past endsAt")
		})
		_, err := tool.Invoke(context.Background(), validCreateParams(func(p *AlertWriteParams) {
			p.EndsAt = "2020-01-01T00:00:00Z"
		}))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endsAt must be in the future for a firing alert")
	})

	t.Run("create endsAt before startsAt", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("no request should be made for endsAt <= startsAt")
		})
		_, err := tool.Invoke(context.Background(), validCreateParams(func(p *AlertWriteParams) {
			p.StartsAt = "2099-01-01T12:00:00Z"
			p.EndsAt = "2099-01-01T11:00:00Z"
		}))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endsAt must be after startsAt")
	})

	t.Run("update endsAt before startsAt", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(amExistingAlertJSON))
				return
			}
			t.Error("no POST should be made for endsAt <= startsAt")
		})
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "update",
			Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
			StartsAt:  "2099-01-01T12:00:00Z",
			EndsAt:    "2099-01-01T11:00:00Z",
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endsAt must be after startsAt")
	})
}

func TestAlertWriteUpdateNoMatch(t *testing.T) {
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	_, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "update",
		Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
		Confirmed: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no existing alert matches labels")
}

func TestAlertWriteUpdateMultipleMatchesUsesFirst(t *testing.T) {
	var gotBody []byte
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(amTwoExistingAlertsJSON))
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	result, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:    "t",
		Operation:   "update",
		Labels:      map[string]string{"alertname": "HighCPU"},
		Annotations: map[string]string{"summary": "merged"},
		Confirmed:   true,
	})
	require.NoError(t, err)

	var posted []postableAlert
	require.NoError(t, json.Unmarshal(gotBody, &posted))
	require.Len(t, posted, 1)
	assert.Equal(t, model.LabelValue("srv1"), posted[0].Labels["instance"])

	var out AlertWriteOutput
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.Equal(t, "fp1", out.Fingerprint)
}

func TestAlertWriteGeneratorURLScheme(t *testing.T) {
	for _, op := range []string{"create", "update"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("no request should be made for a javascript: generatorURL")
			})
			_, err := tool.Invoke(context.Background(), &AlertWriteParams{
				Instance:     "t",
				Operation:    op,
				Labels:       map[string]string{"alertname": "HighCPU"},
				GeneratorURL: "javascript:alert(1)",
				Confirmed:    true,
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "generatorURL must be an http/https URL")
		})
	}
}

func TestAlertWriteMissingInstance(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		t.Run(op, func(t *testing.T) {
			tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("no request should be made for a missing instance")
			})
			_, err := tool.Invoke(context.Background(), &AlertWriteParams{
				Instance:  "nope",
				Operation: op,
				Labels:    map[string]string{"alertname": "HighCPU"},
				Confirmed: true,
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "nope")
		})
	}
}

func TestAlertWriteAPIError(t *testing.T) {
	t.Run("create POST 400", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad"}`))
		})
		_, err := tool.Invoke(context.Background(), validCreateParams(nil))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to post alerts to Alertmanager")
	})

	t.Run("update POST 500", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(amExistingAlertJSON))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		})
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "update",
			Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to post alerts to Alertmanager")
	})

	t.Run("delete POST 500", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		})
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "delete",
			Labels:    map[string]string{"alertname": "HighCPU"},
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to post alerts to Alertmanager")
	})

	t.Run("update GET error", func(t *testing.T) {
		tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		})
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "update",
			Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list Alertmanager alerts")
	})
}

func TestAlertWriteAuthHeaders(t *testing.T) {
	t.Run("bearer on POST", func(t *testing.T) {
		var gotAuth string
		tool := newWriteToolConfig(t, AlertmanagerConfig{BearerToken: "token-123"}, func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		})

		_, err := tool.Invoke(context.Background(), validCreateParams(nil))
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-123", gotAuth)
	})

	t.Run("bearer on GET during update", func(t *testing.T) {
		var gotAuth string
		tool := newWriteToolConfig(t, AlertmanagerConfig{BearerToken: "token-123"}, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(amExistingAlertJSON))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		})

		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "update",
			Labels:    map[string]string{"alertname": "HighCPU", "instance": "srv1"},
			Confirmed: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-123", gotAuth)
	})
}

func TestAlertWriteSecretRedaction(t *testing.T) {
	const (
		bearer = "LEAK-BEARER-TOKEN"
		passwd = "LEAK-PASSWORD"
	)

	t.Run("outputs never leak secrets", func(t *testing.T) {
		tool := newWriteToolConfig(t, AlertmanagerConfig{BearerToken: bearer, Password: passwd}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		})

		result, err := tool.Invoke(context.Background(), validCreateParams(nil))
		require.NoError(t, err)
		assert.NotContains(t, result, bearer)
		assert.NotContains(t, result, passwd)

		// Dry-run preview must not leak secrets either.
		preview, err := tool.Invoke(context.Background(), validCreateParams(func(p *AlertWriteParams) {
			p.DryRun = true
			p.Confirmed = false
		}))
		require.NoError(t, err)
		assert.NotContains(t, preview, bearer)
		assert.NotContains(t, preview, passwd)
	})

	t.Run("errors never leak secrets", func(t *testing.T) {
		tool := newWriteToolConfig(t, AlertmanagerConfig{BearerToken: bearer, Password: passwd}, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		})

		_, err := tool.Invoke(context.Background(), validCreateParams(nil))
		assert.Error(t, err)
		assert.NotContains(t, err.Error(), bearer)
		assert.NotContains(t, err.Error(), passwd)
	})
}

func TestBuildMatcherFilterEscapesSpecialChars(t *testing.T) {
	filter := buildMatcherFilter(map[string]string{
		"alertname": `High"CPU`,
		"path":      `C:\tmp`,
	})
	// Deterministic (sorted) ordering, one matcher per label.
	assert.Equal(t, []string{`alertname="High\"CPU"`, `path="C:\\tmp"`}, filter)
}

func TestBuildMatcherFilterOneMatcherPerLabel(t *testing.T) {
	filter := buildMatcherFilter(map[string]string{
		"alertname": "HighCPU",
		"instance":  "srv1",
	})
	assert.Len(t, filter, 2)
	assert.Equal(t, `alertname="HighCPU"`, filter[0])
	assert.Equal(t, `instance="srv1"`, filter[1])
	for _, m := range filter {
		assert.NotContains(t, m, ",")
	}
}

// TestValidateMatcherLabelKeys verifies that label keys used to build
// Alertmanager matcher filters are validated against the Prometheus legacy
// label-name regex. This prevents matcher injection via keys containing
// metacharacters (=, !, ~, ", \, whitespace) that could break the matcher
// syntax or alter its semantics (CWE-74).
func TestValidateMatcherLabelKeys(t *testing.T) {
	validKeys := []string{"alertname", "instance", "_private", "foo_bar", "A1", "a"}
	for _, k := range validKeys {
		assert.NoError(t, validateMatcherLabelKeys(map[string]string{k: "v"}),
			"key %q should be valid", k)
	}

	invalidKeys := []string{
		"", "has space", `has"quote`, `has\back`, "has=eq",
		"has!bang", "has~tilde", "has,comma", "has-dash",
		"1starts_with_digit", "has/slash",
	}
	for _, k := range invalidKeys {
		err := validateMatcherLabelKeys(map[string]string{k: "v"})
		assert.Error(t, err, "key %q should be rejected", k)
		assert.Contains(t, err.Error(), "invalid label name")
	}
}

// TestAlertWriteUpdateRejectsInvalidLabelKeys verifies that the
// update operation rejects label keys that could break the Alertmanager
// matcher filter, before any request is made.
func TestAlertWriteUpdateRejectsInvalidLabelKeys(t *testing.T) {
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for invalid label keys")
	})
	_, err := tool.Invoke(context.Background(), &AlertWriteParams{
		Instance:  "t",
		Operation: "update",
		Labels: map[string]string{
			"alertname": "HighCPU",
			`bad"key`:   "value",
		},
		Confirmed: true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid label name")
}

// TestAlertWriteInputSizeLimits verifies that oversized label/annotation maps
// are rejected by validate.Struct before any HTTP request is made (CWE-400).
func TestAlertWriteInputSizeLimits(t *testing.T) {
	tool := newWriteTool(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for oversized inputs")
	})

	t.Run("too many labels rejected", func(t *testing.T) {
		labels := make(map[string]string, 65)
		for i := 0; i < 65; i++ {
			labels[fmt.Sprintf("l%d", i)] = "v"
		}
		labels["alertname"] = "HighCPU"
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:  "t",
			Operation: "create",
			Labels:    labels,
			Confirmed: true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})

	t.Run("too many annotations rejected", func(t *testing.T) {
		annotations := make(map[string]string, 65)
		for i := 0; i < 65; i++ {
			annotations[fmt.Sprintf("a%d", i)] = "v"
		}
		_, err := tool.Invoke(context.Background(), &AlertWriteParams{
			Instance:     "t",
			Operation:    "create",
			Labels:       map[string]string{"alertname": "HighCPU"},
			Annotations:  annotations,
			Confirmed:    true,
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameters")
	})
}
