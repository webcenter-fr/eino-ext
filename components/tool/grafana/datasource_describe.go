package grafana

import (
	"context"
	"fmt"
	"net/http"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const dataSourceDescribeDescription = `
** General Purpose **
It gets the full details of a specific Grafana data source by its UID.
Use this to inspect a data source's configuration (e.g. default database, region,
time field) before building dashboards that query it.

** Output **
It returns a JSON object with the data source's full configuration. Sensitive
fields (passwords, tokens, secrets) are excluded or redacted.
`

// DataSourceDescribeParams defines the parameters for describing a Grafana data source.
type DataSourceDescribeParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The Grafana instance to connect to."`
	UID      string `json:"uid" validate:"required" jsonschema:"(required) The data source UID."`
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

// DataSourceDescribeTool is an eino tool for describing a Grafana data source.
type DataSourceDescribeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns the full details of a Grafana data source by UID.
func (t *DataSourceDescribeTool) Invoke(ctx context.Context, params *DataSourceDescribeParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	body, err := c.GetDataSource(ctx, params.UID)
	if err != nil {
		// Surface a clear not-found error for 404, propagate everything else.
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

	data, err := json.Marshal(output)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

// NewDataSourceDescribeTool creates a new DataSourceDescribeTool.
func NewDataSourceDescribeTool(ctx context.Context, configs Configs) (*DataSourceDescribeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	describeTool := &DataSourceDescribeTool{baseTool: base}
	t, err := utils.InferTool("grafana_datasource_describe", fmt.Sprintf("%s\n%s", dataSourceDescribeDescription, dataSourceDescribeOutputGuidance), describeTool.Invoke)
	if err != nil {
		return nil, err
	}
	describeTool.InvokableTool = t

	return describeTool, nil
}
