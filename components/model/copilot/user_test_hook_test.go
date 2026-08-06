package copilot

// setTestUserAPIBaseForTesting overrides the GitHub API base URL used by
// validateFineGrainedPAT. Call with empty string to restore default.
// Only for use in tests.
func setTestUserAPIBaseForTesting(base string) {
	testUserAPIBase = base
}
