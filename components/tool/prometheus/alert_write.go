package prometheus

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/confirm"
)

const alertWriteToolDescription = `
** General Purpose **
A single tool that creates, updates, or deletes (resolves) an alert on the
Alertmanager associated with a Prometheus instance. The required 'operation'
param selects the action:

- create: POST a new firing alert (endsAt must be in the future).
- update: Fetch an existing alert by labels, merge the provided fields, and
  re-POST (upsert). Errors if no existing alert matches.
- delete: Re-POST the alert with endsAt <= now to resolve it (Alertmanager has
  no DELETE /alerts). Idempotent.

** Safety **
This is a write tool. Always use dryRun=true first to preview the resolved
postableAlert payload (and, for update, the existing alert). After reviewing,
set confirmed=true to perform the POST. Neither dryRun nor confirmed returns
an error.

** Required labels **
All operations require a 'labels' map that includes the 'alertname' label.
`

// AlertWriteParams defines the parameters for creating, updating, or
// deleting an Alertmanager alert. The 'operation' field selects which action is
// performed; fields apply to operations as documented below.
type AlertWriteParams struct {
	Instance    string            `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance (must have Alertmanager configured)."`
	Operation   string            `json:"operation" validate:"required,oneof=create update delete" jsonschema:"(required) Operation to perform: 'create', 'update', or 'delete'."`
	Labels      map[string]string `json:"labels" validate:"required,min=1,max=64,dive,keys,required,endkeys,required" jsonschema:"(required) Alert labels as key/value pairs. Must include 'alertname'. For create/delete these are the alert's labels; for update these identify the existing alert to modify."`
	Annotations map[string]string `json:"annotations,omitempty" validate:"omitempty,max=64,dive,keys,required,endkeys,required" jsonschema:"(optional) Alert annotations. Used by create and update. For update, omit to keep existing annotations; set to {} or a new map to replace them."`
	StartsAt    string            `json:"startsAt,omitempty" jsonschema:"(optional) Start time in RFC3339. create: defaults to now. update: omit to keep existing. delete: ignored (resolved alert uses now-1m)."`
	EndsAt      string            `json:"endsAt,omitempty" jsonschema:"(optional) End time in RFC3339. create: defaults to now+5m, must be in the future. update: omit to keep existing. delete: ignored (resolved alert uses now)."`
	// The validate:"omitempty,url" tag is only a first-pass syntactic check; the
	// authoritative http/https scheme enforcement is the code-level check below.
	GeneratorURL string `json:"generatorURL,omitempty" validate:"omitempty,url" jsonschema:"(optional) URL of the source that generated the alert. create: sets it. update: omit to keep existing. delete: ignored."`
	DryRun       bool   `json:"dryRun,omitempty" jsonschema:"(optional) If true, preview the resolved postableAlert payload without posting."`
	Confirmed    bool   `json:"confirmed,omitempty" jsonschema:"(optional) Must be true to actually post. Set after reviewing the dry-run."`
}

// AlertWriteOutput is the structured output for an Alertmanager
// alert write operation.
type AlertWriteOutput struct {
	Status      string `json:"status"`
	Action      string `json:"action"`                // "created" | "updated" | "deleted"
	Fingerprint string `json:"fingerprint,omitempty"` // set for update (existing alert's fingerprint)
	EndsAt      string `json:"endsAt,omitempty"`      // set for delete (the resolve endsAt, RFC3339)
}

// AlertWriteTool is an eino tool for writing Alertmanager alerts.
type AlertWriteTool struct {
	*alertmanagerBaseTool
	tool.InvokableTool
}

