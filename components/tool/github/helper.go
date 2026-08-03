package github

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"

	"emperror.dev/errors"
	"github.com/goccy/go-json"
	ghlib "github.com/google/go-github/v71/github" // aliased to avoid conflict with package name
	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

// labelList splits a comma-separated labels string into a slice.
func labelList(labels string) []string {
	if labels == "" {
		return nil
	}
	parts := strings.Split(labels, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// truncate returns a truncated string with "..." if longer than maxLen.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// clonePath returns the safe local path for cloning a repository.
func clonePath(cloneDir, owner, repo string) string {
	return fmt.Sprintf("%s/%s/%s", cloneDir, sanitizeSegment(owner), sanitizeSegment(repo))
}

// sanitizeSegment ensures a path segment does not contain path traversal characters.
func sanitizeSegment(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", "")
	}
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if s == "" || s == "." {
		s = "repo"
	}
	return s
}

const defaultMaxPages = 5

// paginateList iterates through paginated GitHub API results up to maxPages
// pages, accumulating all items into a single slice. The fetch callback is
// called for each page; it must set opts.Page to the supplied page number
// before making the API call.
func paginateList[T any](
	fetch func(page int) ([]T, *ghlib.Response, error),
	maxPages int,
) ([]T, error) {
	var allItems []T
	page := 1
	for pagesFetched := 0; pagesFetched < maxPages; pagesFetched++ {
		items, resp, err := fetch(page)
		if err != nil {
			return nil, errors.Wrapf(err, "paginateList fetch page %d", page)
		}
		if resp == nil {
			return nil, errors.Errorf("paginateList fetch page %d: nil response", page)
		}
		allItems = append(allItems, items...)
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return allItems, nil
}

//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

//go:embed prompts/describe_output_guidance.md
var describeOutputGuidance string

// filterMapMarshal maps each source item to an output value, marshals it, keeps
// only items whose JSON matches re, and returns the JSON array of survivors.
func filterMapMarshal[T, O any](items []T, re *regexp.Regexp, toOutput func(T) O) (string, error) {
	outputs := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		outputJSON := json.RawMessage(marshal.MustMarshal(toOutput(item)))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}
	return marshal.Outputs(outputs)
}

// applyExcludes clears each requested field using the provided setter map.
func applyExcludes(excludeFields []string, setters map[string]func()) error {
	for _, field := range excludeFields {
		setter, ok := setters[field]
		if !ok {
			return errors.Errorf("invalid exclude field: %s", field)
		}
		setter()
	}
	return nil
}

// stringPtr returns a pointer to the given string value.
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool {
	return &b
}
