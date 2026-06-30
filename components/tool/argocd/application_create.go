package argocd

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const applicationCreateDescription = `
** General Purpose **
It creates a new ArgoCD application.

** Output **
It returns the created application JSON.
`

type ApplicationCreateParams struct {
	Instance       string `json:"instance" validate:"required" jsonschema:"(required) The ArgoCD instance to connect to."`
	Name           string `json:"name" validate:"required" jsonschema:"(required) The application name."`
	Project        string `json:"project,omitempty" jsonschema:"(optional) ArgoCD project name. Defaults to 'default'."`
	AppNamespace   string `json:"appNamespace,omitempty" jsonschema:"(optional) Application namespace."`
	RepoURL        string `json:"repoURL" validate:"required" jsonschema:"(required) Git repository URL."`
	TargetRevision string `json:"targetRevision,omitempty" jsonschema:"(optional) Git branch/tag/commit. Defaults to 'HEAD'."`
	Path           string `json:"path,omitempty" jsonschema:"(optional) Path within the repo."`
	DestServer     string `json:"destServer" validate:"required" jsonschema:"(required) Destination cluster API server URL."`
	DestNamespace  string `json:"destNamespace,omitempty" jsonschema:"(optional) Destination namespace."`
	Upsert         bool   `json:"upsert,omitempty" jsonschema:"(optional) Update if application already exists."`
}

type ApplicationCreateTool struct {
	clients        map[string]*Client
	knownInstances []string

	tool.InvokableTool
}

func (t *ApplicationCreateTool) Invoke(ctx context.Context, params *ApplicationCreateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, ok := t.clients[params.Instance]
	if !ok {
		return "", instanceNotFoundError(params.Instance, t.knownInstances)
	}

	project := params.Project
	if project == "" {
		project = "default"
	}

	targetRevision := params.TargetRevision
	if targetRevision == "" {
		targetRevision = "HEAD"
	}

	req := &ApplicationCreateRequest{
		Metadata: ObjectMeta{
			Name:      params.Name,
			Namespace: params.AppNamespace,
		},
		Spec: ApplicationSpec{
			Source: &ApplicationSource{
				RepoURL:        params.RepoURL,
				Path:           params.Path,
				TargetRevision: targetRevision,
			},
			Destination: &ApplicationDestination{
				Server:    params.DestServer,
				Namespace: params.DestNamespace,
			},
			Project: project,
		},
	}

	app, err := c.CreateApplication(ctx, req, params.Upsert)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(app)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}

	return string(data), nil
}

func NewApplicationCreateTool(ctx context.Context, configs Configs) (*ApplicationCreateTool, error) {
	clients, err := NewClients(configs)
	if err != nil {
		return nil, err
	}

	createTool := &ApplicationCreateTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
	}

	t, err := utils.InferTool("argocd_application_create", applicationCreateDescription, createTool.Invoke)
	if err != nil {
		return nil, err
	}
	createTool.InvokableTool = t

	return createTool, nil
}
