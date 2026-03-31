package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	corev1 "k8s.io/api/core/v1"
)

const pvcListDescription = `
** General Purpose **
It lists all the PersistentVolumeClaims (PVCs) in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a PVC with the following fields:
- name: the name of the PVC.
- namespace: the namespace of the PVC.
- status: the current status of the PVC (e.g., Bound, Pending, Lost).
- storageClass: the storage class of the PVC.
- capacity: the storage capacity of the PVC.
`

// PVCListOutput defines the structure of the output returned by the PVCList function. It represents a PVC with its name, namespace, status, storage class, and capacity.
type PVCListOutput struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Status       string `json:"status"`
	StorageClass string `json:"storageClass"`
	Capacity     string `json:"capacity"`
}

// ToJson returns the JSON representation of the PVCListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *PVCListOutput) ToJson(o *corev1.PersistentVolumeClaim) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.Status = string(o.Status.Phase)
	if o.Spec.StorageClassName != nil {
		output.StorageClass = *o.Spec.StorageClassName
	}
	if o.Status.Capacity != nil {
		if capacity, ok := o.Status.Capacity[corev1.ResourceStorage]; ok {
			output.Capacity = capacity.String()
		}
	}

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewPVCListTool creates a new instance of the PVCListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewPVCListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	return NewListTool(ctx, configs, "kubernetes_list_pvcs", pvcListDescription, &corev1.PersistentVolumeClaimList{}, &PVCListOutput{}, nil)
}
