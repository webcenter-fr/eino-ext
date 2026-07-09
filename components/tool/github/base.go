package github

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// systemDirs contains paths that must not be used as clone directories.
var systemDirs = map[string]bool{
	"/":     true,
	"/etc":  true,
	"/bin":  true,
	"/usr":  true,
	"/var":  true,
	"/proc": true,
	"/sys":  true,
	"/dev":  true,
	"/tmp":  true,
}

func validateCloneDir(cloneDir string) error {
	if cloneDir == "" {
		return errors.New("CloneDir is required")
	}
	if !filepath.IsAbs(cloneDir) {
		return errors.Errorf("CloneDir must be an absolute path, got %q", cloneDir)
	}
	cleaned := filepath.Clean(cloneDir)
	if systemDirs[cleaned] {
		return errors.Errorf("CloneDir must not be a system directory, got %q", cleaned)
	}
	if strings.HasPrefix(cleaned, "/proc/") || strings.HasPrefix(cleaned, "/sys/") || strings.HasPrefix(cleaned, "/dev/") {
		return errors.Errorf("CloneDir must not be under a system mount, got %q", cleaned)
	}
	return nil
}

// baseTool holds shared state for all GitHub tools.
type baseTool struct {
	clients        map[string]*github.Client
	knownInstances []string
	cloneDir       string
	tokens         map[string]string
	baseURLs       map[string]string
}

// client returns the GitHub API client for the given instance name, or an error
// if the instance is not found among the known instances.
func (b *baseTool) client(instance string) (*github.Client, error) {
	c, ok := b.clients[instance]
	if !ok {
		return nil, instanceNotFoundError(instance, b.knownInstances)
	}
	return c, nil
}

// token returns the token for the given instance name.
func (b *baseTool) token(instance string) (string, error) {
	t, ok := b.tokens[instance]
	if !ok {
		return "", instanceNotFoundError(instance, b.knownInstances)
	}
	return t, nil
}

// newBaseTool builds GitHub clients for all configured instances and returns a
// baseTool ready to be embedded by individual tools.
func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error) {
	if len(configs) == 0 {
		return nil, errors.Errorf("at least one GitHub instance configuration is required")
	}

	cloneDir := ""
	tokens := make(map[string]string)
	baseURLs := make(map[string]string)
	for name, cfg := range configs {
		if cloneDir == "" {
			cloneDir = cfg.CloneDir
		}
		if cfg.CloneDir != "" && cfg.CloneDir != cloneDir {
			return nil, errors.Errorf("all instances must share the same CloneDir (got %q and %q)", cloneDir, cfg.CloneDir)
		}
		tokens[name] = cfg.Token
		baseURLs[name] = cfg.BaseURL
	}

	if err := validateCloneDir(cloneDir); err != nil {
		return nil, err
	}

	clients, err := BuildClients(ctx, configs)
	if err != nil {
		return nil, err
	}

	return &baseTool{
		clients:        clients,
		knownInstances: configs.GetInstanceNames(),
		cloneDir:       cloneDir,
		tokens:         tokens,
		baseURLs:       baseURLs,
	}, nil
}

// validateParams validates a struct using the shared validator instance.
func validateParams(v any) error {
	return validate.Struct(v)
}

// instanceNotFoundError returns an error indicating the requested instance is unknown.
func instanceNotFoundError(instance string, known []string) error {
	return toolutil.NotFoundError("GitHub instance", instance, known)
}

// gitHost extracts the host portion from a GHES BaseURL, falling back to github.com.
func (b *baseTool) gitHost(instance string) (string, error) {
	baseURL, ok := b.baseURLs[instance]
	if !ok {
		return "", instanceNotFoundError(instance, b.knownInstances)
	}
	if baseURL == "" {
		return "github.com", nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "invalid base URL for git host")
	}
	return u.Host, nil
}
