package copilot

import "testing"

func TestDetectPlan(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		want     Plan
	}{
		{
			name:    "business",
			baseURL: "https://api.business.githubcopilot.com",
			want:    PlanBusiness,
		},
		{
			name:    "enterprise",
			baseURL: "https://api.enterprise.githubcopilot.com",
			want:    PlanEnterprise,
		},
		{
			name:    "individual",
			baseURL: "https://api.individual.githubcopilot.com",
			want:    PlanIndividual,
		},
		{
			name:    "empty",
			baseURL: "",
			want:    PlanUnknown,
		},
		{
			name:    "garbage",
			baseURL: "not-a-url",
			want:    PlanUnknown,
		},
		{
			name:    "enterprise custom slug",
			baseURL: "https://api.corp.enterprise.githubcopilot.com",
			want:    PlanEnterprise,
		},
		{
			name:    "invalid url must not panic",
			baseURL: "://not a url",
			want:    PlanUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectPlan(tt.baseURL)
			if got != tt.want {
				t.Errorf("DetectPlan(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}
