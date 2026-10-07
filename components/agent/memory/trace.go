package memory

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TraceStepKind identifies the provenance of a single trace step.
type TraceStepKind string

const (
	// TraceStepAssistantText is assistant prose (schema.Assistant Content).
	TraceStepAssistantText TraceStepKind = "assistant_text"
	// TraceStepToolCall is an assistant-emitted tool call (name + JSON args).
	TraceStepToolCall TraceStepKind = "tool_call"
	// TraceStepToolResult is a tool execution result (schema.Tool content).
	TraceStepToolResult TraceStepKind = "tool_result"
	// TraceStepTerminalAnswer is the captured terminal answer from a
	// configured terminal tool argument (e.g. attempt_completion.result).
	TraceStepTerminalAnswer TraceStepKind = "terminal_answer"
)

// TraceStep is one ordered step in a run trace.
type TraceStep struct {
	Kind  TraceStepKind `json:"kind"`
	Agent string        `json:"agent"`          // event.AgentName (sub-agent attribution)
	Name  string        `json:"name,omitempty"` // tool/function name for tool_call/tool_result/terminal_answer
	Text  string        `json:"text"`           // content / JSON args / result / terminal answer
}

// RunTrace is the ordered sequence of steps recorded during one agent run.
type RunTrace []TraceStep

// TraceConfig configures run-trace collection and rendering.
type TraceConfig struct {
	// Enabled turns on trace collection + trace-aware extraction. Off by default.
	Enabled bool `json:"enabled" jsonschema:"description=Collect the full run trace for procedure extraction,default=false"`

	// MaxStepChars caps a single rendered step (head+tail). Default 1500.
	MaxStepChars int `json:"max_step_chars" validate:"omitempty,gte=1" jsonschema:"description=Per-step character cap when rendering,default=1500"`

	// MaxChars caps the total rendered trace. Default 12000.
	MaxChars int `json:"max_chars" validate:"omitempty,gte=1" jsonschema:"description=Total rendered trace budget,default=12000"`

	// TerminalTools maps tool name -> the JSON argument field whose value is
	// captured as a terminal_answer step. e.g. {"attempt_completion":"result"}.
	TerminalTools map[string]string `json:"terminal_tools,omitempty" jsonschema:"description=Tool name to JSON argument field mapping for terminal answer capture"`

	// Redact masks secrets in every rendered step. nil uses defaultTraceRedactor.
	Redact func(string) string `json:"-" jsonschema:"-"`

	// StepFilter drops noisy steps before rendering. nil keeps all steps.
	StepFilter func(TraceStep) bool `json:"-" jsonschema:"-"`
}

// Default trace rendering budgets, applied when the corresponding config value
// is non-positive.
const (
	defaultTraceMaxStepChars = 1500
	defaultTraceMaxChars     = 12000
)

// RenderTrace renders a run trace to a single string for extraction.
// It: (1) drops steps filtered by cfg.StepFilter, (2) redacts every step,
// (3) caps each step to cfg.MaxStepChars (head+tail), and (4) trims to
// cfg.MaxChars total, keeping terminal answers first, then tool calls, tool
// results, assistant text — newest first — never dropping terminal answers.
func RenderTrace(trace RunTrace, cfg TraceConfig) string {
	if len(trace) == 0 {
		return ""
	}

	redact := cfg.Redact
	if redact == nil {
		redact = defaultTraceRedactor
	}
	maxStep := cfg.MaxStepChars
	if maxStep <= 0 {
		maxStep = defaultTraceMaxStepChars
	}
	maxTotal := cfg.MaxChars
	if maxTotal <= 0 {
		maxTotal = defaultTraceMaxChars
	}

	steps := make([]TraceStep, 0, len(trace))
	rendered := make([]string, 0, len(trace))
	for _, step := range trace {
		if cfg.StepFilter != nil && !cfg.StepFilter(step) {
			continue
		}
		text := truncateHeadTail(redact(step.Text), maxStep)
		steps = append(steps, step)
		rendered = append(rendered, formatTraceStep(step, text))
	}
	if len(steps) == 0 {
		return ""
	}

	keep := make([]bool, len(steps))
	for i := range keep {
		keep[i] = true
	}

	total := renderedTotal(rendered, keep)
	if total <= maxTotal {
		return joinRendered(rendered, keep)
	}

	// Drop lowest-priority steps first: assistant_text, then tool_result, then
	// tool_call, oldest first within a rank. terminal_answer is never dropped.
	order := make([]int, len(steps))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra := traceStepKeepRank(steps[order[a]].Kind)
		rb := traceStepKeepRank(steps[order[b]].Kind)
		if ra != rb {
			return ra > rb
		}
		return order[a] < order[b]
	})

	for _, idx := range order {
		if total <= maxTotal {
			break
		}
		if steps[idx].Kind == TraceStepTerminalAnswer {
			continue
		}
		keep[idx] = false
		total = renderedTotal(rendered, keep)
	}

	return joinRendered(rendered, keep)
}

