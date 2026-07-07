package safety

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OwnershipInfo describes what manages a Kubernetes resource.
type OwnershipInfo struct {
	IsManaged       bool     `json:"isManaged"`
	ManagedBy       string   `json:"managedBy,omitempty"`       // e.g., "argocd", "helm", "flux"
	ControllerName  string   `json:"controllerName,omitempty"`  // e.g., "deployment-controller"
	OwnerReferences []string `json:"ownerReferences,omitempty"` // Owner reference names
	Warnings        []string `json:"warnings,omitempty"`        // Human-readable warnings
}

// Well-known annotation and label keys for operator/controller detection.
const (
	argoCDAnnotation  = "argocd.argoproj.io/instance"
	helmAnnotation    = "meta.helm.sh/release-name"
	fluxAnnotation    = "kustomize.toolkit.fluxcd.io/name"
	kubectlAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
	managedByLabel    = "app.kubernetes.io/managed-by"
)

// CheckOwnership inspects a Kubernetes object for controller/operator management.
// It checks ownerReferences, managed-by annotations, and labels from ArgoCD,
// Helm, Flux, and kubectl.
func CheckOwnership(obj metav1.Object) OwnershipInfo {
	info := OwnershipInfo{}

	annotations := obj.GetAnnotations()
	labels := obj.GetLabels()

	// Check ownerReferences.
	for _, ref := range obj.GetOwnerReferences() {
		info.IsManaged = true
		info.OwnerReferences = append(info.OwnerReferences,
			fmt.Sprintf("%s/%s", ref.APIVersion, ref.Name))
		if ref.Controller != nil && *ref.Controller {
			info.ControllerName = ref.Name
		}
	}

	if annotations == nil {
		annotations = map[string]string{}
	}
	if labels == nil {
		labels = map[string]string{}
	}

	// Check ArgoCD annotation.
	if instance, ok := annotations[argoCDAnnotation]; ok && instance != "" {
		info.IsManaged = true
		info.ManagedBy = "argocd"
		info.Warnings = append(info.Warnings,
			fmt.Sprintf("This resource is managed by ArgoCD (instance: %s). Modifying it directly may cause drift.", instance))
	}

	// Check Helm annotation.
	if release, ok := annotations[helmAnnotation]; ok && release != "" {
		info.IsManaged = true
		if info.ManagedBy == "" {
			info.ManagedBy = "helm"
		}
		info.Warnings = append(info.Warnings,
			fmt.Sprintf("This resource is managed by Helm (release: %s). Modifying it directly may cause drift.", release))
	}

	// Check Flux annotation.
	if name, ok := annotations[fluxAnnotation]; ok && name != "" {
		info.IsManaged = true
		if info.ManagedBy == "" {
			info.ManagedBy = "flux"
		}
		info.Warnings = append(info.Warnings,
			fmt.Sprintf("This resource is managed by Flux (kustomization: %s). Modifying it directly may cause drift.", name))
	}

	// Check generic managed-by label.
	if managedBy, ok := labels[managedByLabel]; ok && managedBy != "" {
		info.IsManaged = true
		if info.ManagedBy == "" {
			info.ManagedBy = managedBy
		}
		info.Warnings = append(info.Warnings,
			fmt.Sprintf("This resource is managed by %q (via app.kubernetes.io/managed-by label).", managedBy))
	}

	// Check kubectl annotation (less strict — only warn, don't mark as managed).
	if _, ok := annotations[kubectlAnnotation]; ok {
		info.Warnings = append(info.Warnings,
			"This resource was previously applied with kubectl. Modifying it may conflict with expected state.")
	}

	return info
}
