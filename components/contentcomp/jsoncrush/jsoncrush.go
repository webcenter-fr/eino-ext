// Package jsoncrush provides a deterministic, lossless "crush" of JSON arrays of
// objects: keys common to every row (with an identical value) are hoisted into a
// shared "_defaults" block, leaving only per-row deviations. This is the
// deterministic variant ported from lean-ctx's json_crush (NOT headroom's
// statistical SmartCrusher, which is non-deterministic and excluded by the plan).
//
// All output is deterministic (sorted keys, no statistics), so unchanged regions
// stay byte-stable for prompt caching. Non-array / non-object inputs pass through
// unchanged.
//
// An opt-in lossy stage moves near-unique high-entropy columns behind a
// content-addressed Store handle (never discarded; reversible via ExpandWithStore).
package jsoncrush

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/components/contentcomp"
)

const (
	markerKey   = "_jsoncrush"
	defaultsKey = "_defaults"
	rowsKey     = "_rows"
	lossyKey    = "_lossy"
	version     = 1
)

// Option configures Crush.
type Option func(*options)

type options struct {
	store contentcomp.Store
	lossy bool
}

// WithStore enables the lossy stage, moving near-unique columns behind handles
// in store. Without a Store the lossy stage is a no-op.
func WithStore(store contentcomp.Store) Option {
	return func(o *options) { o.store = store; o.lossy = store != nil }
}

