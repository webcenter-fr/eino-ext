package github

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/go-github/v71/github"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const webhookUpsertDescription = `
** General Purpose **
It creates or updates a repository webhook.

** Output **
It returns the created or updated webhook details.

** Security **
- Webhook URLs must use HTTPS.
- Internal/loopback addresses are blocked (SSRF protection).
- Secrets are redacted from output.
`

// WebhookUpsertParams defines the parameters for upserting a webhook.
type WebhookUpsertParams struct {
	Instance    string   `json:"instance" validate:"required" jsonschema:"(required) The GitHub instance to connect to."`
	Owner       string   `json:"owner" validate:"required" jsonschema:"(required) Repository owner."`
	Repo        string   `json:"repo" validate:"required" jsonschema:"(required) Repository name."`
	HookURL     string   `json:"hookUrl" validate:"required,url" jsonschema:"(required) Webhook payload URL."`
	Secret      string   `json:"secret,omitempty" jsonschema:"(optional) Webhook secret."`
	ContentType string   `json:"contentType,omitempty" jsonschema:"(optional) Content type: json or form. Defaults to json."`
	Events      []string `json:"events,omitempty" jsonschema:"(optional) Event names. Defaults to push."`
	Active      bool     `json:"active,omitempty" jsonschema:"(optional) Whether the webhook is active. Defaults to true."`
	HookID      int64    `json:"hookId,omitempty" jsonschema:"(optional) Existing hook ID to update. If not provided, creates a new hook."`
	DryRun      bool     `json:"dryRun,omitempty" jsonschema:"(optional) If true, simulate the operation without making changes."`
	Confirmed   bool     `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually execute. Set this after the user has approved the dry-run result."`
}

// WebhookUpsertTool is an eino tool for upserting GitHub webhooks.
type WebhookUpsertTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke creates or updates a GitHub webhook.
func (t *WebhookUpsertTool) Invoke(ctx context.Context, params *WebhookUpsertParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if err := validateWebhookURL(params.HookURL); err != nil {
		return "", err
	}

	if params.DryRun {
		return fmt.Sprintf(`{"dryRun": true, "wouldCreate": {"url": %q, "events": %v, "active": %v}}`, params.HookURL, params.Events, params.Active), nil
	}

	if err := confirm.RequireConfirmationForAction("create/update webhook", params.Confirmed); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	events := params.Events
	if len(events) == 0 {
		events = []string{"push"}
	}

	contentType := params.ContentType
	if contentType == "" {
		contentType = "json"
	}

	hook := &github.Hook{
		Config: &github.HookConfig{
			URL:         github.Ptr(params.HookURL),
			ContentType: github.Ptr(contentType),
			Secret:      stringPtr(params.Secret),
		},
		Events: events,
		Active: &params.Active,
	}

	if params.HookID != 0 {
		updated, _, err := c.Repositories.EditHook(ctx, params.Owner, params.Repo, params.HookID, hook)
		if err != nil {
			return "", errors.Wrap(err, "failed to update webhook")
		}
		params.Secret = ""
		return fmt.Sprintf(`{"updated": true, "hook": {"id": %d, "url": %q, "events": %v, "active": %v}}`,
			updated.GetID(), params.HookURL, updated.Events, updated.GetActive()), nil
	}

	created, _, err := c.Repositories.CreateHook(ctx, params.Owner, params.Repo, hook)
	if err != nil {
		return "", errors.Wrap(err, "failed to create webhook")
	}
	params.Secret = ""
	return fmt.Sprintf(`{"created": true, "hook": {"id": %d, "url": %q, "events": %v, "active": %v}}`,
		created.GetID(), params.HookURL, created.Events, created.GetActive()), nil
}

func validateWebhookURL(rawURL string) error {
	if !strings.HasPrefix(rawURL, "https://") {
		return errors.Errorf("webhook URL must use HTTPS (got %q)", rawURL)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.Wrap(err, "invalid webhook URL")
	}

	host := u.Hostname()

	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" {
		return errors.Errorf("webhook URL must not point to localhost: %s", host)
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return errors.Errorf("webhook URL must not point to private/loopback address: %s", host)
		}
		ip4 := ip.To4()
		if ip4 != nil && ip4[0] == 169 && ip4[1] == 254 {
			return errors.Errorf("webhook URL must not point to link-local/metadata address: %s", host)
		}
	}

	ips, lookupErr := net.LookupIP(host)
	if lookupErr != nil {
		// DNS resolution failed: the hostname is unresolvable and
		// therefore harmless (no one can deliver webhooks to it).
		return nil
	}
	for _, resolvedIP := range ips {
		if resolvedIP.IsLoopback() || resolvedIP.IsPrivate() || resolvedIP.IsLinkLocalUnicast() || resolvedIP.IsLinkLocalMulticast() || resolvedIP.IsUnspecified() {
			return errors.Errorf("webhook URL must not point to private/loopback address: %s (resolved from %s)", resolvedIP.String(), host)
		}
		ip4 := resolvedIP.To4()
		if ip4 != nil && ip4[0] == 169 && ip4[1] == 254 {
			return errors.Errorf("webhook URL must not point to link-local/metadata address: %s (resolved from %s)", resolvedIP.String(), host)
		}
	}

	return nil
}

// NewWebhookUpsertTool creates a new WebhookUpsertTool.
func NewWebhookUpsertTool(ctx context.Context, configs Configs) (*WebhookUpsertTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}
	return newWebhookUpsertTool(ctx, base)
}

func newWebhookUpsertTool(ctx context.Context, base *baseTool) (*WebhookUpsertTool, error) {
	webhookTool := &WebhookUpsertTool{baseTool: base}
	t, err := utils.InferTool("github_webhook_upsert", webhookUpsertDescription, webhookTool.Invoke, utils.WithSchemaModifier(base.instanceSchemaModifier()))
	if err != nil {
		return nil, err
	}
	webhookTool.InvokableTool = t

	return webhookTool, nil
}
