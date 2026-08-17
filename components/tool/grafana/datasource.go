package grafana

import (
	"context"
	"fmt"
	"net/http"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
)

const dataSourceDescription = `
** General Purpose **
It reads Grafana data sources. Set 'uid' to describe (return) a single data
source by UID; leave 'uid' empty to list all data sources.

Data sources are READ-ONLY: there is no write tool for them.

** Output **
- describe mode (uid set): a single JSON object with the data source's full
  configuration. Sensitive fields (passwords, tokens, secrets) are excluded or
  redacted.
- list mode (uid empty): a JSON array of objects, each with uid, name, type,
  typeName, url, access, isDefault, readOnly, version, and redacted jsonData.
`

// DataSourceParams defines the parameters for reading Grafana data sources.
// When UID is set the tool describes a single data source; otherwise it lists.
type DataSourceParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	UID      string `json:"uid,omitempty" jsonschema:"(optional) If set, return the full data source with this UID (describe mode, single object). If empty, list all data sources (array)."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional, list mode) Go RE2 regex on each data source list output JSON."`
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

// DataSourceDescribeOutput is the structured output for a data source describe.
// Sensitive top-level fields (password, basicAuthPassword, secureJsonFields,
// secureJsonData) are excluded; jsonData is recursively redacted.
type DataSourceDescribeOutput struct {
	ID              int64          `json:"id"`
	UID             string         `json:"uid"`
	OrgID           int64          `json:"orgId"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	TypeName        string         `json:"typeName,omitempty"`
	TypeLogoURL     string         `json:"typeLogoUrl,omitempty"`
	Access          string         `json:"access"`
	URL             string         `json:"url"`
	User            string         `json:"user"`
	Database        string         `json:"database"`
	BasicAuth       bool           `json:"basicAuth"`
	BasicAuthUser   string         `json:"basicAuthUser,omitempty"`
	WithCredentials bool           `json:"withCredentials,omitempty"`
	IsDefault       bool           `json:"isDefault"`
	JSONData        map[string]any `json:"jsonData,omitempty"`
	ReadOnly        bool           `json:"readOnly"`
	Version         int            `json:"version"`
}

// DataSourceTool is an eino tool for reading Grafana data sources (list/describe).
type DataSourceTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke reads data sources: describe a single data source when UID is set,
// otherwise list.
func (t *DataSourceTool) Invoke(ctx context.Context, params *DataSourceParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	if params.UID != "" {
		// describe mode
		body, err := c.GetDataSource(ctx, params.UID)
		if err != nil {
			if isHTTPStatus(err, http.StatusNotFound) {
				return "", errors.Wrapf(err, "data source with UID %q not found", params.UID)
			}
			return "", errors.Wrap(err, "failed to get data source")
		}

		var ds dataSource
		if err := json.Unmarshal(body, &ds); err != nil {
			return "", errors.Wrap(err, "failed to unmarshal data source")
		}

		output := ds.toDescribeOutput()

		return marshalJSON(output, "failed to marshal output")
	}

	// list mode
	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
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

// NewDataSourceTool creates a new DataSourceTool.
func NewDataSourceTool(ctx context.Context, configs Configs) (*DataSourceTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	dataSourceTool := &DataSourceTool{baseTool: base}
	t, err := utils.InferTool("grafana_datasource", fmt.Sprintf("%s\n%s", dataSourceDescription, dataSourceListOutputGuidance), dataSourceTool.Invoke)
	if err != nil {
		return nil, err
	}
	dataSourceTool.InvokableTool = t

	return dataSourceTool, nil
}
