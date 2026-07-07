package prompt

// NewPullRequestReview returns a pull-request reviewer persona prompt.
// projectRules holds the project's custom rules and may be empty.
func NewPullRequestReview(projectRules string, opts ...Option) *Prompt {
	return newPrompt(KindPullRequestReview, basePullRequestReview, projectRules, opts...)
}
