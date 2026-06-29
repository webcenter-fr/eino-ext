package prompt

// NewCheck returns a current-state verification prompt. projectRules holds the
// project's custom rules and may be empty.
func NewCheck(projectRules string, opts ...Option) *Prompt {
	return newPrompt(KindCheck, baseCheck, projectRules, opts...)
}
