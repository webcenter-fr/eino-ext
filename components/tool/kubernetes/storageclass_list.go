package kubernetes

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/goccy/go-json"
	storagev1 "k8s.io/api/storage/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

const storageClassListDescription = `
** General Purpose **
It lists all the StorageClasses in a specified Kubernetes cluster.

** Output **
It return a JSON array of objects, where each object represents a StorageClass with the following fields:
- name: the name of the StorageClass.
- namespace: the namespace of the StorageClass.
- isDefault: whether the StorageClass is the default one.
- provisioner: the provisioner of the StorageClass.
`

// StorageClassListOutput defines the structure of the output returned by the StorageClassList function. It represents a StorageClass with its name and namespace.
type StorageClassListOutput struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	IsDefault   bool   `json:"isDefault"`
	Provisioner string `json:"provisioner"`
}

// ToJson returns the JSON representation of the StorageClassListOutput struct. It marshals the struct into a JSON RawMessage and returns it. If there is an error during marshaling, it panics.
func (h *StorageClassListOutput) ToJson(o *storagev1.StorageClass) json.RawMessage {

	// Forge object
	output := CloneObject(h)
	output.Name = o.Name
	output.Namespace = o.Namespace
	output.IsDefault = o.Annotations["storageclass.kubernetes.io/is-default-class"] == "true"
	output.Provisioner = o.Provisioner

	data, err := json.Marshal(output)
	if err != nil {
		panic(err)
	}
	return data
}

// NewStorageClassListTool creates a new instance of the StorageClassListTool. It takes a context and a Configs object as parameters, builds Kubernetes clients for the provided configurations, and infers the tool using the description and invoke function. It returns the invokable tool or an error if any step fails.
func NewStorageClassListTool(ctx context.Context, configs Configs) (tool.InvokableTool, error) {
	s := scheme.Scheme
	utilruntime.Must(storagev1.AddToScheme(s))
	return NewListTool(ctx, configs, "kubernetes_list_storageclasses", storageClassListDescription, &storagev1.StorageClassList{}, &StorageClassListOutput{}, nil)
}
