package argocd

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const argocdCheckTimeout = 10 * time.Second

// Check probes connectivity and RBAC permissions for all configured ArgoCD
// instances. For each instance it tests every read-only tool: list endpoints are
// called directly; describe endpoints are tested by listing first, then
// describing the first item. Write tools (create/delete/sync) are not probed.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "argocd",
			Status:    checkup.StatusError,
			Error:     "no ArgoCD instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		cfg := configs.GetConfig(instance)
		baseCtx, baseCancel := context.WithTimeout(ctx, argocdCheckTimeout)

		client, err := NewClient(baseCtx, cfg)
		if err != nil {
			all = append(all, clientErrorResults(instance, err)...)
			baseCancel()
			continue
		}
		func() {
			defer baseCancel()
			all = append(all, probeInstance(baseCtx, client, instance)...)
		}()
	}

	return all
}

func clientErrorResults(instance string, err error) checkup.Results {
	errStr := err.Error()
	return checkup.Results{
		{Component: "argocd_instance_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_application_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_application_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_cluster_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_cluster_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_project_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_project_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_repository_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_repository_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_certificate_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
	}
}

func probeInstance(ctx context.Context, client api.API, instance string) checkup.Results {
	var results checkup.Results

	results = append(results, probeInstanceList(ctx, instance))

	listResult, apps, err := probeApplicationList(ctx, client, instance)
	results = append(results, listResult)
	if err == nil && len(apps) > 0 {
		results = append(results, probeApplicationDescribe(ctx, client, instance, apps[0].Name, apps[0].Namespace))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_application_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no applications to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_application_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	cr, clusters, err := probeClusterList(ctx, client, instance)
	results = append(results, cr)
	if err == nil && len(clusters) > 0 {
		results = append(results, probeClusterDescribe(ctx, client, instance, clusters[0].Name))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_cluster_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no clusters to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_cluster_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	pr, projects, err := probeProjectList(ctx, client, instance)
	results = append(results, pr)
	if err == nil && len(projects) > 0 {
		results = append(results, probeProjectDescribe(ctx, client, instance, projects[0].Name))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_project_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no projects to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_project_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	rr, repos, err := probeRepositoryList(ctx, client, instance)
	results = append(results, rr)
	if err == nil && len(repos) > 0 {
		results = append(results, probeRepositoryDescribe(ctx, client, instance, repos[0].Repo))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_repository_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no repositories to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_repository_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	cr2, _, _ := probeCertificateList(ctx, client, instance)
	results = append(results, cr2)

	return results
}

func probeInstanceList(ctx context.Context, instance string) checkup.Result {
	return checkup.Result{
		Component: "argocd_instance_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
	}
}

func probeApplicationList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.ApplicationModel, error) {
	resp, err := client.Application().List(&api.ApplicationListOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_application_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list applications").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d applications found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_application_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeApplicationDescribe(ctx context.Context, client api.API, instance, name, namespace string) checkup.Result {
	_, err := client.Application().Get(name, &api.ApplicationGetOptions{
		AppNamespace: namespace,
	})
	if err != nil {
		return checkup.Result{
			Component: "argocd_application_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe application").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_application_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described application %q, RBAC ok", name),
	}
}

func probeClusterList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.ClusterModel, error) {
	resp, err := client.Cluster().List(&api.ClusterQueryOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_cluster_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list clusters").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d clusters found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_cluster_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeClusterDescribe(ctx context.Context, client api.API, instance, name string) checkup.Result {
	_, err := client.Cluster().Get("", &api.ClusterQueryOptions{Name: name})
	if err != nil {
		return checkup.Result{
			Component: "argocd_cluster_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe cluster").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_cluster_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described cluster %q, RBAC ok", name),
	}
}

func probeProjectList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.ProjectModel, error) {
	resp, err := client.Project().List()
	if err != nil {
		return checkup.Result{
			Component: "argocd_project_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list projects").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d projects found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_project_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeProjectDescribe(ctx context.Context, client api.API, instance, name string) checkup.Result {
	_, err := client.Project().Get(name)
	if err != nil {
		return checkup.Result{
			Component: "argocd_project_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe project").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_project_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described project %q, RBAC ok", name),
	}
}

func probeRepositoryList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.RepositoryModel, error) {
	resp, err := client.Repository().List(&api.RepositoryQueryOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_repository_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list repositories").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d repositories found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_repository_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeRepositoryDescribe(ctx context.Context, client api.API, instance, repo string) checkup.Result {
	_, err := client.Repository().Get(repo, &api.RepositoryQueryOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_repository_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe repository").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_repository_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described repository %q, RBAC ok", repo),
	}
}

func probeCertificateList(ctx context.Context, client api.API, instance string) (checkup.Result, []api.CertificateModel, error) {
	resp, err := client.Certificate().List(&api.CertificateQuery{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_certificate_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list certificates").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d certificates found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_certificate_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}
