package copilot

import (
	"net/url"
	"strings"
)

// Plan is the Copilot subscription tier inferred from the resolved API host.
// It is diagnostic only — routing is driven by the resolved BaseURL, not by Plan.
type Plan string

const (
	PlanUnknown    Plan = "unknown"
	PlanFree       Plan = "free"
	PlanIndividual Plan = "individual"
	PlanBusiness   Plan = "business"
	PlanEnterprise Plan = "enterprise"
)

// DetectPlan derives a Plan from the resolved Copilot API base URL host.
// Free and Individual are indistinguishable by host (both use
// api.individual.githubcopilot.com); DetectPlan returns PlanIndividual for
// that host. Callers that know they are on free-tier should treat
// PlanIndividual as PlanFree. Returns PlanUnknown for unrecognized hosts.
func DetectPlan(baseURL string) Plan {
	u, err := url.Parse(baseURL)
	if err != nil {
		return PlanUnknown
	}
	host := u.Host
	switch {
	case host == "":
		return PlanUnknown
	case strings.Contains(host, "business.githubcopilot.com"):
		return PlanBusiness
	case strings.Contains(host, "enterprise.githubcopilot.com"):
		return PlanEnterprise
	case strings.Contains(host, "individual.githubcopilot.com"):
		return PlanIndividual
	default:
		return PlanUnknown
	}
}
