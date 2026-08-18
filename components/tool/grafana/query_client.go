package grafana

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/strutil"
)

// maxLogLineLen caps the length of a single log line returned to the LLM.
// Loki log lines may contain sensitive data; truncation limits how much is
// surfaced in tool output while still conveying a representative sample.
const maxLogLineLen = 256

// proxyQueryResponse is the shared Prometheus/Loki query envelope.
// Both return {status, data:{resultType, result}}. For Loki streams,
// result entries use "stream" instead of "metric".
type proxyQueryResponse struct {
	Status    string         `json:"status"` // "success" | "error"
	Data      proxyQueryData `json:"data"`
	ErrorType string         `json:"errorType,omitempty"`
	Error     string         `json:"error,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

type proxyQueryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

// Wire shapes for parsing result by resultType.
type wireVectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"` // [ts, "val"]
}
type wireMatrixSample struct {
	Metric map[string]string   `json:"metric"`
	Values [][]json.RawMessage `json:"values"` // [[ts, "val"], ...]
}
type wireStream struct {
	Stream map[string]string   `json:"stream"`
	Values [][]json.RawMessage `json:"values"` // [[ts_nanos, "line"], ...]
}

// dataSourceProxyGet performs a GET to /api/datasources/proxy/uid/{uid}{path}
// with the given query params and returns the raw body. The uid is path-escaped
// to prevent path traversal / endpoint injection (mirrors GetDataSource).
func (c *grafanaClient) dataSourceProxyGet(ctx context.Context, uid, path string, q url.Values) ([]byte, error) {
	fullPath := "/api/datasources/proxy/uid/" + url.PathEscape(uid) + path
	if encoded := q.Encode(); encoded != "" {
		fullPath += "?" + encoded
	}

	body, _, err := c.doRequest(ctx, http.MethodGet, fullPath, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to proxy query datasource %q", uid)
	}
	return body, nil
}

// proxyQuerySpec describes the endpoint and time encoding for a datasource
// proxy query. Prometheus and Loki share the response envelope but differ in
// path prefix and range time unit (seconds vs nanoseconds).
type proxyQuerySpec struct {
	pathPrefix string // "/api/v1" (Prometheus) or "/loki/api/v1" (Loki)
	nanoTimes  bool   // encode range start/end as nanoseconds (Loki)
	limit      int    // range only: max log lines (0 = omit)
	direction  string // range only: "forward" | "backward" ("" = omit)
}

// queryProxy executes an instant or range query against a datasource proxy
// endpoint and parses the response envelope. queryType is "instant" or "range".
// For instant, only evalTime is used; for range, start, end, and stepSeconds
// are used.
func (c *grafanaClient) queryProxy(ctx context.Context, uid, expr, queryType string, evalTime, start, end time.Time, stepSeconds int, spec proxyQuerySpec) (*proxyQueryResponse, error) {
	q := url.Values{}
	q.Set("query", expr)

	path := spec.pathPrefix + "/query"
	if queryType == "range" {
		path = spec.pathPrefix + "/query_range"
		startStr, endStr := strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10)
		if spec.nanoTimes {
			startStr, endStr = strconv.FormatInt(start.UnixNano(), 10), strconv.FormatInt(end.UnixNano(), 10)
		}
		q.Set("start", startStr)
		q.Set("end", endStr)
		q.Set("step", strconv.Itoa(stepSeconds))
		if spec.limit > 0 {
			q.Set("limit", strconv.Itoa(spec.limit))
		}
		if spec.direction != "" {
			q.Set("direction", spec.direction)
		}
	} else {
		// Prometheus instant "time" is Unix seconds; Loki instant "time" is a
		// nanosecond Unix epoch (Loki interprets ALL epoch values as nanoseconds).
		ts := strconv.FormatInt(evalTime.Unix(), 10)
		if spec.nanoTimes {
			ts = strconv.FormatInt(evalTime.UnixNano(), 10)
		}
		q.Set("time", ts)
	}

	body, err := c.dataSourceProxyGet(ctx, uid, path, q)
	if err != nil {
		return nil, err
	}
	return parseProxyResponse(body)
}

// QueryPrometheus executes a PromQL query via the Prometheus proxy.
func (c *grafanaClient) QueryPrometheus(ctx context.Context, uid, expr, queryType string, evalTime, start, end time.Time, stepSeconds int) (*proxyQueryResponse, error) {
	return c.queryProxy(ctx, uid, expr, queryType, evalTime, start, end, stepSeconds, proxyQuerySpec{pathPrefix: "/api/v1"})
}