// buildMatcherFilter builds Alertmanager matchers from labels. Each label is
// emitted as its own matcher element: the Alertmanager `filter` query param
// accepts exactly one matcher per value and ANDs repeated params, so a
// comma-joined string would be rejected as "two or more matchers". Values
// containing `"` or `\` are escaped so they cannot break out of the matcher
// syntax.
//
// Label KEYS are not escaped because they appear as bare identifiers in the
// matcher grammar (e.g. `alertname="X"`); escaping is not meaningful there.
// Instead, keys are validated by validateMatcherLabelKeys before this
// function is called, ensuring they match the Prometheus legacy label-name
// regex and therefore cannot contain matcher metacharacters (=, !, ~, ", \,
// whitespace) that could break the matcher or alter its semantics.
func buildMatcherFilter(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	matchers := make([]string, 0, len(keys))
	for _, k := range keys {
		esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(labels[k])
		matchers = append(matchers, fmt.Sprintf(`%s="%s"`, k, esc))
	}
	return matchers
}

// validateMatcherLabelKeys checks that all label keys are valid Prometheus
// legacy label names (^[a-zA-Z_][a-zA-Z0-9_]*$). This is required for the
// update operation, where keys are used as bare identifiers in Alertmanager
// matcher strings. A key containing matcher metacharacters (=, !, ~, ", \,
// whitespace) could break the matcher syntax or cause unexpected matching
// behavior (CWE-74). The legacy regex is used (rather than the UTF-8 scheme)
// because the Alertmanager v2 filter parser expects identifier-style label
// names.
func validateMatcherLabelKeys(labels map[string]string) error {
	for k := range labels {
		if !model.LabelNameRE.MatchString(k) {
			return errors.Errorf("invalid label name %q: must match %s", k, model.LabelNameRE)
		}
	}
	return nil
}

// coalesceTime returns the RFC3339 timestamp in provided when non-empty,
// otherwise the existing/default value. It is shared by create (defaults
// now / now+5m) and update (keep existing).
func coalesceTime(existing time.Time, provided string) (time.Time, error) {
	if provided == "" {
		return existing, nil
	}
	t, err := parseRFC3339(provided)
	if err != nil {
		return time.Time{}, errors.Wrapf(err, "invalid startsAt/endsAt, expected RFC3339")
	}
	return t, nil
}

// validateGeneratorURL enforces that a non-empty generatorURL is an http/https
// URL. This is the authoritative scheme check; the validate:"omitempty,url" tag
// only performs a first-pass syntactic check.
func validateGeneratorURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.Errorf("generatorURL must be an http/https URL")
	}
	return nil
}

// postAlert resolves the client for instance and POSTs a single alert.
func (t *AlertWriteTool) postAlert(ctx context.Context, instance string, alert postableAlert) error {
	c, err := t.amClient(instance)
	if err != nil {
		return err
	}
	return c.PostAlerts(ctx, []postableAlert{alert})
}

