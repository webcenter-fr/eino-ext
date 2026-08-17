package alertmanager

import (
	_ "embed"
	"time"

	"emperror.dev/errors"
	"github.com/go-openapi/strfmt"
	"github.com/goccy/go-json"
	"github.com/prometheus/alertmanager/api/v2/models"
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
	return toolutil.NotFoundError("Alertmanager instance", instance, known)
}

func validateParams(v any) error {
	return validate.Struct(v)
}

// ptrString safely dereferences a *string, returning "" for nil.
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ptrDateTime returns the time.Time value of a *strfmt.DateTime, or the zero
// time.Time for nil.
func ptrDateTime(dt *strfmt.DateTime) time.Time {
	if dt == nil {
		return time.Time{}
	}
	return time.Time(*dt)
}

// ptrDateTimeFormat formats a *strfmt.DateTime as RFC3339, returning "" for
// nil. It formats with time.RFC3339 (rather than the strfmt.DateTime default)
// to preserve the previous second-precision output.
func ptrDateTimeFormat(dt *strfmt.DateTime) string {
	if dt == nil {
		return ""
	}
	return ptrDateTime(dt).Format(time.RFC3339)
}

// alertStatusState safely returns the state of an alert's status, or "" when
// the status (or its State pointer) is nil.
func alertStatusState(status *models.AlertStatus) string {
	if status == nil {
		return ""
	}
	return ptrString(status.State)
}

// alertStatusSilencedBy safely returns the silencedBy slice of an alert's
// status, or nil when the status is nil.
func alertStatusSilencedBy(status *models.AlertStatus) []string {
	if status == nil {
		return nil
	}
	return status.SilencedBy
}

// AlertPaginate holds pagination state for alert listing.
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

// receiverNames flattens the receiver list of an alert into a plain []string.
func receiverNames(rs []*models.ReceiverReference) []string {
	names := make([]string, 0, len(rs))
	for _, r := range rs {
		if r != nil && r.Name != nil {
			names = append(names, *r.Name)
		}
	}
	return names
}
