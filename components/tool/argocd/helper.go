package argocd

import (
	_ "embed"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

// listOutputGuidance is a shared guidance block appended to the description of all
// list tools. It instructs the model to narrow queries to avoid large responses.
//
//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

// describeOutputGuidance is a shared guidance block appended to the description of all
// describe tools. It instructs the model to narrow the output to avoid large responses.
//
//go:embed prompts/describe_output_guidance.md
var describeOutputGuidance string

// instanceNotFoundError returns an error indicating the requested instance is unknown.
func instanceNotFoundError(instance string, known []string) error {
	return toolutil.NotFoundError("ArgoCD instance", instance, known)
}

// filterMapMarshal maps each source item to an output value, marshals it, keeps
// only items whose JSON matches re, and returns the JSON array of survivors. It
// captures the per-item filter/marshal loop shared by all list tools.
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

// applyExcludes clears each requested field using the provided setter map. It
// returns an error for a field name not present in the map. Each setter nils
// out its corresponding output field.
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