// Invoke executes the AlertWriteTool with the given parameters.
func (t *AlertWriteTool) Invoke(ctx context.Context, params *AlertWriteParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	if err := confirm.RequireConfirmation(params.DryRun, params.Confirmed); err != nil {
		return "", err
	}

	if params.Labels["alertname"] == "" {
		return "", errors.Errorf("labels must include 'alertname'")
	}

	labelSet := toLabelSet(params.Labels)

	switch params.Operation {
	case "create":
		if err := validateGeneratorURL(params.GeneratorURL); err != nil {
			return "", err
		}

		now := time.Now().UTC()

		startsAt, err := coalesceTime(now, params.StartsAt)
		if err != nil {
			return "", err
		}
		endsAt, err := coalesceTime(now.Add(5*time.Minute), params.EndsAt)
		if err != nil {
			return "", err
		}

		if !endsAt.After(now) {
			return "", errors.Errorf("endsAt must be in the future for a firing alert")
		}
		if !endsAt.After(startsAt) {
			return "", errors.Errorf("endsAt must be after startsAt")
		}

		alert := postableAlert{
			Labels:       labelSet,
			Annotations:  toLabelSet(params.Annotations),
			StartsAt:     &startsAt,
			EndsAt:       &endsAt,
			GeneratorURL: params.GeneratorURL,
		}

		if params.DryRun {
			return marshalString(map[string]any{
				"dryRun":    true,
				"operation": "create",
				"alert":     alert,
			}), nil
		}

		if err := t.postAlert(ctx, params.Instance, alert); err != nil {
			return "", err
		}

		return marshalString(AlertWriteOutput{
			Status: "success",
			Action: "created",
		}), nil

	case "update":
		if err := validateGeneratorURL(params.GeneratorURL); err != nil {
			return "", err
		}
		if err := validateMatcherLabelKeys(params.Labels); err != nil {
			return "", err
		}
		filter := buildMatcherFilter(params.Labels)

		c, err := t.amClient(params.Instance)
		if err != nil {
			return "", err
		}

		matches, err := c.ListAlerts(ctx, &amListAlertsParams{
			Active:    boolPtr(true),
			Silenced:  boolPtr(true),
			Inhibited: boolPtr(true),
			Filter:    filter,
		})
		if err != nil {
			return "", err
		}

		if len(matches) == 0 {
			return "", errors.Errorf("no existing alert matches labels %v", params.Labels)
		}

		existing := matches[0]

		mergedAnnotations := existing.Annotations
		if params.Annotations != nil {
			mergedAnnotations = toLabelSet(params.Annotations)
		}

		mergedStartsAt, err := coalesceTime(existing.StartsAt, params.StartsAt)
		if err != nil {
			return "", err
		}
		mergedEndsAt, err := coalesceTime(existing.EndsAt, params.EndsAt)
		if err != nil {
			return "", err
		}

		mergedGeneratorURL := existing.GeneratorURL
		if params.GeneratorURL != "" {
			mergedGeneratorURL = params.GeneratorURL
		}

		if !mergedEndsAt.After(mergedStartsAt) {
			return "", errors.Errorf("endsAt must be after startsAt")
		}

		merged := postableAlert{
			Labels:       existing.Labels,
			Annotations:  mergedAnnotations,
			StartsAt:     &mergedStartsAt,
			EndsAt:       &mergedEndsAt,
			GeneratorURL: mergedGeneratorURL,
		}

		if params.DryRun {
			return marshalString(map[string]any{
				"dryRun":    true,
				"operation": "update",
				"existing":  existing,
				"merged":    merged,
			}), nil
		}

		if err := c.PostAlerts(ctx, []postableAlert{merged}); err != nil {
			return "", err
		}

		return marshalString(AlertWriteOutput{
			Status:      "success",
			Action:      "updated",
			Fingerprint: existing.Fingerprint,
		}), nil

	case "delete":
		now := time.Now().UTC()
		startsAt := now.Add(-time.Minute)

		resolve := postableAlert{
			Labels:   labelSet,
			StartsAt: &startsAt,
			EndsAt:   &now,
		}

		if params.DryRun {
			return marshalString(map[string]any{
				"dryRun":    true,
				"operation": "delete",
				"resolve":   resolve,
			}), nil
		}

		if err := t.postAlert(ctx, params.Instance, resolve); err != nil {
			return "", err
		}

		return marshalString(AlertWriteOutput{
			Status: "success",
			Action: "deleted",
			EndsAt: now.Format(time.RFC3339),
		}), nil

	default:
		return "", errors.Errorf("unsupported operation %q", params.Operation)
	}
}

// NewAlertWriteTool creates a new AlertWriteTool.
func NewAlertWriteTool(ctx context.Context, configs Configs) (*AlertWriteTool, error) {
	base, err := newAlertmanagerBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	writeTool := &AlertWriteTool{alertmanagerBaseTool: base}
	t, err := utils.InferTool(alertWriteToolName, alertWriteToolDescription, writeTool.Invoke)
	if err != nil {
		return nil, err
	}
	writeTool.InvokableTool = t

	return writeTool, nil
}
