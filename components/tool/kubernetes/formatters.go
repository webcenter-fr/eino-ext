package kubernetes

import (
	"fmt"
	"reflect"
	"strings"

	strimzi "github.com/RedHatInsights/strimzi-client-go/apis/kafka.strimzi.io/v1beta2"
	"github.com/goccy/go-json"
	spark "github.com/kubeflow/spark-operator/api/v1beta2"
	routev1 "github.com/openshift/api/route/v1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type listFormatter func(runtime.Object) json.RawMessage

// describeFormatter produces a curated describeOutput for a single resource.
// When nil, DescribeTool falls back to the raw metadata/spec/status/data dump.
type describeFormatter func(*unstructured.Unstructured) describeOutput

type formatterEntry struct {
	newObj   func() runtime.Object // nil for unstructured-only kinds
	format   listFormatter         // list view (required)
	describe describeFormatter     // optional curated describe view
}

var formatterRegistry = initFormatterRegistry()

func strimziReadyStatus(conditions any) string {
	v := reflect.ValueOf(conditions)
	for i := 0; i < v.Len(); i++ {
		c := v.Index(i)
		t := c.FieldByName("Type")
		s := c.FieldByName("Status")
		if t.IsValid() && s.IsValid() && !t.IsNil() && !s.IsNil() {
			if t.Elem().String() == "Ready" {
				if s.Elem().String() == "True" {
					return "Ready"
				}
				return "Not Ready"
			}
		}
	}
	return ""
}

func initFormatterRegistry() map[schema.GroupVersionKind]formatterEntry {
	reg := make(map[schema.GroupVersionKind]formatterEntry)

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.Pod{} },
		format: func(o runtime.Object) json.RawMessage {
			pod := o.(*corev1.Pod)
			images := make([]string, 0, len(pod.Spec.Containers))
			for _, c := range pod.Spec.Containers {
				images = append(images, c.Image)
			}
			return marshal.MustMarshal(struct {
				Name      string   `json:"name"`
				Namespace string   `json:"namespace"`
				Status    string   `json:"status"`
				Node      string   `json:"node"`
				Images    []string `json:"images"`
				IP        string   `json:"ip"`
			}{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Status:    string(pod.Status.Phase),
				Node:      pod.Spec.NodeName,
				Images:    images,
				IP:        pod.Status.PodIP,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}] = formatterEntry{
		newObj: func() runtime.Object { return &appsv1.Deployment{} },
		format: func(o runtime.Object) json.RawMessage {
			d := o.(*appsv1.Deployment)
			images := make([]string, 0, len(d.Spec.Template.Spec.Containers))
			for _, c := range d.Spec.Template.Spec.Containers {
				images = append(images, c.Image)
			}
			return marshal.MustMarshal(struct {
				Name        string   `json:"name"`
				Namespace   string   `json:"namespace"`
				Status      string   `json:"status"`
				ExpectedPod int32    `json:"expectedPod"`
				CurrentPod  int32    `json:"currentPod"`
				ReadyPod    int32    `json:"readyPod"`
				Images      []string `json:"images"`
			}{
				Name:        d.Name,
				Namespace:   d.Namespace,
				ExpectedPod: d.Status.Replicas,
				CurrentPod:  d.Status.Replicas,
				ReadyPod:    d.Status.ReadyReplicas,
				Status:      fmt.Sprintf("%d/%d pods", d.Status.ReadyReplicas, d.Status.Replicas),
				Images:      images,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"}] = formatterEntry{
		newObj: func() runtime.Object { return &appsv1.StatefulSet{} },
		format: func(o runtime.Object) json.RawMessage {
			s := o.(*appsv1.StatefulSet)
			images := make([]string, 0, len(s.Spec.Template.Spec.Containers))
			for _, c := range s.Spec.Template.Spec.Containers {
				images = append(images, c.Image)
			}
			return marshal.MustMarshal(struct {
				Name        string   `json:"name"`
				Namespace   string   `json:"namespace"`
				Status      string   `json:"status"`
				ExpectedPod int32    `json:"expectedPod"`
				CurrentPod  int32    `json:"currentPod"`
				ReadyPod    int32    `json:"readyPod"`
				Images      []string `json:"images"`
			}{
				Name:        s.Name,
				Namespace:   s.Namespace,
				ExpectedPod: s.Status.Replicas,
				CurrentPod:  s.Status.Replicas,
				ReadyPod:    s.Status.ReadyReplicas,
				Status:      fmt.Sprintf("%d/%d pods", s.Status.ReadyReplicas, s.Status.Replicas),
				Images:      images,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DaemonSet"}] = formatterEntry{
		newObj: func() runtime.Object { return &appsv1.DaemonSet{} },
		format: func(o runtime.Object) json.RawMessage {
			d := o.(*appsv1.DaemonSet)
			images := make([]string, 0, len(d.Spec.Template.Spec.Containers))
			for _, c := range d.Spec.Template.Spec.Containers {
				images = append(images, c.Image)
			}
			return marshal.MustMarshal(struct {
				Name        string   `json:"name"`
				Namespace   string   `json:"namespace"`
				Status      string   `json:"status"`
				ExpectedPod int32    `json:"expectedPod"`
				CurrentPod  int32    `json:"currentPod"`
				ReadyPod    int32    `json:"readyPod"`
				Images      []string `json:"images"`
			}{
				Name:        d.Name,
				Namespace:   d.Namespace,
				ExpectedPod: d.Status.DesiredNumberScheduled,
				CurrentPod:  d.Status.CurrentNumberScheduled,
				ReadyPod:    d.Status.NumberReady,
				Status:      fmt.Sprintf("%d/%d pods", d.Status.NumberReady, d.Status.DesiredNumberScheduled),
				Images:      images,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.ConfigMap{} },
		format: func(o runtime.Object) json.RawMessage {
			cm := o.(*corev1.ConfigMap)
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			}{
				Name:      cm.Name,
				Namespace: cm.Namespace,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.Secret{} },
		format: func(o runtime.Object) json.RawMessage {
			s := o.(*corev1.Secret)
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Type      string `json:"type"`
			}{
				Name:      s.Name,
				Namespace: s.Namespace,
				Type:      string(s.Type),
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.Service{} },
		format: func(o runtime.Object) json.RawMessage {
			svc := o.(*corev1.Service)
			var ips []string
			var ports []string
			switch svc.Spec.Type {
			case corev1.ServiceTypeClusterIP:
				if svc.Spec.ClusterIP != "" {
					ips = append(ips, svc.Spec.ClusterIP)
				}
				for _, p := range svc.Spec.Ports {
					ports = append(ports, fmt.Sprintf("%d", p.Port))
				}
			case corev1.ServiceTypeNodePort:
				if svc.Spec.ClusterIP != "" {
					ips = append(ips, svc.Spec.ClusterIP)
				}
				for _, p := range svc.Spec.Ports {
					ports = append(ports, fmt.Sprintf("%d", p.NodePort))
				}
			case corev1.ServiceTypeLoadBalancer:
				for _, ing := range svc.Status.LoadBalancer.Ingress {
					if ing.IP != "" {
						ips = append(ips, ing.IP)
					} else if ing.Hostname != "" {
						ips = append(ips, ing.Hostname)
					}
				}
			}
			return marshal.MustMarshal(struct {
				Name      string   `json:"name"`
				Namespace string   `json:"namespace"`
				Type      string   `json:"type"`
				IPs       []string `json:"ips"`
				Ports     []string `json:"ports"`
			}{
				Name:      svc.Name,
				Namespace: svc.Namespace,
				Type:      string(svc.Spec.Type),
				IPs:       ips,
				Ports:     ports,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}] = formatterEntry{
		newObj: func() runtime.Object { return &networkingv1.Ingress{} },
		format: func(o runtime.Object) json.RawMessage {
			ing := o.(*networkingv1.Ingress)
			hosts := make([]string, 0, len(ing.Spec.Rules))
			for _, rule := range ing.Spec.Rules {
				hosts = append(hosts, rule.Host)
			}
			tls := make([]string, 0, len(ing.Spec.TLS))
			for _, t := range ing.Spec.TLS {
				tls = append(tls, t.Hosts...)
			}
			return marshal.MustMarshal(struct {
				Name      string   `json:"name"`
				Namespace string   `json:"namespace"`
				Hosts     []string `json:"hosts"`
				TLS       []string `json:"tls"`
			}{
				Name:      ing.Name,
				Namespace: ing.Namespace,
				Hosts:     hosts,
				TLS:       tls,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "PersistentVolumeClaim"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.PersistentVolumeClaim{} },
		format: func(o runtime.Object) json.RawMessage {
			pvc := o.(*corev1.PersistentVolumeClaim)
			storageClass := ""
			if pvc.Spec.StorageClassName != nil {
				storageClass = *pvc.Spec.StorageClassName
			}
			capacity := ""
			if pvc.Status.Capacity != nil {
				if c, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
					capacity = c.String()
				}
			}
			return marshal.MustMarshal(struct {
				Name         string `json:"name"`
				Namespace    string `json:"namespace"`
				Status       string `json:"status"`
				StorageClass string `json:"storageClass"`
				Capacity     string `json:"capacity"`
			}{
				Name:         pvc.Name,
				Namespace:    pvc.Namespace,
				Status:       string(pvc.Status.Phase),
				StorageClass: storageClass,
				Capacity:     capacity,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.Node{} },
		format: func(o runtime.Object) json.RawMessage {
			n := o.(*corev1.Node)
			internalIP := ""
			externalIP := ""
			for _, addr := range n.Status.Addresses {
				switch addr.Type {
				case corev1.NodeInternalIP:
					internalIP = addr.Address
				case corev1.NodeExternalIP:
					externalIP = addr.Address
				}
			}
			status := "NotReady"
			for _, cond := range n.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					status = "Ready"
					break
				}
			}
			return marshal.MustMarshal(struct {
				Name       string `json:"name"`
				Version    string `json:"version"`
				InternalIP string `json:"internalIP"`
				ExternalIP string `json:"externalIP"`
				OS         string `json:"OS"`
				Status     string `json:"status"`
			}{
				Name:       n.Name,
				Version:    n.Status.NodeInfo.KubeletVersion,
				InternalIP: internalIP,
				ExternalIP: externalIP,
				OS:         n.Status.NodeInfo.OperatingSystem,
				Status:     status,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.Namespace{} },
		format: func(o runtime.Object) json.RawMessage {
			ns := o.(*corev1.Namespace)
			return marshal.MustMarshal(struct {
				Name string `json:"name"`
			}{Name: ns.Name})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Event"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.Event{} },
		format: func(o runtime.Object) json.RawMessage {
			e := o.(*corev1.Event)
			source := e.Source.Component
			if e.Source.Host != "" {
				source = fmt.Sprintf("%s/%s", e.Source.Component, e.Source.Host)
			}
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Reason    string `json:"reason"`
				Count     int32  `json:"count"`
				LastTime  string `json:"lastTime"`
				Source    string `json:"source"`
			}{
				Name:      e.Name,
				Namespace: e.Namespace,
				Reason:    e.Reason,
				Count:     e.Count,
				LastTime:  e.LastTimestamp.String(),
				Source:    source,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ServiceAccount"}] = formatterEntry{
		newObj: func() runtime.Object { return &corev1.ServiceAccount{} },
		format: func(o runtime.Object) json.RawMessage {
			sa := o.(*corev1.ServiceAccount)
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			}{
				Name:      sa.Name,
				Namespace: sa.Namespace,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass"}] = formatterEntry{
		newObj: func() runtime.Object { return &storagev1.StorageClass{} },
		format: func(o runtime.Object) json.RawMessage {
			sc := o.(*storagev1.StorageClass)
			return marshal.MustMarshal(struct {
				Name        string `json:"name"`
				Namespace   string `json:"namespace"`
				IsDefault   bool   `json:"isDefault"`
				Provisioner string `json:"provisioner"`
			}{
				Name:        sc.Name,
				Namespace:   sc.Namespace,
				IsDefault:   sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true",
				Provisioner: sc.Provisioner,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}] = formatterEntry{
		newObj: func() runtime.Object { return &apiextensionsv1.CustomResourceDefinition{} },
		format: func(o runtime.Object) json.RawMessage {
			crd := o.(*apiextensionsv1.CustomResourceDefinition)
			versions := make([]string, 0, len(crd.Spec.Versions))
			for _, v := range crd.Spec.Versions {
				versions = append(versions, v.Name)
			}
			return marshal.MustMarshal(struct {
				Name      string   `json:"name"`
				Namespace string   `json:"namespace"`
				Group     string   `json:"group"`
				Kind      string   `json:"kind"`
				Versions  []string `json:"versions"`
			}{
				Name:     crd.Name,
				Group:    crd.Spec.Group,
				Kind:     strings.ToLower(crd.Spec.Names.Plural),
				Versions: versions,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "kafka.strimzi.io", Version: "v1beta2", Kind: "Kafka"}] = formatterEntry{
		newObj: func() runtime.Object { return &strimzi.Kafka{} },
		format: func(o runtime.Object) json.RawMessage {
			k := o.(*strimzi.Kafka)
			version := ""
			if k.Status.KafkaVersion != nil {
				version = *k.Status.KafkaVersion
			}
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Status    string `json:"status"`
				Version   string `json:"version"`
			}{
				Name:      k.Name,
				Namespace: k.Namespace,
				Status:    strimziReadyStatus(k.Status.Conditions),
				Version:   version,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "kafka.strimzi.io", Version: "v1beta2", Kind: "KafkaTopic"}] = formatterEntry{
		newObj: func() runtime.Object { return &strimzi.KafkaTopic{} },
		format: func(o runtime.Object) json.RawMessage {
			kt := o.(*strimzi.KafkaTopic)
			topicName := ""
			if kt.Spec.TopicName != nil {
				topicName = *kt.Spec.TopicName
			}
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				TopicName string `json:"topicName"`
				Status    string `json:"status"`
			}{
				Name:      kt.Name,
				Namespace: kt.Namespace,
				TopicName: topicName,
				Status:    strimziReadyStatus(kt.Status.Conditions),
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "kafka.strimzi.io", Version: "v1beta2", Kind: "KafkaNodePool"}] = formatterEntry{
		newObj: func() runtime.Object { return &strimzi.KafkaNodePool{} },
		format: func(o runtime.Object) json.RawMessage {
			knp := o.(*strimzi.KafkaNodePool)
			replicas := int32(0)
			if knp.Status.Replicas != nil {
				replicas = *knp.Status.Replicas
			}
			roles := make([]string, 0, len(knp.Spec.Roles))
			for _, role := range knp.Spec.Roles {
				roles = append(roles, string(role))
			}
			return marshal.MustMarshal(struct {
				Name      string   `json:"name"`
				Namespace string   `json:"namespace"`
				Status    string   `json:"status"`
				Replicas  int32    `json:"replicas"`
				Roles     []string `json:"roles"`
			}{
				Name:      knp.Name,
				Namespace: knp.Namespace,
				Status:    strimziReadyStatus(knp.Status.Conditions),
				Replicas:  replicas,
				Roles:     roles,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "kafka.strimzi.io", Version: "v1beta2", Kind: "KafkaUser"}] = formatterEntry{
		newObj: func() runtime.Object { return &strimzi.KafkaUser{} },
		format: func(o runtime.Object) json.RawMessage {
			ku := o.(*strimzi.KafkaUser)
			username := ""
			if ku.Status.Username != nil {
				username = *ku.Status.Username
			}
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Username  string `json:"username"`
				Status    string `json:"status"`
			}{
				Name:      ku.Name,
				Namespace: ku.Namespace,
				Username:  username,
				Status:    strimziReadyStatus(ku.Status.Conditions),
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "ClusterServiceVersion"}] = formatterEntry{
		newObj: func() runtime.Object { return &olmv1alpha1.ClusterServiceVersion{} },
		format: func(o runtime.Object) json.RawMessage {
			csv := o.(*olmv1alpha1.ClusterServiceVersion)
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Status    string `json:"status"`
				Version   string `json:"version"`
			}{
				Name:      csv.Name,
				Namespace: csv.Namespace,
				Status:    string(csv.Status.Phase),
				Version:   csv.Spec.Version.String(),
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}] = formatterEntry{
		newObj: func() runtime.Object { return &olmv1alpha1.Subscription{} },
		format: func(o runtime.Object) json.RawMessage {
			sub := o.(*olmv1alpha1.Subscription)
			return marshal.MustMarshal(struct {
				Name        string `json:"name"`
				Namespace   string `json:"namespace"`
				Status      string `json:"status"`
				Version     string `json:"version"`
				SourceName  string `json:"sourceName"`
				PackageName string `json:"packageName"`
			}{
				Name:        sub.Name,
				Namespace:   sub.Namespace,
				Status:      string(sub.Status.State),
				Version:     sub.Status.InstalledCSV,
				SourceName:  fmt.Sprintf("%s/%s", sub.Spec.CatalogSourceNamespace, sub.Spec.CatalogSource),
				PackageName: sub.Spec.Package,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "InstallPlan"}] = formatterEntry{
		newObj: func() runtime.Object { return &olmv1alpha1.InstallPlan{} },
		format: func(o runtime.Object) json.RawMessage {
			ip := o.(*olmv1alpha1.InstallPlan)
			return marshal.MustMarshal(struct {
				Name       string `json:"name"`
				Namespace  string `json:"namespace"`
				Status     string `json:"status"`
				IsApproved bool   `json:"isApproved"`
			}{
				Name:       ip.Name,
				Namespace:  ip.Namespace,
				Status:     string(ip.Status.Phase),
				IsApproved: ip.Spec.Approved,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "route.openshift.io", Version: "v1", Kind: "Route"}] = formatterEntry{
		newObj: func() runtime.Object { return &routev1.Route{} },
		format: func(o runtime.Object) json.RawMessage {
			r := o.(*routev1.Route)
			tls := r.Spec.TLS != nil && r.Spec.TLS.Termination != ""
			return marshal.MustMarshal(struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
				Host      string `json:"host"`
				TLS       bool   `json:"tls"`
			}{
				Name:      r.Name,
				Namespace: r.Namespace,
				Host:      r.Spec.Host,
				TLS:       tls,
			})
		},
	}

	reg[schema.GroupVersionKind{Group: "sparkoperator.k8s.io", Version: "v1beta2", Kind: "SparkApplication"}] = formatterEntry{
		newObj: func() runtime.Object { return &spark.SparkApplication{} },
		format: func(o runtime.Object) json.RawMessage {
			sa := o.(*spark.SparkApplication)
			lastAttempt := ""
			if !sa.Status.LastSubmissionAttemptTime.IsZero() {
				lastAttempt = sa.Status.LastSubmissionAttemptTime.String()
			}
			terminationTime := ""
			if !sa.Status.TerminationTime.IsZero() {
				terminationTime = sa.Status.TerminationTime.String()
			}
			return marshal.MustMarshal(struct {
				Name            string `json:"name"`
				Namespace       string `json:"namespace"`
				Status          string `json:"status"`
				LastAttempt     string `json:"lastAttempt"`
				TerminationTime string `json:"terminationTime"`
			}{
				Name:            sa.Name,
				Namespace:       sa.Namespace,
				Status:          string(sa.Status.AppState.State),
				LastAttempt:     lastAttempt,
				TerminationTime: terminationTime,
			})
		},
	}

	registerMonitoringFormatters(reg)

	return reg
}

func defaultListFormatter(u *unstructured.Unstructured) json.RawMessage {
	var status string
	for _, c := range uSlice(uStatus(u), "conditions") {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if uString(cm, "type") == "Ready" {
			if uString(cm, "status") == "True" {
				status = "Ready"
			} else {
				status = "Not Ready"
			}
			break
		}
	}

	return marshal.MustMarshal(struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Status    string `json:"status,omitempty"`
	}{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		Status:    status,
	})
}

func formatListItem(u *unstructured.Unstructured) json.RawMessage {
	apiVersion := u.GetAPIVersion()
	kind := u.GetKind()
	if apiVersion == "" || kind == "" {
		return defaultListFormatter(u)
	}

	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return defaultListFormatter(u)
	}

	gvk := schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    kind,
	}

	entry, ok := formatterRegistry[gvk]
	if !ok {
		return defaultListFormatter(u)
	}

	if entry.newObj == nil {
		// Unstructured-only formatter (e.g. monitoring CRDs without typed deps).
		return entry.format(u)
	}
	dst := entry.newObj()
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, dst); err != nil {
		return defaultListFormatter(u)
	}

	return entry.format(dst)
}

func redactSecretData(data any) {
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	for k := range m {
		m[k] = "REDACTED"
	}
}
