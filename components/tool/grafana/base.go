package grafana

import (
	"context"
	"net/http"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// ─── baseTool ────────────────────────────────────────────────────────────

// baseTool holds shared state for all Grafana tools that require API clients.
type baseTool struct {
	clients        map[string]*grafanaClient
	configs        Configs
	knownInstances []string
	protected      map[string]*dashboardProtection
}

// newBaseTool builds Grafana clients for all configured instances and returns
// a baseTool ready to be embedded by individual tools.
func newBaseTool(ctx context.Context, configs Configs) (*baseTool, error) {
	if len(configs) == 0 {
		return nil, errors.Errorf("at least one Grafana instance configuration is required")
	}

	clients, err := BuildClients(ctx, configs)
	if err != nil {
		return nil, err
	}

	protected := make(map[string]*dashboardProtection)
	for name, cfg := range configs {
		protected[name] = buildProtection(cfg.ProtectedDashboards)
	}

	return &baseTool{
		clients:        clients,
		configs:        configs,
		knownInstances: configs.GetInstanceNames(),
		protected:      protected,
	}, nil
}

// client returns the grafanaClient for the given instance, or a NotFoundError.
func (b *baseTool) client(instance string) (*grafanaClient, error) {
	c, ok := b.clients[instance]
	if !ok {
		return nil, instanceNotFoundError(instance, b.knownInstances)
	}
	return c, nil
}

// protection returns the compiled dashboardProtection for the instance (may be nil).
func (b *baseTool) protection(instance string) *dashboardProtection {
	return b.protected[instance]
}

// checkProtected fetches the existing dashboard by UID (if non-empty) and
// returns an error if it is protected. Used by the build tool before saving.
// If uid is empty or the dashboard does not exist (404), returns nil (no protection).
func (b *baseTool) checkProtected(ctx context.Context, instance, uid string) error {
	if uid == "" {
		return nil
	}

	client, err := b.client(instance)
	if err != nil {
		return err
	}

	body, err := client.GetDashboard(ctx, uid)
	if err != nil {
		// A 404 means the dashboard does not exist yet; treat as not protected.
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	var dr dashboardResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return errors.Wrap(err, "failed to unmarshal dashboard for protection check")
	}

	title := ""
	if dr.Dashboard != nil {
		if t, ok := dr.Dashboard["title"].(string); ok {
			title = t
		}
	}

	folderUID := dr.Meta.FolderUID

	var tags []string
	if dr.Dashboard != nil {
		if rawTags, ok := dr.Dashboard["tags"].([]any); ok {
			for _, t := range rawTags {
				if s, ok := t.(string); ok {
					tags = append(tags, s)
				}
			}
		}
	}

	prot := b.protection(instance)
	if prot.isProtected(uid, title, folderUID, tags) {
		return errors.Errorf("dashboard %q (UID %s) is protected and cannot be modified", title, uid)
	}

	return nil
}

// checkProtectedModel evaluates the NEW dashboard model (and target folder)
// against the instance's protection blocklist. This is defense-in-depth: it
// prevents creating a new dashboard — or renaming an existing one — so that
// it matches protected criteria (title prefix, folder, or tag), which would
// otherwise bypass checkProtected (which only inspects the EXISTING dashboard).
// Returns nil if no protection is configured or the model does not match.
func (b *baseTool) checkProtectedModel(instance string, model map[string]any, folderUID string) error {
	prot := b.protection(instance)
	if prot == nil {
		return nil
	}

	uid, _ := model["uid"].(string)
	title, _ := model["title"].(string)

	var tags []string
	if rawTags, ok := model["tags"].([]any); ok {
		for _, t := range rawTags {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	if prot.isProtected(uid, title, folderUID, tags) {
		return errors.Errorf("dashboard %q matches the protected blocklist and cannot be created or modified", title)
	}

	return nil
}

// ─── dashboardProtection ─────────────────────────────────────────────────

// dashboardProtection is the compiled, lookup-optimized form of
// ProtectedDashboardsConfig for a single instance.
type dashboardProtection struct {
	uidSet        map[string]bool
	titlePrefixes []string
	folderSet     map[string]bool
	tagSet        map[string]bool
}

// isProtected reports whether the given dashboard attributes match any
// protection criterion. Returns true if ANY criterion matches.
func (p *dashboardProtection) isProtected(uid, title, folderUID string, tags []string) bool {
	if p == nil {
		return false
	}

	if uid != "" && p.uidSet[uid] {
		return true
	}

	for _, prefix := range p.titlePrefixes {
		if strings.HasPrefix(title, prefix) {
			return true
		}
	}

	if folderUID != "" && p.folderSet[folderUID] {
		return true
	}

	for _, tag := range tags {
		if p.tagSet[tag] {
			return true
		}
	}

	return false
}

// buildProtection compiles a ProtectedDashboardsConfig into a dashboardProtection.
// Returns nil if all criteria are empty (no protection configured).
func buildProtection(cfg ProtectedDashboardsConfig) *dashboardProtection {
	p := &dashboardProtection{}

	if len(cfg.UIDs) > 0 {
		p.uidSet = make(map[string]bool)
		for _, u := range cfg.UIDs {
			p.uidSet[u] = true
		}
	}
	if len(cfg.TitlePrefixes) > 0 {
		p.titlePrefixes = append([]string{}, cfg.TitlePrefixes...)
	}
	if len(cfg.Folders) > 0 {
		p.folderSet = make(map[string]bool)
		for _, f := range cfg.Folders {
			p.folderSet[f] = true
		}
	}
	if len(cfg.Tags) > 0 {
		p.tagSet = make(map[string]bool)
		for _, t := range cfg.Tags {
			p.tagSet[t] = true
		}
	}

	if len(p.uidSet) == 0 && len(p.titlePrefixes) == 0 && len(p.folderSet) == 0 && len(p.tagSet) == 0 {
		return nil
	}

	return p
}
