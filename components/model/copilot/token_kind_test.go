package copilot

import "testing"

func TestDetectTokenKind(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  TokenKind
	}{
		{"fine_grained", "github_pat_11AA...", TokenKindFineGrainedPAT},
		{"fine_grained_uppercase", "GITHUB_PAT_11AA...", TokenKindFineGrainedPAT},
		{"fine_grained_mixed", "GitHub_Pat_11AA...", TokenKindFineGrainedPAT},
		{"classic", "ghp_test123", TokenKindClassicPAT},
		{"classic_uppercase", "GHP_test123", TokenKindClassicPAT},
		{"copilot_oauth", "gho_test123", TokenKindCopilotOAuth},
		{"copilot_oauth_uppercase", "GHO_test123", TokenKindCopilotOAuth},
		{"empty", "", TokenKindUnknown},
		{"unknown_prefix", "foobar", TokenKindUnknown},
		{"almost_fine_grained", "github_pat2_no_underscore", TokenKindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectTokenKind(tt.token)
			if got != tt.want {
				t.Errorf("DetectTokenKind(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}
