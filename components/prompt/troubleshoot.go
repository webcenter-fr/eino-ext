package prompt

// NewTroubleshoot returns an infrastructure + code troubleshooting prompt.
// projectRules holds the project's custom rules and may be empty.
func NewTroubleshoot(projectRules string, opts ...Option) *Prompt {
	return newPrompt(KindTroubleshoot, baseTroubleshoot, projectRules, opts...)
}
