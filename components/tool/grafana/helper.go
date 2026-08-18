package grafana

import (
	_ "embed"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

//go:embed prompts/dashboard_search_output_guidance.md
var dashboardSearchOutputGuidance string

//go:embed prompts/datasource_list_output_guidance.md
var dataSourceListOutputGuidance string

func instanceNotFoundError(instance string, known []string) error {
	return toolutil.NotFoundError("Grafana instance", instance, known)
}

// marshalJSON marshals v to a JSON string, wrapping any marshal error with msg.
func marshalJSON(v any, msg string) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", errors.Wrap(err, msg)
	}
	return string(data), nil
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
			return errors.Errorf("parameter 'excludeFieldsOutput' has invalid value %q; allowed values are: %s. Remove or fix it and retry",
				field, strings.Join(toolutil.SortedKeys(setters), ", "))
		}
		setter()
	}
	return nil
}
