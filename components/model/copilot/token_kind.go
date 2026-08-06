package copilot

import "strings"

// TokenKind classifies a GitHub credential by its prefix so the provider can
// route to the correct auth mode (direct-bearer vs token exchange vs
// pre-obtained CopilotToken).
type TokenKind string

const (
	// TokenKindFineGrainedPAT is a fine-grained GitHub PAT (github_pat_...).
	// Used DIRECTLY as the Copilot bearer token — no /copilot_internal/v2/token
	// exchange (that endpoint 403s for fine-grained PATs). Requires the
	// "Copilot Requests" account permission (Read) on a user-owned token.
	TokenKindFineGrainedPAT TokenKind = "fine_grained_pat"

	// TokenKindClassicPAT is a classic GitHub PAT (ghp_...). Exchanged at
	// /copilot_internal/v2/token for a short-lived Copilot bearer token
	// (paid plans). Background refresh is started.
	TokenKindClassicPAT TokenKind = "classic_pat"

	// TokenKindCopilotOAuth is a pre-obtained Copilot OAuth token (gho_...).
	// Used directly as the bearer token with no exchange and no refresh —
	// equivalent to setting Config.CopilotToken.
	TokenKindCopilotOAuth TokenKind = "copilot_oauth"

	// TokenKindUnknown is any unrecognized/empty token. Treated as
	// TokenKindClassicPAT (exchange) for backward compatibility.
	TokenKindUnknown TokenKind = "unknown"
)

// DetectTokenKind classifies a raw GitHub credential by prefix. It is a pure
// function (no I/O, no errors). Empty input returns TokenKindUnknown.
// Detection is case-insensitive on the prefix.
func DetectTokenKind(token string) TokenKind {
	if token == "" {
		return TokenKindUnknown
	}
	lower := strings.ToLower(token)
	switch {
	case strings.HasPrefix(lower, "github_pat_"):
		return TokenKindFineGrainedPAT
	case strings.HasPrefix(lower, "ghp_"):
		return TokenKindClassicPAT
	case strings.HasPrefix(lower, "gho_"):
		return TokenKindCopilotOAuth
	default:
		return TokenKindUnknown
	}
}
