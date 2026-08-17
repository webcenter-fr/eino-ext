package kubernetes

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const kubeCheckTimeout = 10 * time.Second

// Check performs a health check against configured Kubernetes clusters.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "kubernetes",
			Status:    checkup.StatusError,
			Error:     "no Kubernetes clusters configured",
		}}
	}

	clusters := configs.GetClusterNames()
	var all checkup.Results

	for _, cluster := range clusters {
		cc := configs.GetConfig(cluster)
		if cc == nil {
			all = append(all, clientErrorResults(cluster, fmt.Errorf("nil config"))...)
			continue
		}

		baseCtx, cancel := context.WithTimeout(ctx, kubeCheckTimeout)
		cfg := cc.Config

		results := func() checkup.Results {
			defer cancel()
			return probeCluster(baseCtx, cfg, cluster)
		}()
		all = append(all, results...)
	}

	return all
}

func clientErrorResults(cluster string, err error) checkup.Results {
	errStr := err.Error()
	r := make(checkup.Results, 0, 8)
	for _, name := range allComponentNames() {
		r = append(r, checkup.Result{
			Component: name,
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     errStr,
		})
	}
	return r
}

func coreKinds() []string {
	return []string{"pods", "configmaps", "nodes", "namespaces"}
}

func probeCluster(ctx context.Context, cfg *rest.Config, cluster string) checkup.Results {
	var results checkup.Results

	results = append(results, checkup.Result{
		Component: "kubernetes_cluster_list",
		Instance:  cluster,
		Status:    checkup.StatusOK,
	})

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return append(results, clientErrorResults(cluster, errors.Wrap(err, "failed to create dynamic client"))...)
	}

	mapper, err := newCachedMapper(cfg)
	if err != nil {
		return append(results, clientErrorResults(cluster, errors.Wrap(err, "failed to create mapper"))...)
	}

	for _, kind := range coreKinds() {
		r := probeKind(ctx, dc, mapper, cluster, kind)
		results = append(results, r...)
	}

	results = append(results, checkup.Result{
		Component: "kubernetes_list",
		Instance:  cluster,
		Status:    checkup.StatusLimited,
		Message:   "CRD-only kinds (kafka, olm, ocp, spark, monitoring.coreos.com) tested with dedicated env",
	})

	results = append(results, checkup.Result{
		Component: "kubernetes_pod_log",
		Instance:  cluster,
		Status:    checkup.StatusLimited,
		Message:   "requires pod name and container to probe",
	})

	// Write tools are not probed (side effects); report as limited.
	writeLimited := []string{
		"kubernetes_resource_create",
		"kubernetes_resource_apply",
		"kubernetes_resource_patch",
		"kubernetes_resource_delete",
	}
	for _, name := range writeLimited {
		results = append(results, checkup.Result{
			Component: name,
			Instance:  cluster,
			Status:    checkup.StatusLimited,
			Message:   "write tool, not probed to avoid side effects",
		})
	}

	return results
}

func probeKind(ctx context.Context, dc dynamic.Interface, mapper *cachedMapper, cluster, kind string) checkup.Results {
	var results checkup.Results

	resolved, err := mapper.Resolve(ctx, kind)
	if err != nil {
		results = append(results, checkup.Result{
			Component: "kubernetes_list",
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     errors.Wrapf(err, "failed to resolve kind %q", kind).Error(),
		})
		results = append(results, checkup.Result{
			Component: "kubernetes_describe",
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
		return results
	}

	list, err := dc.Resource(resolved.GVR).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return checkup.Results{
			{Component: "kubernetes_list", Instance: cluster, Status: checkup.StatusError, Error: errors.Wrap(err, "failed to list").Error()},
			{Component: "kubernetes_describe", Instance: cluster, Status: checkup.StatusError, Error: "dependency failed"},
		}
	}

	results = append(results, checkup.Result{
		Component: "kubernetes_list",
		Instance:  cluster,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d items found, RBAC ok", len(list.Items)),
	})

	if len(list.Items) == 0 {
		results = append(results, checkup.Result{
			Component: "kubernetes_describe",
			Instance:  cluster,
			Status:    checkup.StatusLimited,
			Message:   "no resources to test describe",
		})
		return results
	}

	first := list.Items[0]
	_, err = dc.Resource(resolved.GVR).Namespace(first.GetNamespace()).Get(ctx, first.GetName(), metav1.GetOptions{})
	if err != nil {
		results = append(results, checkup.Result{
			Component: "kubernetes_describe",
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe").Error(),
		})
		return results
	}

	results = append(results, checkup.Result{
		Component: "kubernetes_describe",
		Instance:  cluster,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described %q, RBAC ok", first.GetName()),
	})

	return results
}

func allComponentNames() []string {
	return []string{
		"kubernetes_cluster_list",
		"kubernetes_list",
		"kubernetes_describe",
		"kubernetes_pod_log",
		"kubernetes_resource_create",
		"kubernetes_resource_apply",
		"kubernetes_resource_patch",
		"kubernetes_resource_delete",
	}
}
