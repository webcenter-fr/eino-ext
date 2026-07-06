package prometheus

import (
	_ "embed"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
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

func marshalOutputs(outputs []json.RawMessage) (string, error) {
	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(data), nil
}

func instanceNotFoundError(instance string, known []string) error {
	return errors.Errorf("Prometheus instance not found: %s. Instance must be one of: %s", instance, strings.Join(known, ", "))
}

func validateParams(v any) error {
	return validate.Struct(v)
}
