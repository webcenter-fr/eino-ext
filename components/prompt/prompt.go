// Package prompt provides a base of solid system prompts by type, extensible
// per project. Each constructor returns an assembled *Prompt that can be used
// directly as an eino system message via Message().
package prompt

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Kind identifies the type of a system prompt.
type Kind string

const (
	// KindQuestion is a read-only Q&A / analysis prompt.
	KindQuestion Kind = "question"
	// KindTroubleshoot is an infrastructure + code troubleshooting prompt.
	KindTroubleshoot Kind = "troubleshoot"
	// KindCheck is a current-state verification prompt.
	KindCheck Kind = "check"
	// KindArchitecture is an architecture documentation prompt.
	KindArchitecture Kind = "architecture"
	// KindPullRequestReview is a pull-request reviewer persona prompt.
	KindPullRequestReview Kind = "pull_request_review"
)

//go:embed prompts/question.md
var baseQuestion string

//go:embed prompts/troubleshoot.md
var baseTroubleshoot string

//go:embed prompts/check.md
var baseCheck string

//go:embed prompts/architecture.md
var baseArchitecture string

//go:embed prompts/pull_request_review.md
var basePullRequestReview string

// section is an optional additional named block appended to the prompt.
type section struct {
	title string
	body  string
}

// Prompt is an assembled system prompt of a given kind.
type Prompt struct {
	kind     Kind
	base     string
	project  string
	sections []section
}

// Option configures a Prompt.
type Option func(*Prompt)

// WithExtraSection appends an additional named section (title + body) to the
// assembled prompt.
func WithExtraSection(title, body string) Option {
	return func(p *Prompt) {
		p.sections = append(p.sections, section{title: title, body: body})
	}
}

// newPrompt builds a Prompt of the given kind from its embedded base and the
// optional project rules.
func newPrompt(kind Kind, base, projectRules string, opts ...Option) *Prompt {
	p := &Prompt{
		kind:    kind,
		base:    base,
		project: strings.TrimSpace(projectRules),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Kind returns the prompt kind.
func (p *Prompt) Kind() Kind {
	return p.kind
}

// String returns the assembled system prompt: base, then any extra sections,
// then the project-specific rules section when project rules are set.
func (p *Prompt) String() string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(p.base, "\n"))

	for _, s := range p.sections {
		fmt.Fprintf(&b, "\n\n## %s\n\n%s", strings.TrimSpace(s.title), strings.TrimSpace(s.body))
	}

	if p.project != "" {
		fmt.Fprintf(&b, "\n\n## Project-specific rules\n\n%s\n\n%s",
			"The following rules are specific to this project and supersede the general guidelines above in case of conflict:",
			p.project)
	}

	return b.String()
}

// Message returns the assembled prompt as an eino system message.
func (p *Prompt) Message() *schema.Message {
	return schema.SystemMessage(p.String())
}
