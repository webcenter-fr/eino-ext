package kubernetes

import (
	"emperror.dev/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var restMapper meta.RESTMapper

// NewClient creates a new Kubernetes client using the provided configuration. It returns the client and any error encountered during the creation process.
func NewClient(config *rest.Config, s *runtime.Scheme) (c client.Client, err error) {

	if s == nil {
		s = scheme.Scheme
	}

	// client
	c, err = client.New(
		config,
		client.Options{
			Scheme: s,
			Mapper: restMapper,
		})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Kubernetes client")
	}

	return c, nil
}

// BuildClients creates Kubernetes clients for all configurations present in the Configs map. It returns a map of cluster names to their corresponding Kubernetes clients, or an error if any client creation fails.
func BuildClients(configs Configs, s *runtime.Scheme) (clients map[string]client.Client, err error) {
	clients = make(map[string]client.Client)

	for clusterName, config := range configs {
		client, err := NewClient(config, s)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client for cluster %s", clusterName)
		}
		clients[clusterName] = client
	}

	return clients, nil
}

// BuildClientsFromKubeconfig creates Kubernetes clients for all kubeconfig paths provided in the input map. It builds the configuration for each cluster using the kubeconfig path and then creates a client for each cluster. It returns a map of cluster names to their corresponding Kubernetes clients, or an error if any step fails.
func BuildClientsFromKubeconfig(configsWithKubeconfigPath map[string]string, s *runtime.Scheme) (map[string]client.Client, error) {
	configs := make(Configs)
	for clusterName, kubeconfigPath := range configsWithKubeconfigPath {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to build config for cluster %s", clusterName)
		}
		configs[clusterName] = config
	}
	return BuildClients(configs, s)
}
