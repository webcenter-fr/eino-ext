// Package shellout provides deterministic, opt-in compressors for noisy CLI / log
// / diff output (ported in spirit from headroom's log/diff/search compressors and
// lean-ctx's pattern table). Every transform is a pure function of its input:
// unknown / unmatched content passes through byte-identically (cache-safe), and
// the original bytes can be preserved behind a content-addressed Store handle for
// reversibility.
//
// The pattern table is intentionally small (high-ROI, declarative) and extensible
// by adding entries to defaultPatterns.
package shellout

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/libs/contentcomp"
)

// Pattern is a named, deterministic line/stream transform.
type Pattern struct {
	// Name identifies the pattern.
	Name string
	// Apply rewrites content. It must be pure and idempotent.
	Apply func(string) string
}

// Option configures Compress.
type Option func(*options)

type options struct {
	store    contentcomp.Store
	patterns []Pattern
	custom   bool
	minGain  int
}

// WithStore preserves the original content behind a handle when compression
// occurs, returning the Ref so the original remains recoverable.
func WithStore(store contentcomp.Store) Option {
	return func(o *options) { o.store = store }
}

// WithPatterns overrides the default pattern table.
func WithPatterns(patterns ...Pattern) Option {
	return func(o *options) { o.patterns = patterns; o.custom = true }
}

// WithMinGain sets the minimum byte reduction required before the compressed
// form is returned (default 64). Below it, content passes through unchanged.
func WithMinGain(bytes int) Option {
	return func(o *options) { o.minGain = bytes }
}

// DefaultPatterns returns a copy of the built-in pattern table.
func DefaultPatterns() []Pattern {
	out := make([]Pattern, len(defaultPatterns))
	copy(out, defaultPatterns)
	return out
}

// Compress applies the configured pattern table to content. When the result is
// meaningfully smaller it is returned; otherwise content passes through unchanged
// with a nil ref. When a Store is configured and compression occurred, the
// original content is preserved and its Ref returned.
func Compress(ctx context.Context, content string, opts ...Option) (out string, ref *contentcomp.Ref, err error) {
	o := &options{patterns: defaultPatterns, minGain: 64}
	for _, fn := range opts {
		fn(o)
	}

	// Cheap fast-path: content too small to ever reach minGain, or lacking any of
	// the structural signals the default transforms target, is returned untouched
	// without running the (line-splitting / regexp) pattern table. This keeps
	// repeated calls on already-compacted content inexpensive.
	if len(content) <= o.minGain || (!o.custom && !mayCompress(content)) {
		return content, nil, nil
	}

	out = content
	for _, p := range o.patterns {
		out = p.Apply(out)
	}

	if len(content)-len(out) < o.minGain {
		return content, nil, nil
	}

	if o.store != nil {
		r, perr := o.store.Put(ctx, content)
		if perr != nil {
			return "", nil, errors.Wrap(perr, "shellout: store original")
		}
		ref = &r
	}
	return out, ref, nil
}

// mayCompress is a cheap, conservative pre-check: it reports whether content
// contains any structural signal the default pattern table can act on (carriage
// returns, blank-line runs, percentage/progress markers, or 3+ identical
// consecutive lines). When it returns false, the default transforms are
// guaranteed to be a no-op, so they can be skipped.
func mayCompress(content string) bool {
	if strings.Contains(content, "\r") || strings.Contains(content, "\n\n") || strings.Contains(content, "%") {
		return true
	}
	lines := strings.Split(content, "\n")
	run := 1
	for i := 1; i < len(lines); i++ {
		if lines[i] == lines[i-1] {
			run++
			if run >= 3 {
				return true
			}
		} else {
			run = 1
		}
	}
	return false
}

// --- Built-in deterministic patterns -------------------------------------------------

var (
	reProgressBar = regexp.MustCompile(`^\s*[\[#=>.\- ]*\s*\d{1,3}(\.\d+)?%.*$`)
	reCarriage    = regexp.MustCompile(`.*\r`)
	reDownloading = regexp.MustCompile(`(?i)^\s*(downloading|fetching|receiving objects|resolving deltas|compiling|building)\b.*\d+%.*$`)
)

var defaultPatterns = []Pattern{
	{Name: "strip-carriage-progress", Apply: stripCarriageProgress},
	{Name: "drop-progress-bars", Apply: dropProgressBars},
	{Name: "collapse-blank-runs", Apply: collapseBlankRuns},
	{Name: "collapse-repeated-lines", Apply: collapseRepeatedLines},
}

// stripCarriageProgress keeps only the final segment after carriage returns
// within a line (terminal progress redraws collapse to their last frame).
func stripCarriageProgress(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, "\r") {
			lines[i] = reCarriage.ReplaceAllString(ln, "")
		}
	}
	return strings.Join(lines, "\n")
}

// dropProgressBars removes intermediate percentage/progress lines.
func dropProgressBars(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if reDownloading.MatchString(ln) || (reProgressBar.MatchString(ln) && !looksLikeData(ln)) {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// looksLikeData guards against dropping legitimate lines that merely contain a
// percentage (e.g. "coverage: 87% of statements").
func looksLikeData(ln string) bool {
	trimmed := strings.TrimSpace(ln)
	return strings.ContainsAny(trimmed, ":") && !strings.ContainsAny(trimmed, "[=>#")
}

// collapseBlankRuns collapses runs of 2+ blank lines into a single blank line.
func collapseBlankRuns(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, ln := range lines {
		isBlank := strings.TrimSpace(ln) == ""
		if isBlank && blank {
			continue
		}
		blank = isBlank
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// collapseRepeatedLines collapses runs of 3+ identical consecutive lines into a
// single line plus a deterministic marker.
func collapseRepeatedLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		run := j - i
		out = append(out, lines[i])
		if run >= 3 {
			out = append(out, fmt.Sprintf("... [%d identical lines collapsed]", run-1))
		} else {
			for k := 1; k < run; k++ {
				out = append(out, lines[i])
			}
		}
		i = j
	}
	return strings.Join(out, "\n")
}
