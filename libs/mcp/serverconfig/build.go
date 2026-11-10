package serverconfig

import (
	"context"

	"emperror.dev/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	argocdmcp "github.com/webcenter-fr/eino-ext/components/mcp/argocd"
	grafanamcp "github.com/webcenter-fr/eino-ext/components/mcp/grafana"
	kubernetesmcp "github.com/webcenter-fr/eino-ext/components/mcp/kubernetes"
	prometheusmcp "github.com/webcenter-fr/eino-ext/components/mcp/prometheus"
	argocdtool "github.com/webcenter-fr/eino-ext/components/tool/argocd"
	grafanatool "github.com/webcenter-fr/eino-ext/components/tool/grafana"
	k8stool "github.com/webcenter-fr/eino-ext/components/tool/kubernetes"
	promtool "github.com/webcenter-fr/eino-ext/components/tool/prometheus"
	mcpserver "github.com/webcenter-fr/eino-ext/libs/mcp"
)

// Build wires the configured family into a libs/mcp server. Client
// construction is lazy: no external service is contacted here.
func Build(ctx context.Context, cfg *Config) (*mcpserver.Server, error) {
	if cfg == nil {
		return nil, errors.New("serverconfig: config is nil")
	}
	mcpCfg := buildMCPConfig(cfg)

	switch cfg.Server {
	case "kubernetes":
		if cfg.Kubernetes == nil {
			return nil, errors.New("serverconfig: kubernetes family is not configured")
		}
		configs := make(k8stool.Configs, len(cfg.Kubernetes.Clusters))
		for name, entry := range cfg.Kubernetes.Clusters {
			restConfig, err := buildRESTConfig(entry)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to build kubernetes cluster %q", name)
			}
			configs[name] = &k8stool.ClusterConfig{
				Config:               restConfig,
				DisallowedNamespaces: entry.DisallowedNamespaces,
				DefaultTimeout:       entry.DefaultTimeout,
			}
		}
		return kubernetesmcp.NewServer(ctx, configs, runtime.NewScheme(), mcpCfg)

	case "argocd":
		if cfg.ArgoCD == nil {
			return nil, errors.New("serverconfig: argocd family is not configured")
		}
		configs := make(argocdtool.Configs, len(cfg.ArgoCD.Instances))
		for name, entry := range cfg.ArgoCD.Instances {
			configs[name] = argocdtool.Config{
				URL:           entry.URL,
				Token:         entry.Token,
				TLSSkipVerify: entry.TLSSkipVerify,
			}
		}
		return argocdmcp.NewServer(ctx, configs, mcpCfg)

	case "prometheus":
		if cfg.Prometheus == nil {
			return nil, errors.New("serverconfig: prometheus family is not configured")
		}
		configs := make(promtool.Configs, len(cfg.Prometheus.Instances))
		for name, entry := range cfg.Prometheus.Instances {
			configs[name] = promtool.Config{
				Address:       entry.URL,
				BearerToken:   entry.Token,
				TLSSkipVerify: entry.TLSSkipVerify,
			}
		}
		return prometheusmcp.NewServer(ctx, configs, mcpCfg)

	case "grafana":
		if cfg.Grafana == nil {
			return nil, errors.New("serverconfig: grafana family is not configured")
		}
		configs := make(grafanatool.Configs, len(cfg.Grafana.Instances))
		for name, entry := range cfg.Grafana.Instances {
			configs[name] = grafanatool.Config{
				URL:            entry.URL,
				Token:          entry.Token,
				TLSSkipVerify:  entry.TLSSkipVerify,
				DefaultTimeout: entry.DefaultTimeout,
			}
		}
		return grafanamcp.NewServer(ctx, configs, mcpCfg)

	default:
		return nil, errors.Errorf("unknown MCP server family %q: must be one of kubernetes, argocd, prometheus, grafana", cfg.Server)
	}
}

func buildRESTConfig(entry ClusterEntry) (*rest.Config, error) {
	if entry.InCluster {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, errors.Wrap(err, "failed to load in-cluster configuration")
		}
		return config, nil
	}
	if entry.Context == "" {
		config, err := clientcmd.BuildConfigFromFlags("", entry.KubeconfigFile)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to load kubeconfig %q", entry.KubeconfigFile)
		}
		return config, nil
	}
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: entry.KubeconfigFile}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: entry.Context}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load context %q from kubeconfig %q", entry.Context, entry.KubeconfigFile)
	}
	return config, nil
}

func buildMCPConfig(cfg *Config) *mcpserver.Config {
	mcpCfg := &mcpserver.Config{}
	if cfg.Auth != nil {
		providers := make([]mcpserver.ProviderConfig, 0, len(cfg.Auth.Providers))
		for _, provider := range cfg.Auth.Providers {
			translated := mcpserver.ProviderConfig{Type: provider.Type}
			if provider.Local != nil {
				tokens := make([]mcpserver.LocalToken, 0, len(provider.Local.Tokens))
				for _, token := range provider.Local.Tokens {
					tokens = append(tokens, mcpserver.LocalToken{
						Token:  token.Token,
						User:   token.User,
						Groups: token.Groups,
						Scopes: token.Scopes,
					})
				}
				translated.Local = &mcpserver.LocalProviderConfig{Tokens: tokens}
			}
			if provider.OIDC != nil {
				translated.OIDC = &mcpserver.OIDCProviderConfig{
					Issuer:      provider.OIDC.Issuer,
					Audience:    provider.OIDC.Audience,
					UserClaim:   provider.OIDC.UserClaim,
					SubClaim:    provider.OIDC.SubClaim,
					GroupsClaim: provider.OIDC.GroupsClaim,
					JWKSURL:     provider.OIDC.JWKSURL,
				}
			}
			providers = append(providers, translated)
		}
		mcpCfg.Auth = &mcpserver.AuthConfig{
			Providers:           providers,
			ResourceMetadataURL: cfg.Auth.ResourceMetadataURL,
			RequiredScopes:      cfg.Auth.RequiredScopes,
		}
	}
	if cfg.Authz != nil {
		mcpCfg.Authorization = cfg.Authz
	}
	if cfg.Approval != nil {
		mcpCfg.Approval = &mcpserver.ApprovalConfig{Timeout: cfg.Approval.Timeout}
		if cfg.Approval.Service != nil {
			service := cfg.Approval.Service
			mcpCfg.Approval.Service = &mcpserver.ApprovalServiceConfig{
				URL:                  service.URL,
				ServerName:           service.ServerName,
				ServerToken:          service.ServerToken,
				ReceiptSecret:        service.ReceiptSecret,
				ReceiptPrivateKeyPEM: service.ReceiptPrivateKeyPEM,
				ReceiptKeyID:         service.ReceiptKeyID,
				ReceiptAlgorithm:     service.ReceiptAlgorithm,
				PollInterval:         service.PollInterval,
				RequestTimeout:       service.RequestTimeout,
				TTL:                  service.TTL,
			}
		}
	}
	return mcpCfg
}