// canonical re-encodes raw JSON with sorted object keys and number fidelity
// (json.Number), producing a deterministic representation.
func canonical(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseRows attempts to decode content as a JSON array of objects. ok is false
// when content is not such an array (caller passes through unchanged).
func parseRows(content string) (rows []map[string]json.RawMessage, ok bool) {
	dec := json.NewDecoder(bytes.NewReader([]byte(content)))
	dec.UseNumber()
	var raws []json.RawMessage
	if err := dec.Decode(&raws); err != nil {
		return nil, false
	}
	if len(raws) == 0 {
		return nil, false
	}
	rows = make([]map[string]json.RawMessage, 0, len(raws))
	for _, r := range raws {
		trimmed := bytes.TrimSpace(r)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil, false
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(r, &m); err != nil {
			return nil, false
		}
		rows = append(rows, m)
	}
	return rows, true
}

// Crush hoists common key/value pairs of a JSON array of objects into a shared
// defaults block. It is deterministic and lossless by default. When the lossy
// stage is enabled (WithStore), near-unique columns are moved behind handles and
// the returned refs allow their later resolution.
//
// If content is not a JSON array of objects, it is returned unchanged with no
// refs.
func Crush(ctx context.Context, content string, opts ...Option) (out string, refs []contentcomp.Ref, err error) {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	rows, ok := parseRows(content)
	if !ok {
		return content, nil, nil
	}

	// Canonicalize every value so equality / uniqueness checks are deterministic.
	canon := make([]map[string]string, len(rows))
	keySet := map[string]struct{}{}
	for i, row := range rows {
		canon[i] = make(map[string]string, len(row))
		for k, v := range row {
			cv, cerr := canonical(v)
			if cerr != nil {
				return "", nil, errors.Wrap(cerr, "jsoncrush: canonicalize")
			}
			canon[i][k] = cv
			keySet[k] = struct{}{}
		}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Hoist defaults: keys present in every row with an identical value.
	defaults := map[string]json.RawMessage{}
	for _, k := range keys {
		first, has := canon[0][k]
		if !has {
			continue
		}
		same := true
		for i := 1; i < len(rows); i++ {
			if cv, h := canon[i][k]; !h || cv != first {
				same = false
				break
			}
		}
		if same {
			defaults[k] = json.RawMessage(first)
		}
	}

	// Optional lossy stage: all-present, all-distinct columns are offloaded.
	lossy := map[string]string{}
	if o.lossy {
		for _, k := range keys {
			if _, isDefault := defaults[k]; isDefault {
				continue
			}
			if !allPresentDistinct(canon, k) {
				continue
			}
			values := make([]json.RawMessage, len(rows))
			for i := range rows {
				values[i] = json.RawMessage(canon[i][k])
			}
			payload, merr := json.Marshal(values)
			if merr != nil {
				return "", nil, errors.Wrap(merr, "jsoncrush: marshal lossy column")
			}
			ref, perr := o.store.Put(ctx, string(payload))
			if perr != nil {
				return "", nil, errors.Wrap(perr, "jsoncrush: store lossy column")
			}
			lossy[k] = ref.Key
			refs = append(refs, ref)
		}
	}

	// Build per-row deviations (keys not hoisted to defaults / not offloaded).
	outRows := make([]map[string]json.RawMessage, len(rows))
	for i, row := range rows {
		dev := map[string]json.RawMessage{}
		for k := range row {
			if _, isDefault := defaults[k]; isDefault {
				continue
			}
			if _, isLossy := lossy[k]; isLossy {
				continue
			}
			dev[k] = json.RawMessage(canon[i][k])
		}
		outRows[i] = dev
	}

	crushed := map[string]any{
		markerKey:   version,
		defaultsKey: defaults,
		rowsKey:     outRows,
	}
	if len(lossy) > 0 {
		crushed[lossyKey] = lossy
	}

	encoded, merr := json.Marshal(crushed)
	if merr != nil {
		return "", nil, errors.Wrap(merr, "jsoncrush: marshal crushed")
	}

	// Only return the crushed form when it is actually smaller; otherwise leave
	// the input untouched (cache-safe, avoids pathological growth).
	if len(encoded) >= len(content) && len(lossy) == 0 {
		return content, nil, nil
	}
	return string(encoded), refs, nil
}

func allPresentDistinct(canon []map[string]string, k string) bool {
	seen := make(map[string]struct{}, len(canon))
	for i := range canon {
		v, has := canon[i][k]
		if !has {
			return false
		}
		if _, dup := seen[v]; dup {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}

// IsCrushed reports whether content is a jsoncrush-encoded payload.
func IsCrushed(content string) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &probe); err != nil {
		return false
	}
	_, ok := probe[markerKey]
	return ok
}

// Expand reverses a lossless Crush. It returns canonical JSON (sorted keys). For
// payloads produced with the lossy stage, use ExpandWithStore. Non-crushed input
// is returned unchanged.
func Expand(content string) (string, error) {
	return ExpandWithStore(context.Background(), content, nil)
}

// ExpandWithStore reverses a Crush, resolving any offloaded lossy columns through
// store. Non-crushed input is returned unchanged.
func ExpandWithStore(ctx context.Context, content string, store contentcomp.Store) (string, error) {
	var crushed struct {
		Marker   *int                         `json:"_jsoncrush"`
		Defaults map[string]json.RawMessage   `json:"_defaults"`
		Rows     []map[string]json.RawMessage `json:"_rows"`
		Lossy    map[string]string            `json:"_lossy"`
	}
	if err := json.Unmarshal([]byte(content), &crushed); err != nil || crushed.Marker == nil {
		return content, nil
	}

	// Resolve offloaded columns.
	lossyValues := map[string][]json.RawMessage{}
	for k, key := range crushed.Lossy {
		if store == nil {
			return "", errors.Errorf("jsoncrush: lossy column %q requires a Store to expand", k)
		}
		data, err := store.Get(ctx, contentcomp.Ref{Key: key})
		if err != nil {
			return "", errors.Wrapf(err, "jsoncrush: resolve lossy column %q", k)
		}
		var vals []json.RawMessage
		if err := json.Unmarshal([]byte(data), &vals); err != nil {
			return "", errors.Wrapf(err, "jsoncrush: decode lossy column %q", k)
		}
		lossyValues[k] = vals
	}

	out := make([]map[string]json.RawMessage, len(crushed.Rows))
	for i, dev := range crushed.Rows {
		merged := make(map[string]json.RawMessage, len(dev)+len(crushed.Defaults))
		for k, v := range crushed.Defaults {
			merged[k] = v
		}
		for k, v := range dev {
			merged[k] = v
		}
		for k, vals := range lossyValues {
			if i < len(vals) {
				merged[k] = vals[i]
			}
		}
		out[i] = merged
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return "", errors.Wrap(err, "jsoncrush: marshal expanded")
	}
	return string(encoded), nil
}
