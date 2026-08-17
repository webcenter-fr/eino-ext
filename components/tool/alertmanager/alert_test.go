package alertmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const amTwoAlertsJSON = `[
	{
		"labels":{"alertname":"HighCPU","instance":"srv1"},
		"annotations":{"summary":"high cpu on srv1"},
		"generatorURL":"http://gen/1",
		"startsAt":"2026-08-17T10:00:00Z",
		"updatedAt":"2026-08-17T10:01:00Z",
		"endsAt":"2026-08-17T10:30:00Z",
		"fingerprint":"fp1",
		"receivers":[{"name":"slack"}],
		"status":{"state":"active","silencedBy":[],"inhibitedBy":[],"mutedBy":[]}
	},
	{
		"labels":{"alertname":"HighMemory","instance":"srv2"},
		"annotations":{"summary":"high mem on srv2"},
		"generatorURL":"http://gen/2",
		"startsAt":"2026-08-17T11:00:00Z",
		"updatedAt":"2026-08-17T11:01:00Z",
		"endsAt":"2026-08-17T11:30:00Z",
		"fingerprint":"fp2",
		"receivers":[{"name":"email"}],
		"status":{"state":"suppressed","silencedBy":["sil1"],"inhibitedBy":[],"mutedBy":[]}
	}
]`

func newAlertTool(t *testing.T, handler http.HandlerFunc) *AlertTool {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tool, err := NewAlertTool(context.Background(), Configs{
		"t": {Address: server.URL},
	})
	require.NoError(t, err)
	return tool
}

func TestAlertHappyPath(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t"})
	require.NoError(t, err)

	var outputs []AlertOutput
	require.NoError(t, json.Unmarshal([]byte(result), &outputs))
	assert.Len(t, outputs, 2)

	assert.Equal(t, "fp1", outputs[0].Fingerprint)
	assert.Equal(t, "active", outputs[0].State)
	assert.Equal(t, "2026-08-17T10:00:00Z", outputs[0].StartsAt)
	assert.Equal(t, "2026-08-17T10:30:00Z", outputs[0].EndsAt)
	assert.Equal(t, []string{"slack"}, outputs[0].Receivers)

	assert.Equal(t, "fp2", outputs[1].Fingerprint)
	assert.Equal(t, "suppressed", outputs[1].State)
	assert.Equal(t, []string{"sil1"}, outputs[1].SilencedBy)
}

func TestAlertFingerprint(t *testing.T) {
	t.Run("matches single alert", func(t *testing.T) {
		tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(amTwoAlertsJSON))
		})

		result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t", Fingerprint: "fp2"})
		require.NoError(t, err)

		var outputs []AlertOutput
		require.NoError(t, json.Unmarshal([]byte(result), &outputs))
		require.Len(t, outputs, 1)
		assert.Equal(t, "fp2", outputs[0].Fingerprint)
	})

	t.Run("unknown fingerprint returns empty list", func(t *testing.T) {
		tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(amTwoAlertsJSON))
		})

		result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t", Fingerprint: "nope"})
		require.NoError(t, err)
		assert.Equal(t, "[]", result)
	})
}

func TestAlertFingerprintPrecedence(t *testing.T) {
	var gotQuery url.Values
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	result, err := tool.Invoke(context.Background(), &AlertParams{
		Instance:    "t",
		Fingerprint: "fp1",
		AlertFilter: `alertname="HighMemory"`,
		State:       "suppressed",
	})
	require.NoError(t, err)

	// fingerprint wins: alertFilter and state are ignored, all states fetched.
	var outputs []AlertOutput
	require.NoError(t, json.Unmarshal([]byte(result), &outputs))
	require.Len(t, outputs, 1)
	assert.Equal(t, "fp1", outputs[0].Fingerprint)

	assert.Equal(t, "true", gotQuery.Get("active"))
	assert.Equal(t, "true", gotQuery.Get("silenced"))
	assert.Equal(t, "true", gotQuery.Get("inhibited"))
	_, hasFilter := gotQuery["filter"]
	assert.False(t, hasFilter)
}

func TestAlertDefaultQueryParams(t *testing.T) {
	var gotQuery url.Values
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	_, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t"})
	require.NoError(t, err)

	// Default listing must be active + unprocessed only: silenced and inhibited
	// are explicitly excluded (Alertmanager's server default for both is true,
	// so relying on omission would leak suppressed alerts).
	assert.Equal(t, "true", gotQuery.Get("active"))
	assert.Equal(t, "false", gotQuery.Get("silenced"))
	assert.Equal(t, "false", gotQuery.Get("inhibited"))
}

func TestAlertSuppressedQueryParams(t *testing.T) {
	var gotQuery url.Values
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	_, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t", State: "suppressed"})
	require.NoError(t, err)

	// Suppressed = silenced OR inhibited, so both categories must be requested.
	assert.Equal(t, "true", gotQuery.Get("active"))
	assert.Equal(t, "true", gotQuery.Get("silenced"))
	assert.Equal(t, "true", gotQuery.Get("inhibited"))
}