// QueryLoki executes a LogQL query via the Loki proxy. Same semantics as
// QueryPrometheus but uses /loki/api/v1/... and nanosecond start/end for range.
// limit (log lines) and direction ("forward"/"backward") apply to range log queries.
func (c *grafanaClient) QueryLoki(ctx context.Context, uid, expr, queryType string, evalTime, start, end time.Time, stepSeconds int, limit int, direction string) (*proxyQueryResponse, error) {
	return c.queryProxy(ctx, uid, expr, queryType, evalTime, start, end, stepSeconds, proxyQuerySpec{
		pathPrefix: "/loki/api/v1",
		nanoTimes:  true,
		limit:      limit,
		direction:  direction,
	})
}

// parseProxyResponse unmarshals a proxy query body and converts a Prometheus/
// Loki status:"error" envelope into a Go error wrapping errorType + error.
func parseProxyResponse(body []byte) (*proxyQueryResponse, error) {
	var resp proxyQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal query response")
	}
	if resp.Status == "error" {
		return nil, errors.Errorf("query failed: %s: %s", resp.ErrorType, resp.Error)
	}
	return &resp, nil
}

// parseProxyQueryResult decodes proxyQueryResponse.Data.Result into a slice
// of SeriesSummary based on ResultType. Returns the series and any parse error.
func parseProxyQueryResult(resp *proxyQueryResponse) ([]SeriesSummary, error) {
	if resp == nil {
		return nil, errors.New("nil query response")
	}

	switch resp.Data.ResultType {
	case "vector":
		var samples []wireVectorSample
		if err := json.Unmarshal(resp.Data.Result, &samples); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal vector result")
		}
		out := make([]SeriesSummary, 0, len(samples))
		for _, s := range samples {
			if len(s.Value) < 2 {
				continue
			}
			v, err := parseFloatValue(s.Value[1])
			if err != nil {
				return nil, err
			}
			out = append(out, SeriesSummary{Labels: s.Metric, Value: &v})
		}
		return out, nil

	case "matrix":
		var samples []wireMatrixSample
		if err := json.Unmarshal(resp.Data.Result, &samples); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal matrix result")
		}
		out := make([]SeriesSummary, 0, len(samples))
		for _, s := range samples {
			summary := SeriesSummary{Labels: s.Metric}
			if len(s.Values) > 0 {
				last := s.Values[len(s.Values)-1]
				if len(last) >= 2 {
					v, err := parseFloatValue(last[1])
					if err != nil {
						return nil, err
					}
					summary.Sample = &MetricSample{Timestamp: parseTimestamp(last[0]), Value: v}
				}
			}
			out = append(out, summary)
		}
		return out, nil

	case "streams":
		var streams []wireStream
		if err := json.Unmarshal(resp.Data.Result, &streams); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal streams result")
		}
		out := make([]SeriesSummary, 0, len(streams))
		for _, s := range streams {
			line := ""
			if len(s.Values) > 0 && len(s.Values[0]) >= 2 {
				line = strutil.Truncate(stringValue(s.Values[0][1]), maxLogLineLen, "...(truncated)")
			}
			out = append(out, SeriesSummary{Labels: s.Stream, Line: line})
		}
		return out, nil

	case "scalar":
		var scalar []json.RawMessage
		if err := json.Unmarshal(resp.Data.Result, &scalar); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal scalar result")
		}
		if len(scalar) < 2 {
			return []SeriesSummary{}, nil
		}
		v, err := parseFloatValue(scalar[1])
		if err != nil {
			return nil, err
		}
		return []SeriesSummary{{Value: &v}}, nil

	case "string":
		var s []json.RawMessage
		if err := json.Unmarshal(resp.Data.Result, &s); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal string result")
		}
		if len(s) < 2 {
			return []SeriesSummary{}, nil
		}
		return []SeriesSummary{{Line: strutil.Truncate(stringValue(s[1]), maxLogLineLen, "...(truncated)")}}, nil

	default:
		return nil, errors.Errorf("unsupported resultType %q", resp.Data.ResultType)
	}
}

// parseFloatValue parses a JSON-encoded sample value. Prometheus/Loki return
// values as JSON strings ("1.234"); fall back to a direct float unmarshal for
// bare numeric values.
func parseFloatValue(raw json.RawMessage) (float64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if f, ferr := strconv.ParseFloat(s, 64); ferr == nil {
			return f, nil
		}
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, errors.Wrapf(err, "failed to parse numeric value %s", string(raw))
	}
	return f, nil
}

// stringValue extracts a JSON string from raw, falling back to the raw bytes
// (which may be an unquoted string) when unmarshal fails.
func stringValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw)
	}
	return s
}

// parseTimestamp renders a JSON-encoded timestamp as a canonical string.
func parseTimestamp(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case string:
		return n
	case json.Number:
		return n.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
