package prompt

// NewArchitecture returns an architecture documentation prompt. projectRules
// holds the project's custom rules and may be empty.
func NewArchitecture(projectRules string, opts ...Option) *Prompt {
	return newPrompt(KindArchitecture, baseArchitecture, projectRules, opts...)
}
