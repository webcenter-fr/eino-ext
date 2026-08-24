// Package strutil provides small string helpers shared across components:
// length-capped truncation with a marker, and extraction of a JSON block from
// free-form LLM output.
package strutil

import "strings"

// Truncate returns s unchanged when its rune count is within maxLen (or maxLen
// is non-positive). Otherwise it returns the first maxLen runes followed by the
// given marker. Truncation is rune-aware to avoid splitting multi-byte UTF-8
// characters.
func Truncate(s string, maxLen int, marker string) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	// Truncate at rune boundary to avoid splitting multi-byte characters.
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + marker
}

// StripMarkdownFences removes a surrounding ```lang ... ``` or ``` ... ```
// fenced code block, returning the trimmed inner content.
func StripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		} else if len(s) > 3 {
			s = s[3:]
		}
	}
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	return s
}

// ExtractJSONBlock strips markdown fences, then returns the first JSON array or
// object found via outermost bracket matching. It returns an empty string when
// no balanced block is found.
func ExtractJSONBlock(content string) string {
	content = strings.TrimSpace(StripMarkdownFences(content))

	if start, end := strings.Index(content, "["), strings.LastIndex(content, "]"); start >= 0 && end > start {
		return content[start : end+1]
	}
	if start, end := strings.Index(content, "{"), strings.LastIndex(content, "}"); start >= 0 && end > start {
		return content[start : end+1]
	}
	return ""
}
