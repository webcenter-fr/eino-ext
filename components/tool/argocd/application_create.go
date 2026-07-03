package argocd

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/disaster37/goargocdclient/api"
	"github.com/goccy/go-json"
	"k8s.io/utils/ptr"
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
	DestServer     string `json:"destServer" validate:"required" jsonschema:"(required) Destination server name"`
	DestNamespace  string `json:"destNamespace,omitempty" jsonschema:"(optional) Destination namespace."`
	Upsert         bool   `json:"upsert,omitempty" jsonschema:"(optional) Update if application already exists."`
	DryRun         bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the creation without making changes. Show the result to the user and ask for confirmation."`
	Confirmed      bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute the creation. Set this after the user has approved the dry-run result."`
}

type ApplicationCreateTool struct {
	*baseTool
	tool.InvokableTool
}

func (t *ApplicationCreateTool) Invoke(ctx context.Context, params *ApplicationCreateParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	project := params.Project
	if project == "" {
		project = "default"
	}

	targetRevision := params.TargetRevision
	if targetRevision == "" {
		targetRevision = "HEAD"
	}

	// Build the application model for dry-run preview.
	appModel := &api.ApplicationModel{
		Spec: api.ApplicationSpec{
			Source: &api.ApplicationSource{
				RepoURL:        params.RepoURL,
				Path:           params.Path,
				TargetRevision: targetRevision,
			},
			Destination: api.ApplicationDestination{
				Name:      params.DestServer,
				Namespace: params.DestNamespace,
			},
			Project: project,
		},
		ObjectMeta: api.ObjectMeta{
			Name:      params.Name,
			Namespace: params.AppNamespace,
		},
	}

	if params.DryRun {
		// Client-side dry-run: return the app model that would be created.
		data, marshalErr := json.Marshal(appModel)
		if marshalErr != nil {
			return "", errors.Wrap(marshalErr, "failed to marshal output")
		}
		return fmt.Sprintf(`{"dryRun": true, "wouldCreate": %s}`, string(data)), nil
	}

	app, err := c.Application().Create(appModel, &api.ApplicationCreateOptions{
		Upsert:   &params.Upsert,
		Validate: ptr.To(true),
	})
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
	base, err := newBaseTool(configs)
	if err != nil {
		return nil, err
	}

	createTool := &ApplicationCreateTool{baseTool: base}
	t, err := utils.InferTool("argocd_application_create", applicationCreateDescription, createTool.Invoke)
	if err != nil {
		return nil, err
	}
	createTool.InvokableTool = t

	return createTool, nil
}
