package prompt

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestConstructorsKindAndContent(t *testing.T) {
	cases := []struct {
		name   string
		prompt *Prompt
		kind   Kind
		marker string
	}{
		{"question", NewQuestion(""), KindQuestion, "read-only technical assistant"},
		{"troubleshoot", NewTroubleshoot(""), KindTroubleshoot, "troubleshooting infrastructure"},
		{"check", NewCheck(""), KindCheck, "verification agent"},
		{"architecture", NewArchitecture(""), KindArchitecture, "architect"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prompt.Kind() != tc.kind {
				t.Fatalf("Kind() = %q, want %q", tc.prompt.Kind(), tc.kind)
			}
			s := tc.prompt.String()
			if strings.TrimSpace(s) == "" {
				t.Fatal("String() is empty")
			}
			if !strings.Contains(s, tc.marker) {
				t.Fatalf("String() missing role marker %q", tc.marker)
			}
		})
	}
}

func TestProjectRulesSection(t *testing.T) {
	const rules = "Always use the company logging library."

	with := NewQuestion(rules).String()
	if !strings.Contains(with, "## Project-specific rules") {
		t.Fatal("expected project rules section header")
	}
	if !strings.Contains(with, rules) {
		t.Fatal("expected project rules content")
	}
	if !strings.Contains(with, "supersede the general guidelines") {
		t.Fatal("expected supersede note")
	}

	without := NewQuestion("   ").String()
	if strings.Contains(without, "## Project-specific rules") {
		t.Fatal("did not expect project rules section for empty rules")
	}
}

func TestWithExtraSection(t *testing.T) {
	s := NewCheck("", WithExtraSection("Scope", "Only the staging cluster.")).String()
	if !strings.Contains(s, "## Scope") {
		t.Fatal("expected extra section title")
	}
	if !strings.Contains(s, "Only the staging cluster.") {
		t.Fatal("expected extra section body")
	}
}

func TestExtraSectionBeforeProjectRules(t *testing.T) {
	s := NewCheck("Project rule body.", WithExtraSection("Scope", "Extra body.")).String()
	extraIdx := strings.Index(s, "## Scope")
	projIdx := strings.Index(s, "## Project-specific rules")
	if extraIdx < 0 || projIdx < 0 {
		t.Fatal("expected both sections present")
	}
	if extraIdx > projIdx {
		t.Fatal("expected extra section before project rules section")
	}
}

func TestMessage(t *testing.T) {
	p := NewArchitecture("")
	msg := p.Message()
	if msg.Role != schema.System {
		t.Fatalf("Role = %q, want %q", msg.Role, schema.System)
	}
	if msg.Content != p.String() {
		t.Fatal("Message content must equal String()")
	}
}
