package kubernetes

import (
	"context"
	"fmt"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const kubeCheckTimeout = 10 * time.Second

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
		c, err := NewClient(baseCtx, cc.Config, nil)
		if err != nil {
			all = append(all, clientErrorResults(cluster, err)...)
			cancel()
			continue
		}

		results := func() checkup.Results {
			defer cancel()
			return probeCluster(baseCtx, c, cluster)
		}()
		all = append(all, results...)
	}

	return all
}

func clientErrorResults(cluster string, err error) checkup.Results {
	errStr := err.Error()
	r := make(checkup.Results, 0, 66)
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

type kubeResourceDescriptor struct {
	componentList    string
	componentDescribe string
	listObj          client.ObjectList
	getObj           client.Object
}

func coreResources() []kubeResourceDescriptor {
	return []kubeResourceDescriptor{
		{"kubernetes_list_pods", "kubernetes_describe_pod", &corev1.PodList{}, &corev1.Pod{}},
		{"kubernetes_list_deployments", "kubernetes_describe_deployment", &appsv1.DeploymentList{}, &appsv1.Deployment{}},
		{"kubernetes_list_statefulsets", "kubernetes_describe_statefulset", &appsv1.StatefulSetList{}, &appsv1.StatefulSet{}},
		{"kubernetes_list_daemonsets", "kubernetes_describe_daemonset", &appsv1.DaemonSetList{}, &appsv1.DaemonSet{}},
		{"kubernetes_list_configmaps", "kubernetes_describe_config_map", &corev1.ConfigMapList{}, &corev1.ConfigMap{}},
		{"kubernetes_list_secrets", "kubernetes_describe_secret", &corev1.SecretList{}, &corev1.Secret{}},
		{"kubernetes_list_services", "kubernetes_describe_service", &corev1.ServiceList{}, &corev1.Service{}},
		{"kubernetes_list_ingresses", "kubernetes_describe_ingress", &networkingv1.IngressList{}, &networkingv1.Ingress{}},
		{"kubernetes_list_persistent_volume_claims", "kubernetes_describe_persistent_volume_claim", &corev1.PersistentVolumeClaimList{}, &corev1.PersistentVolumeClaim{}},
		{"kubernetes_list_nodes", "kubernetes_describe_node", &corev1.NodeList{}, &corev1.Node{}},
		{"kubernetes_list_namespaces", "kubernetes_describe_namespace", &corev1.NamespaceList{}, &corev1.Namespace{}},
		{"kubernetes_list_events", "kubernetes_describe_event", &corev1.EventList{}, &corev1.Event{}},
		{"kubernetes_list_service_accounts", "kubernetes_describe_service_account", &corev1.ServiceAccountList{}, &corev1.ServiceAccount{}},
		{"kubernetes_list_storage_classes", "kubernetes_describe_storage_class", &storagev1.StorageClassList{}, &storagev1.StorageClass{}},
		{"kubernetes_list_custom_resource_definitions", "kubernetes_describe_custom_resource_definition", &apiextensionsv1.CustomResourceDefinitionList{}, &apiextensionsv1.CustomResourceDefinition{}},
	}
}

func probeCluster(ctx context.Context, c client.Client, cluster string) checkup.Results {
	var results checkup.Results

	results = append(results, checkup.Result{
		Component: "kubernetes_cluster_list",
		Instance:  cluster,
		Status:    checkup.StatusOK,
	})

	for _, rd := range coreResources() {
		r := probeCoreResource(ctx, c, cluster, rd)
		results = append(results, r...)
	}

	results = append(results, limitedResults(cluster,
		"kubernetes_list_kafka_clusters", "kubernetes_describe_kafka_cluster",
		"kubernetes_list_kafka_topics", "kubernetes_describe_kafka_topic",
		"kubernetes_list_kafka_node_pools", "kubernetes_describe_kafka_node_pool",
		"kubernetes_list_kafka_users", "kubernetes_describe_kafka_user",
		"kubernetes_list_olm_cluster_service_versions", "kubernetes_describe_olm_cluster_service_version",
		"kubernetes_list_olm_subscriptions", "kubernetes_describe_olm_subscription",
		"kubernetes_list_olm_install_plans", "kubernetes_describe_olm_install_plan",
		"kubernetes_list_ocp_routes", "kubernetes_describe_ocp_route",
		"kubernetes_list_spark_applications", "kubernetes_describe_spark_application",
	)...)

	results = append(results, checkup.Result{
		Component: "kubernetes_pod_log",
		Instance:  cluster,
		Status:    checkup.StatusLimited,
		Message:   "requires pod name and container to probe",
	})

	results = append(results, checkup.Result{
		Component: "kubernetes_resource_list",
		Instance:  cluster,
		Status:    checkup.StatusLimited,
		Message:   "requires GVR to probe",
	})
	results = append(results, checkup.Result{
		Component: "kubernetes_resource_describe",
		Instance:  cluster,
		Status:    checkup.StatusLimited,
		Message:   "requires GVR to probe",
	})

	return results
}

func probeCoreResource(ctx context.Context, c client.Client, cluster string, rd kubeResourceDescriptor) checkup.Results {
	var results checkup.Results

	if err := c.List(ctx, rd.listObj, &client.ListOptions{}); err != nil {
		results = append(results, checkup.Result{
			Component: rd.componentList,
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list").Error(),
		})
		results = append(results, checkup.Result{
			Component: rd.componentDescribe,
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
		return results
	}

	items := listItems(rd.listObj)
	results = append(results, checkup.Result{
		Component: rd.componentList,
		Instance:  cluster,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("%d items found, RBAC ok", len(items)),
	})

	if len(items) == 0 {
		results = append(results, checkup.Result{
			Component: rd.componentDescribe,
			Instance:  cluster,
			Status:    checkup.StatusLimited,
			Message:   "no resources to test describe",
		})
		return results
	}

	first := items[0]
	name, ns := itemNameAndNamespace(first)
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, rd.getObj); err != nil {
		results = append(results, checkup.Result{
			Component: rd.componentDescribe,
			Instance:  cluster,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe").Error(),
		})
		return results
	}

	results = append(results, checkup.Result{
		Component: rd.componentDescribe,
		Instance:  cluster,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described %q, RBAC ok", name),
	})

	return results
}

type listItem interface {
	GetName() string
	GetNamespace() string
}

func listItems(ol client.ObjectList) []listItem {
	switch l := ol.(type) {
	case *corev1.PodList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *appsv1.DeploymentList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *appsv1.StatefulSetList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *appsv1.DaemonSetList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *corev1.ConfigMapList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *corev1.SecretList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *corev1.ServiceList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *networkingv1.IngressList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *corev1.PersistentVolumeClaimList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *corev1.NodeList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name}
		}
		return r
	case *corev1.NamespaceList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name}
		}
		return r
	case *corev1.EventList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *corev1.ServiceAccountList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name, namespace: l.Items[i].Namespace}
		}
		return r
	case *storagev1.StorageClassList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name}
		}
		return r
	case *apiextensionsv1.CustomResourceDefinitionList:
		r := make([]listItem, len(l.Items))
		for i := range l.Items {
			r[i] = listItemAdapter{name: l.Items[i].Name}
		}
		return r
	default:
		panic("checkup: unhandled list type")
	}
}

