package convertor

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"

	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/util/json"
	yamlk8s "k8s.io/apimachinery/pkg/util/yaml"
)

const convertorDescription = `
** General Purpose **
It converts data from one format to another. It supports converting between YAML and JSON formats.

** Output **
It returns the converted data in the specified output format.
`

// ConvertorParams defines the parameters for the Convertor function.
type ConvertorParams struct {
	Input      string `json:"input" validate:"required" jsonschema:"(required) The input data to convert."`
	InputType  string `json:"inputType" validate:"required,oneof=yaml json" jsonschema:"(required) The type of the input data. Must be: 'yaml', 'json'."`
	OutputType string `json:"outputType" validate:"required,oneof=yaml json" jsonschema:"(required) The type of the output data. Must be: 'yaml', 'json'."`
}

// ConvertorTool is a tool that converts data from one format to another. It implements the InvokableTool interface.
type ConvertorTool struct {
	tool.InvokableTool
}

const maxInputSize = 1024 * 1024 // 1MB

// Invoke executes the ConvertorTool with the given parameters. It validates the parameters, converts the input data from the specified input format to the output format, and returns the result as a string.
func (t *ConvertorTool) Invoke(ctx context.Context, params *ConvertorParams) (result string, err error) {

	if err := validate.Struct(params); err != nil {
		return "", err
	}

	if len(params.Input) > maxInputSize {
		return "", errors.Errorf("input too large: %d bytes (max %d)", len(params.Input), maxInputSize)
	}

	// Input to Object
	var o map[string]any
	switch params.InputType {
	case "yaml":
		if err = yamlk8s.Unmarshal([]byte(params.Input), &o); err != nil {
			return "", errors.Wrap(err, "failed to unmarshal YAML input")
		}
	case "json":
		err = json.Unmarshal([]byte(params.Input), &o)
		if err != nil {
			return "", errors.Wrap(err, "failed to unmarshal JSON input")
		}
	default:
		return "", errors.Errorf("unsupported input type: %s", params.InputType)
	}

	// Object to Output
	var output string
	switch params.OutputType {
	case "yaml":
		data, err := yaml.Marshal(o)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal output to YAML")
		}
		output = string(data)
	case "json":
		data, err := json.Marshal(o)
		if err != nil {
			return "", errors.Wrap(err, "failed to marshal output to JSON")
		}
		output = string(data)
	default:
		return "", errors.Errorf("unsupported output type: %s", params.OutputType)
	}

	return output, nil

}

// NewConvertorTool creates a new instance of the ConvertorTool.
func NewConvertorTool(ctx context.Context) (tool.InvokableTool, error) {

	convertorTool := &ConvertorTool{}

	// Infer tool
	t, err := utils.InferTool("convertor_object_tool", convertorDescription, convertorTool.Invoke)
	if err != nil {
		return nil, err
	}
	convertorTool.InvokableTool = t

	return convertorTool, nil
}
