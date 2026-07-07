package contextopt

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// VerbositySteerMarkerKey marks a system message to which a VerbositySteer
// instruction has already been appended, guaranteeing idempotence.
const VerbositySteerMarkerKey = "__eino_ext_contextopt_verbosity_steer"

// VolatileKind classifies a volatile token detected in the cached prefix.
type VolatileKind string

const (
	// VolatileTimestamp is an ISO-8601 timestamp.
	VolatileTimestamp VolatileKind = "timestamp"
	// VolatileUUID is a UUID (v4-style).
	VolatileUUID VolatileKind = "uuid"
	// VolatileIDField is a JSON field whose name ends in "_id".
	VolatileIDField VolatileKind = "id_field"
)

// VolatileFinding describes a volatile token found in the cacheable prefix
// (system prompt + tools + first messages). Volatile tokens churn the prefix and
// defeat prompt caching; this is warn-only, no bytes are modified.
type VolatileFinding struct {
	MessageIndex int
	Role         schema.RoleType
	Kind         VolatileKind
	Sample       string
}

var (
	reISO8601 = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	reUUID    = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}`)
	reIDField = regexp.MustCompile(`"([A-Za-z0-9]+_id)"\s*:`)
)

// prefixScanDepth is the number of leading messages (beyond system messages)
// scanned by VolatileCheck.
const prefixScanDepth = 3

// runVolatileCheck scans the cached prefix and reports findings via the observer.
// It never mutates msgs.
func (o *Optimizer) runVolatileCheck(ctx context.Context, msgs []*schema.Message) {
	if !o.cfg.VolatileCheck || o.cfg.VolatileObserver == nil {
		return
	}
	scanned := 0
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		isPrefix := msg.Role == schema.System || scanned < prefixScanDepth
		if msg.Role != schema.System {
			scanned++
		}
		if !isPrefix {
			break
		}
		for _, f := range scanContent(msg.Content) {
			f.MessageIndex = i
			f.Role = msg.Role
			o.cfg.VolatileObserver(ctx, f)
		}
	}
}

func scanContent(content string) []VolatileFinding {
	var out []VolatileFinding
	if m := reISO8601.FindString(content); m != "" {
		out = append(out, VolatileFinding{Kind: VolatileTimestamp, Sample: m})
	}
	if m := reUUID.FindString(content); m != "" {
		out = append(out, VolatileFinding{Kind: VolatileUUID, Sample: m})
	}
	if m := reIDField.FindStringSubmatch(content); m != nil {
		out = append(out, VolatileFinding{Kind: VolatileIDField, Sample: m[1]})
	}
	return out
}

// applyVerbositySteer appends the configured concision instruction to the END of
// the first system message (cache-safe: the prefix before the append is
// unchanged). It is idempotent (marked via VerbositySteerMarkerKey) and clones
// rather than mutating the input.
func (o *Optimizer) applyVerbositySteer(msgs []*schema.Message) []*schema.Message {
	steer := strings.TrimSpace(o.cfg.VerbositySteer)
	if steer == "" {
		return msgs
	}
	for i, msg := range msgs {
		if msg == nil || msg.Role != schema.System {
			continue
		}
		if msg.Extra != nil {
			if done, _ := msg.Extra[VerbositySteerMarkerKey].(bool); done {
				return msgs
			}
		}
		out := make([]*schema.Message, len(msgs))
		copy(out, msgs)
		clone := *msg
		clone.Content = fmt.Sprintf("%s\n\n%s", msg.Content, steer)
		extra := make(map[string]any, len(clone.Extra)+1)
		for k, v := range clone.Extra {
			extra[k] = v
		}
		extra[VerbositySteerMarkerKey] = true
		clone.Extra = extra
		out[i] = &clone
		return out
	}
	return msgs
}
