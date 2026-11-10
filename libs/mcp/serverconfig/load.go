package serverconfig

import (
	"strings"

	"emperror.dev/errors"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/secretref"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Default values applied before validation.
const (
	DefaultTransport = "stdio"
	DefaultAddr      = ":8080"
	DefaultOpsAddr   = ":9090"
	DefaultLogLevel  = "info"
	DefaultLogFormat = "text"
	DefaultTimeout   = "30s"
)

// DefaultDisallowedNamespaces are the namespaces the Kubernetes tools refuse
// to touch unless the configuration overrides them.
var DefaultDisallowedNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}

// Load reads the YAML configuration at path, applies environment variables and
// CLI overrides, resolves secret references, and validates the result.
//
// Precedence is flags > environment > file > defaults. An empty path means
// "no config file"; a non-empty path that cannot be read is an error.
func Load(path string, overrides map[string]any) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("EINO_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, key := range []string{
		"server", "transport", "addr", "opsAddr",
		"log.level", "log.format",
		"approval.timeout",
		"approval.service.url", "approval.service.serverName",
		"approval.service.receiptAlgorithm", "approval.service.pollInterval",
		"approval.service.requestTimeout", "approval.service.ttl",
		"auth.resourceMetadataURL",
	} {
		_ = v.BindEnv(key)
	}

	setDefaults(v)

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, errors.Wrapf(err, "failed to read MCP config file %q", path)
		}
	}

	for key, value := range overrides {
		v.Set(key, value)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg, func(dc *mapstructure.DecoderConfig) {
		dc.ErrorUnused = true
	}); err != nil {
		return nil, errors.Wrap(err, "failed to decode MCP config")
	}

	applyDefaults(cfg)
	if err := resolveSecrets(cfg); err != nil {
		return nil, err
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	if err := validateFamily(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateFamily rejects a selected family that is absent or empty. Viper
// drops empty maps, so this is checked on the decoded config rather than with
// a struct tag.
func validateFamily(cfg *Config) error {
	switch cfg.Server {
	case "kubernetes":
		if cfg.Kubernetes == nil || len(cfg.Kubernetes.Clusters) == 0 {
			return errors.New("serverconfig: kubernetes family requires at least one cluster")
		}
	case "argocd":
		if cfg.ArgoCD == nil || len(cfg.ArgoCD.Instances) == 0 {
			return errors.New("serverconfig: argocd family requires at least one instance")
		}
	case "prometheus":
		if cfg.Prometheus == nil || len(cfg.Prometheus.Instances) == 0 {
			return errors.New("serverconfig: prometheus family requires at least one instance")
		}
	case "grafana":
		if cfg.Grafana == nil || len(cfg.Grafana.Instances) == 0 {
			return errors.New("serverconfig: grafana family requires at least one instance")
		}
	}
	return nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("transport", DefaultTransport)
	v.SetDefault("addr", DefaultAddr)
	v.SetDefault("opsAddr", DefaultOpsAddr)
	v.SetDefault("log.level", DefaultLogLevel)
	v.SetDefault("log.format", DefaultLogFormat)
}

func applyDefaults(cfg *Config) {
	if cfg.Transport == "" {
		cfg.Transport = DefaultTransport
	}
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.OpsAddr == "" {
		cfg.OpsAddr = DefaultOpsAddr
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = DefaultLogLevel
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = DefaultLogFormat
	}
	if cfg.Kubernetes != nil {
		for name, cluster := range cfg.Kubernetes.Clusters {
			if len(cluster.DisallowedNamespaces) == 0 {
				cluster.DisallowedNamespaces = append([]string(nil), DefaultDisallowedNamespaces...)
			}
			if cluster.DefaultTimeout == "" {
				cluster.DefaultTimeout = DefaultTimeout
			}
			cfg.Kubernetes.Clusters[name] = cluster
		}
	}
	for _, family := range []*InstanceFamily{cfg.ArgoCD, cfg.Prometheus, cfg.Grafana} {
		if family == nil {
			continue
		}
		for name, instance := range family.Instances {
			if instance.DefaultTimeout == "" {
				instance.DefaultTimeout = DefaultTimeout
			}
			family.Instances[name] = instance
		}
	}
}

func resolveSecrets(cfg *Config) error {
	for _, family := range []*InstanceFamily{cfg.ArgoCD, cfg.Prometheus, cfg.Grafana} {
		if family == nil {
			continue
		}
		for name, instance := range family.Instances {
			token, err := secretref.Resolve(instance.TokenFile, instance.TokenEnv, "", "instance "+name+" token")
			if err != nil {
				return err
			}
			instance.Token = token
			family.Instances[name] = instance
		}
	}

	if cfg.Auth != nil {
		for i := range cfg.Auth.Providers {
			provider := &cfg.Auth.Providers[i]
			if provider.Local == nil {
				continue
			}
			for j := range provider.Local.Tokens {
				token := &provider.Local.Tokens[j]
				value, err := secretref.Resolve(token.TokenFile, token.TokenEnv, "", "local auth token")
				if err != nil {
					return err
				}
				token.Token = value
			}
		}
	}

	if cfg.Approval != nil && cfg.Approval.Service != nil {
		service := cfg.Approval.Service
		serverToken, err := secretref.Resolve(service.ServerTokenFile, service.ServerTokenEnv, "", "approval server token")
		if err != nil {
			return err
		}
		service.ServerToken = serverToken

		receiptSecret, err := secretref.Resolve(service.ReceiptSecretFile, service.ReceiptSecretEnv, "", "approval receipt secret")
		if err != nil {
			return err
		}
		service.ReceiptSecret = receiptSecret

		privateKey, err := secretref.Resolve(service.ReceiptPrivateKeyFile, service.ReceiptPrivateKeyEnv, "", "approval receipt private key")
		if err != nil {
			return err
		}
		service.ReceiptPrivateKeyPEM = privateKey
	}
	return nil
}
