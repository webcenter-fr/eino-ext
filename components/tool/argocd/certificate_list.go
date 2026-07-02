package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/goccy/go-json"
)

const certificateListDescription = `
** General Purpose **
It lists all ArgoCD certificates accessible to the configured instance.

** Output **
It returns a JSON array of objects, where each object represents a certificate with the following fields:
- certInfo: the certificate information.
- certType: the certificate type.
- serverName: the server name.
`

type CertificateListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex on each certificate JSON."`
}

type CertificateListOutput struct {
	CertInfo   string `json:"certInfo"`
	CertType   string `json:"certType"`
	ServerName string `json:"serverName"`
}

type CertificateListTool struct {
	clients        map[string]api.API
	knownInstances []string

	tool.InvokableTool
}

func (t *CertificateListTool) Invoke(ctx context.Context, params *CertificateListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	filter, err := CompileFilter(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	resp, err := c.Certificate().List(&api.CertificateQuery{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list certificates")
	}

	outputs := make([]json.RawMessage, 0, len(resp.Items))
	for _, item := range resp.Items {
		output := CertificateListOutput{
			CertInfo:   item.CertInfo,
			CertType:   item.CertType,
			ServerName: item.ServerName,
		}

		outputJSON := json.RawMessage(MustMarshal(output))
		if !IsMatch(outputJSON, filter) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewCertificateListTool(ctx context.Context, configs Configs) (*CertificateListTool, error) {
	clients, err := BuildClients(configs)
	if err != nil {
		return nil, err
	}

	listTool := &CertificateListTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_certificate_list", fmt.Sprintf("%s\n%s", certificateListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