func TestAlertStateFilter(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t", State: "active"})
	require.NoError(t, err)

	var outputs []AlertOutput
	require.NoError(t, json.Unmarshal([]byte(result), &outputs))
	require.Len(t, outputs, 1)
	assert.Equal(t, "active", outputs[0].State)
}

func TestAlertFilterPassedToAPI(t *testing.T) {
	var gotQuery url.Values
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	_, err := tool.Invoke(context.Background(), &AlertParams{
		Instance:    "t",
		AlertFilter: `alertname="HighCPU",severity="critical"`,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{`alertname="HighCPU"`, `severity="critical"`}, gotQuery["filter"])
}

func TestAlertRegexFilter(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t", Filter: "HighMemory"})
	require.NoError(t, err)

	var outputs []AlertOutput
	require.NoError(t, json.Unmarshal([]byte(result), &outputs))
	require.Len(t, outputs, 1)
	assert.Equal(t, "fp2", outputs[0].Fingerprint)
}

func TestAlertPagination(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	// First page: 1 result + next-page token.
	result, err := tool.Invoke(context.Background(), &AlertParams{
		Instance: "t",
		Paginate: &AlertPaginate{PageSize: 1},
	})
	require.NoError(t, err)

	var page []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result), &page))
	require.Len(t, page, 2)

	var token alertPaginateToken
	require.NoError(t, json.Unmarshal(page[1], &token))
	assert.Equal(t, 1, token.PaginateToken)

	// Second page: remaining result, no token.
	result, err = tool.Invoke(context.Background(), &AlertParams{
		Instance: "t",
		Paginate: &AlertPaginate{PageSize: 1, PaginateToken: string(page[1])},
	})
	require.NoError(t, err)

	var page2 []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result), &page2))
	require.Len(t, page2, 1)

	var output AlertOutput
	require.NoError(t, json.Unmarshal(page2[0], &output))
	assert.Equal(t, "fp2", output.Fingerprint)
}

func TestPaginateWindowClampsStaleToken(t *testing.T) {
	t.Run("token beyond total is clamped", func(t *testing.T) {
		start, end, err := paginateWindow(&AlertPaginate{PageSize: 1, PaginateToken: `{"paginateToken":999}`}, 2)
		require.NoError(t, err)
		assert.Equal(t, 2, start)
		assert.Equal(t, 2, end)
	})

	t.Run("negative token is clamped", func(t *testing.T) {
		start, end, err := paginateWindow(&AlertPaginate{PageSize: 1, PaginateToken: `{"paginateToken":-1}`}, 2)
		require.NoError(t, err)
		assert.Equal(t, 0, start)
		assert.Equal(t, 1, end)
	})
}

func TestAlertEmptyResult(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t"})
	require.NoError(t, err)
	assert.Equal(t, "[]", result)
}

func TestAlertMissingInstance(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	_, err := tool.Invoke(context.Background(), &AlertParams{Instance: "nope"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

func TestAlertInvalidState(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(amTwoAlertsJSON))
	})

	_, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t", State: "broken"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid parameters")
}

func TestAlertAPIError(t *testing.T) {
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`"boom"`))
	})

	_, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list Alertmanager alerts")
	assert.Contains(t, err.Error(), "getAlertsInternalServerError")
}

func TestAlertNilStatus(t *testing.T) {
	// An alert whose JSON omits the (optional-in-practice) "status" field must
	// not panic the output loop. The official model's GettableAlert.Status is a
	// *AlertStatus pointer, so a nil Status must be tolerated.
	tool := newAlertTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"labels":{"alertname":"NoStatus"},
			"annotations":{},
			"generatorURL":"http://gen/1",
			"startsAt":"2026-08-17T10:00:00Z",
			"updatedAt":"2026-08-17T10:01:00Z",
			"endsAt":"2026-08-17T10:30:00Z",
			"fingerprint":"fp-nostatus",
			"receivers":[{"name":"slack"}]
		}]`))
	})

	result, err := tool.Invoke(context.Background(), &AlertParams{Instance: "t"})
	require.NoError(t, err)

	var outputs []AlertOutput
	require.NoError(t, json.Unmarshal([]byte(result), &outputs))
	require.Len(t, outputs, 1)
	assert.Equal(t, "", outputs[0].State)
	assert.Empty(t, outputs[0].SilencedBy)
	assert.Equal(t, "fp-nostatus", outputs[0].Fingerprint)
}

func TestAlertConstructor(t *testing.T) {
	tool, err := NewAlertTool(context.Background(), Configs{})
	require.NoError(t, err)
	require.NotNil(t, tool)

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "alertmanager_alert", info.Name)
}
