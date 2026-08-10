package grafana

import (
	_ "embed"
	"regexp"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

//go:embed prompts/dashboard_search_output_guidance.md
var dashboardSearchOutputGuidance string

//go:embed prompts/dashboard_describe_output_guidance.md
var dashboardDescribeOutputGuidance string

func instanceNotFoundError(instance string, known []string) error {
	return toolutil.NotFoundError("Grafana instance", instance, known)
}

// filterMapMarshal maps each source item to an output value, marshals it,
// keeps only items whose JSON matches re, and returns the JSON array string.
func filterMapMarshal[T, O any](items []T, re *regexp.Regexp, toOutput func(T) O) (string, error) {
	outputs := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		outputJSON := json.RawMessage(marshal.MustMarshal(toOutput(item)))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}
	return marshal.Outputs(outputs)
}

// validateParams validates a struct using the shared validator.
func validateParams(v any) error {
	return validate.Struct(v)
}

// applyExcludes clears each requested field using the provided setter map. It
// returns an error for a field name not present in the map.
func applyExcludes(excludeFields []string, setters map[string]func()) error {
	for _, field := range excludeFields {
		setter, ok := setters[field]
		if !ok {
			return errors.Errorf("invalid exclude field: %s", field)
		}
		setter()
	}
	return nil
}