type listItemAdapter struct {
	name      string
	namespace string
}

func (a listItemAdapter) GetName() string      { return a.name }
func (a listItemAdapter) GetNamespace() string { return a.namespace }

func itemNameAndNamespace(item listItem) (string, string) {
	return item.GetName(), item.GetNamespace()
}

func limitedResults(cluster string, names ...string) checkup.Results {
	r := make(checkup.Results, len(names))
	for i, name := range names {
		r[i] = checkup.Result{
			Component: name,
			Instance:  cluster,
			Status:    checkup.StatusLimited,
			Message:   "requires specialized CRD schemas not available in checkup",
		}
	}
	return r
}

func allComponentNames() []string {
	names := []string{"kubernetes_cluster_list"}
	for _, rd := range coreResources() {
		names = append(names, rd.componentList, rd.componentDescribe)
	}
	names = append(names,
		"kubernetes_list_kafka_clusters", "kubernetes_describe_kafka_cluster",
		"kubernetes_list_kafka_topics", "kubernetes_describe_kafka_topic",
		"kubernetes_list_kafka_node_pools", "kubernetes_describe_kafka_node_pool",
		"kubernetes_list_kafka_users", "kubernetes_describe_kafka_user",
		"kubernetes_list_olm_cluster_service_versions", "kubernetes_describe_olm_cluster_service_version",
		"kubernetes_list_olm_subscriptions", "kubernetes_describe_olm_subscription",
		"kubernetes_list_olm_install_plans", "kubernetes_describe_olm_install_plan",
		"kubernetes_list_ocp_routes", "kubernetes_describe_ocp_route",
		"kubernetes_list_spark_applications", "kubernetes_describe_spark_application",
		"kubernetes_pod_log",
		"kubernetes_resource_list", "kubernetes_resource_describe",
	)
	return names
}
