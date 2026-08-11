package prometheus

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
	"github.com/prometheus/common/model"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

const targetListDescription = `
** General Purpose **
It lists all active scrape targets and their health status from a Prometheus instance.
This is useful to verify that metrics are being scraped correctly (e.g. no network policy issues,
misconfigured scrape jobs, or unreachable exporters).

** Output **
It returns a JSON array of objects, where each object represents a single active target with the following fields:
- labels: the target labels (e.g. instance, job).
- scrapePool: the scrape pool name (typically "<job_name>/<instance>").
- scrapeUrl: the URL being scraped.
- health: the target health: 'up', 'down', or 'unknown'.
- lastError: the last scrape error message (empty when healthy).
- lastScrape: the timestamp of the last scrape in RFC3339 format.
- lastScrapeDuration: the duration of the last scrape as a Go duration string (e.g. '12.3ms').
`

// TargetListParams defines the parameters for listing Prometheus scrape targets.
type TargetListParams struct {
	Instance   string `json:"instance" validate:"required" jsonschema:"(required) The Prometheus instance to query."`
	Filter     string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each target JSON. Keep only targets that match. RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or backreferences — such patterns return an error. Invalid regex returns an error."`
	Health     string `json:"health,omitempty" validate:"omitempty,oneof=up down unknown" jsonschema:"(optional) Filter by target health: 'up', 'down', or 'unknown'."`
	ScrapePool string `json:"scrapePool,omitempty" jsonschema:"(optional) Filter by scrape pool name. Must match exactly (e.g. 'node-exporter/10.0.0.1:9100')."`
}

// TargetListOutput is the structured output for a Prometheus target list.
type TargetListOutput struct {
	Labels             model.LabelSet `json:"labels"`
	ScrapePool         string         `json:"scrapePool"`
	ScrapeUrl          string         `json:"scrapeUrl"`
	Health             string         `json:"health"`
	LastError          string         `json:"lastError"`
	LastScrape         string         `json:"lastScrape"`
	LastScrapeDuration string         `json:"lastScrapeDuration"`
}

// TargetListTool is an eino tool for listing Prometheus scrape targets.
type TargetListTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke returns matching active scrape targets as JSON.
func (t *TargetListTool) Invoke(ctx context.Context, params *TargetListParams) (result string, err error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	targetsResult, err := c.Targets(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list targets")
	}

	active := targetsResult.Active
	outputs := make([]json.RawMessage, 0, len(active))
	for _, tgt := range active {
		// Filter by health if specified
		if params.Health != "" && string(tgt.Health) != params.Health {
			continue
		}
		// Filter by scrapePool if specified (exact match)
		if params.ScrapePool != "" && tgt.ScrapePool != params.ScrapePool {
			continue
		}

		output := TargetListOutput{
			Labels:             tgt.Labels,
			ScrapePool:         tgt.ScrapePool,
			ScrapeUrl:          redactScrapeURL(tgt.ScrapeURL),
			Health:             string(tgt.Health),
			LastError:          tgt.LastError,
			LastScrape:         tgt.LastScrape.Format("2006-01-02T15:04:05Z"),
			LastScrapeDuration: time.Duration(tgt.LastScrapeDuration * float64(time.Second)).String(),
		}

		outputJSON := json.RawMessage(marshal.MustMarshal(output))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	return marshalOutputs(outputs)
}

// NewTargetListTool creates a new TargetListTool.
func NewTargetListTool(ctx context.Context, configs Configs) (*TargetListTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	listTool := &TargetListTool{baseTool: base}
	t, err := utils.InferTool("prometheus_target_list", fmt.Sprintf("%s\n%s", targetListDescription, listOutputGuidance), listTool.Invoke)
	if err != nil {
		return nil, err
	}
	listTool.InvokableTool = t

	return listTool, nil
}

// redactScrapeURL strips embedded userinfo (HTTP basic-auth credentials) from a
// scrape URL before it is exposed in tool output. The scheme, host, port, path
// and query are preserved so the URL remains useful for diagnosing scrape
// connectivity. Credentials in URL userinfo are a common (if discouraged)
// Prometheus scrape-config pattern; leaking them into LLM context would risk
// disclosure via logs or model-provider responses.
//
// If the value is not a parseable absolute URL, it is returned unchanged so
// redaction never interferes with non-URL strings.
func redactScrapeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	if u.User != nil {
		u.User = nil
	}
	return u.String()
}
