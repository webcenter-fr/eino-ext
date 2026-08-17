package prometheus

import (
	_ "embed"
	"strconv"
	"time"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// listOutputGuidance is a shared guidance block appended to the description of all
// list tools. It instructs the model to narrow queries to avoid large responses.
//
//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

func marshalOutputs(outputs []json.RawMessage) (string, error) {
	return marshal.Outputs(outputs)
}

// marshalString marshals a single value to a JSON string, mirroring the
// MustMarshal semantics used across the component.
func marshalString(v any) string {
	return string(marshal.MustMarshal(v))
}

func instanceNotFoundError(instance string, known []string) error {
	return toolutil.NotFoundError("Prometheus instance", instance, known)
}

func validateParams(v any) error {
	return validate.Struct(v)
}

// AlertPaginate holds pagination state for alert listing (renamed from
// AlertListPaginate; shared by prometheus_alert).
type AlertPaginate struct {
	PageSize      int    `json:"pageSize,omitempty" validate:"omitempty,min=1,max=500" jsonschema:"(optional) The number of alerts to return per page. Default is 20."`
	PaginateToken string `json:"paginateToken,omitempty" jsonschema:"(optional) The token to retrieve the next page of results. This token is returned when there are more results available than can fit in a single page."`
}

type alertPaginateToken struct {
	PaginateToken int `json:"paginateToken"`
}

// paginateWindow computes the [start, end) slice window for client-side index
// pagination of alert listings. It decodes the opaque paginate token when
// present and clamps the window to the total number of items.
func paginateWindow(paginate *AlertPaginate, total int) (start, end int, err error) {
	if paginate != nil && paginate.PaginateToken != "" {
		var tok alertPaginateToken
		if err := json.Unmarshal([]byte(paginate.PaginateToken), &tok); err != nil {
			return 0, 0, errors.Wrap(err, "invalid paginate token")
		}
		start = tok.PaginateToken
	}
	// Clamp start into [0, total] so a stale or malformed token cannot cause a
	// slice-bounds panic when the underlying result set has shrunk.
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end = total
	if paginate != nil && start+paginate.PageSize < end {
		end = start + paginate.PageSize
	}
	return start, end, nil
}

// nextPageToken returns the opaque pagination token for the next page, or nil
// when there are no more items.
func nextPageToken(end, total int) json.RawMessage {
	if end >= total {
		return nil
	}
	return json.RawMessage(marshal.MustMarshal(alertPaginateToken{PaginateToken: end}))
}

// parsePromQLDuration parses a single PromQL duration value (e.g. "1d", "2w",
// "24h") and returns a Go time.Duration. Supports standard Go suffixes plus
// PromQL-specific d (day) and w (week).
func parsePromQLDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty duration")
	}
	// Standard Go duration suffixes: ns, us, ms, s, m, h.
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	// PromQL-specific: <number><unit> with no chaining.
	unit := s[len(s)-1]
	num, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, errors.Wrapf(err, "invalid PromQL duration: %q", s)
	}
	switch unit {
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(num) * 7 * 24 * time.Hour, nil
	case 'y':
		return time.Duration(num) * 365 * 24 * time.Hour, nil
	default:
		return 0, errors.Errorf("unrecognized unit %q in PromQL duration: %q", string(unit), s)
	}
}

// receiverNames flattens the receiver list of an alert into a plain []string.
func receiverNames(rs []amReceiver) []string {
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		names = append(names, r.Name)
	}
	return names
}
