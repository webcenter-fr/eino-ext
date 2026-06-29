package prompt

// NewQuestion returns a read-only Q&A / analysis prompt. projectRules holds the
// project's custom rules and may be empty.
func NewQuestion(projectRules string, opts ...Option) *Prompt {
	return newPrompt(KindQuestion, baseQuestion, projectRules, opts...)
}
