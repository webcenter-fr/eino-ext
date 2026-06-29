# prompt

Package `prompt` provides a base of solid system prompts, one constructor per
type, extensible with project-specific rules. Each constructor returns an
assembled `*Prompt` that can be used directly as an [eino](https://github.com/cloudwego/eino)
system message.

The prompt text is inspired by kilocode's prompt style: a short role statement
followed by terse, actionable guidelines.

## Types

| Type           | Constructor        | When to use |
| -------------- | ------------------ | ----------- |
| `Question`     | `NewQuestion`      | Read-only Q&A and analysis of code, systems, and concepts. |
| `Troubleshoot` | `NewTroubleshoot`  | Diagnose the behavior of an application or pod (infrastructure + code analysis). |
| `Check`        | `NewCheck`         | Verify and report the current state of an application or platform. |
| `Architecture` | `NewArchitecture`  | Document the current architecture (Mermaid diagrams, versions, URLs). |

Each constructor takes the project's custom rules as its first argument (pass
`""` when there are none). When set, project rules are appended in a
`## Project-specific rules` section that supersedes the general guidelines in
case of conflict.

## API

```go
func NewQuestion(projectRules string, opts ...Option) *Prompt
func NewTroubleshoot(projectRules string, opts ...Option) *Prompt
func NewCheck(projectRules string, opts ...Option) *Prompt
func NewArchitecture(projectRules string, opts ...Option) *Prompt

func WithExtraSection(title, body string) Option

func (p *Prompt) String() string           // assembled system prompt
func (p *Prompt) Message() *schema.Message  // schema.SystemMessage(p.String())
func (p *Prompt) Kind() Kind
```

## Example

```go
import (
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/components/prompt"
)

projectRules := "Target only the staging cluster. Never touch production."

p := prompt.NewTroubleshoot(
	projectRules,
	prompt.WithExtraSection("Context", "The payment pod restarts every few minutes."),
)

// Inject as the system message of an eino graph / chat model call.
messages := []*schema.Message{
	p.Message(),
	schema.UserMessage("Why does the payment pod keep crashing?"),
}
```
