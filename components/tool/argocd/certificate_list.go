package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
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

// CertificateListParams defines the parameters for listing ArgoCD certificates.
type CertificateListParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) Go RE2 regex on each certificate JSON. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Invalid regex returns an error."`
}

// CertificateListOutput is the structured output for a certificate list.
type CertificateListOutput struct {
	CertInfo   string `json:"certInfo"`
	CertType   string `json:"certType"`
	ServerName string `json:"serverName"`
}

// CertificateListTool is an eino tool for listing ArgoCD certificates.
type CertificateListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns matching certificates as JSON.
func (t *CertificateListTool) Invoke(ctx context.Context, params *CertificateListParams) (result string, err error) {
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

	resp, err := c.Certificate().List(&api.CertificateQuery{})
	if err != nil {
		return "", errors.Wrap(err, "failed to list certificates")
	}

	return filterMapMarshal(resp.Items, re, func(item api.CertificateModel) CertificateListOutput {
		return CertificateListOutput{
			CertInfo:   item.CertInfo,
			CertType:   item.CertType,
			ServerName: item.ServerName,
		}
	})
}

// NewCertificateListTool creates a new CertificateListTool.
func NewCertificateListTool(ctx context.Context, configs Configs) (*CertificateListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &CertificateListTool{baseTool: base}
	t, err := utils.InferTool("argocd_certificate_list", fmt.Sprintf("%s\n%s", certificateListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}
