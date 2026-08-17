package grafana

import (
	"context"
	"net/http"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

// ─── baseTool ────────────────────────────────────────────────────────────

// Grafana tool names, shared across constructors, registry and check.
const (
	dashboardWriteToolName = "grafana_dashboard_write"
)

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

// fetchDashboard fetches an existing dashboard by UID. Callers distinguish a
// "dashboard does not exist" (HTTP 404) from other failures via isHTTPStatus.
func (b *baseTool) fetchDashboard(ctx context.Context, instance, uid string) (dashboardResponse, error) {
	client, err := b.client(instance)
	if err != nil {
		return dashboardResponse{}, err
	}

	body, err := client.GetDashboard(ctx, uid)
	if err != nil {
		return dashboardResponse{}, err
	}

	var dr dashboardResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return dashboardResponse{}, errors.Wrap(err, "failed to unmarshal dashboard for protection check")
	}

	return dr, nil
}

// checkProtected fetches the existing dashboard by UID (if non-empty) and
// returns an error if it is protected. Used by the write tool before saving or
// deleting. If uid is empty or the dashboard does not exist (404), returns nil
// (no protection).
func (b *baseTool) checkProtected(ctx context.Context, instance, uid string) error {
	if uid == "" {
		return nil
	}

	dr, err := b.fetchDashboard(ctx, instance, uid)
	if err != nil {
		// A 404 means the dashboard does not exist yet; treat as not protected.
		if isHTTPStatus(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	return b.checkProtectedDashboard(instance, uid, dr)
}

// checkProtectedDashboard evaluates an already-fetched dashboard against the
// instance's protection blocklist and returns an error if it is protected.
func (b *baseTool) checkProtectedDashboard(instance, uid string, dr dashboardResponse) error {
	title := dashboardTitle(dr.Dashboard)
	if b.protection(instance).isProtected(uid, title, dr.Meta.FolderUID, dashboardTags(dr.Dashboard)) {
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
	title := dashboardTitle(model)

	if prot.isProtected(uid, title, folderUID, dashboardTags(model)) {
		return errors.Errorf("dashboard %q matches the protected blocklist and cannot be created or modified", title)
	}

	return nil
}

// dashboardTitle returns the "title" field of a dashboard model as a string,
// or "" when the field is absent or not a string.
func dashboardTitle(model map[string]any) string {
	title, _ := model["title"].(string)
	return title
}

// dashboardTags returns the "tags" field of a dashboard model as a []string,
// ignoring non-string entries. It returns nil when the field is absent.
func dashboardTags(model map[string]any) []string {
	rawTags, ok := model["tags"].([]any)
	if !ok {
		return nil
	}
	tags := make([]string, 0, len(rawTags))
	for _, t := range rawTags {
		if s, ok := t.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
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
