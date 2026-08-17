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

// parseRFC3339 parses an RFC3339 timestamp, accepting both the second-level
// form (2024-01-01T00:00:00Z) and fractional seconds (2024-01-01T00:00:00.123Z).
func parseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
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