// formatTraceStep renders one step as "[kind(:name)@agent] text".
func formatTraceStep(step TraceStep, text string) string {
	label := string(step.Kind)
	if step.Name != "" {
		label += ":" + step.Name
	}
	if step.Agent != "" {
		label += "@" + step.Agent
	}
	return fmt.Sprintf("[%s] %s", label, text)
}

// renderedTotal returns the total length of the kept rendered steps, including
// the newline separators between them.
func renderedTotal(rendered []string, keep []bool) int {
	total := 0
	count := 0
	for i, r := range rendered {
		if !keep[i] {
			continue
		}
		total += len(r)
		count++
	}
	if count > 1 {
		total += count - 1
	}
	return total
}

// joinRendered joins the kept rendered steps in their original order.
func joinRendered(rendered []string, keep []bool) string {
	kept := make([]string, 0, len(rendered))
	for i, r := range rendered {
		if keep[i] {
			kept = append(kept, r)
		}
	}
	return strings.Join(kept, "\n")
}

// traceStepKeepRank returns the keep priority (lower kept first):
// terminal_answer(0) > tool_call(1) > tool_result(2) > assistant_text(3).
func traceStepKeepRank(k TraceStepKind) int {
	switch k {
	case TraceStepTerminalAnswer:
		return 0
	case TraceStepToolCall:
		return 1
	case TraceStepToolResult:
		return 2
	default:
		return 3
	}
}

// truncateHeadTail caps s to max runes keeping a head and a tail.
func truncateHeadTail(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	head := max * 3 / 4
	tail := max - head
	removed := len(runes) - max
	return string(runes[:head]) +
		fmt.Sprintf("…[truncated %d chars]…", removed) +
		string(runes[len(runes)-tail:])
}

// terminalArgValue unmarshals args and returns the value of field. String
// values are returned verbatim; other JSON scalars/objects are marshaled back
// to JSON. It returns ("", false) for a missing field or malformed JSON.
func terminalArgValue(args, field string) (string, bool) {
	if field == "" {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return "", false
	}
	v, ok := m[field]
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

var (
	traceSecretKVRe = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api_?key)(\s*[:=]\s*)([^\s,;]+)`)
	traceBearerRe   = regexp.MustCompile(`(?i)\b(authorization\s*:\s*bearer\s+)[^\s]+`)
	tracePEMRe      = regexp.MustCompile(`-----BEGIN [^-]+-----[\s\S]*?-----END [^-]+-----`)
	traceSASLRe     = regexp.MustCompile(`(?i)(sasl\.jaas\.config\s*=\s*)[^\n;]+`)
)

// defaultTraceRedactor masks secrets in a rendered step. It is best-effort:
// the four patterns cover the common credential leakage vectors (key-value
// secrets, bearer tokens, PEM blocks, Kafka SASL JAAS configs).
func defaultTraceRedactor(s string) string {
	s = traceSecretKVRe.ReplaceAllString(s, "$1$2<redacted>")
	s = traceBearerRe.ReplaceAllString(s, "$1<redacted>")
	s = tracePEMRe.ReplaceAllString(s, "<PEM removed>")
	s = traceSASLRe.ReplaceAllString(s, "$1<redacted>")
	return s
}
