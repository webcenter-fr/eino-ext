package grafana

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const dataSourceListDescription = `
** General Purpose **
It lists all configured data sources on a Grafana instance.
Use this to discover which data sources (Prometheus, Loki, Elasticsearch, etc.)
are available before building dashboards that reference them.

** Output **
It returns a JSON array of objects, where each object represents a data source with:
- uid: the data source UID (use this to reference the data source in dashboards).
- name: the data source name.
- type: the plugin type (e.g. 'prometheus', 'loki', 'elasticsearch').
- typeName: the human-readable type name (when available).
- url: the data source URL.
- access: the access mode ('proxy' or 'direct').
- isDefault: whether this is the default data source.
- readOnly: whether the data source is read-only.
- version: the data source version.
- jsonData: plugin-specific configuration with sensitive fields redacted.
`

// DataSourceListParams defines the parameters for listing Grafana data sources.
type DataSourceListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each data source JSON output. Keep only data sources that match the pattern. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Example: 'prometheus|loki'."`
}

// DataSourceListOutput is the structured output for a single data source in a list.
// Sensitive fields (password, basicAuthPassword, secureJsonFields) are excluded;
// jsonData is recursively redacted.
type DataSourceListOutput struct {
	ID        int64          `json:"id"`
	UID       string         `json:"uid"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	TypeName  string         `json:"typeName,omitempty"`
	URL       string         `json:"url"`
	Access    string         `json:"access"`
	IsDefault bool           `json:"isDefault"`
	ReadOnly  bool           `json:"readOnly"`
	Version   int            `json:"version"`
	JSONData  map[string]any `json:"jsonData,omitempty"`
}

// DataSourceListTool is an eino tool for listing Grafana data sources.
type DataSourceListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke lists all data sources on the given Grafana instance.
func (t *DataSourceListTool) Invoke(ctx context.Context, params *DataSourceListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	body, err := c.ListDataSources(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list data sources")
	}

	var sources []dataSource
	if err := json.Unmarshal(body, &sources); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal data sources")
	}

	return filterMapMarshal(sources, re, dataSource.toListOutput)
}

// NewDataSourceListTool creates a new DataSourceListTool.
func NewDataSourceListTool(ctx context.Context, configs Configs) (*DataSourceListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &DataSourceListTool{baseTool: base}
	t, err := utils.InferTool("grafana_datasource_list", fmt.Sprintf("%s\n%s", dataSourceListDescription, dataSourceListOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
