// Package approval is the lightweight client used by MCP servers to talk to
// the standalone approval service. It owns the canonical argsHash, signed
// receipt verification, and the REST client (register + long-poll).
package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"

	"emperror.dev/errors"
)

// Excluded argument keys. They are gate/transport fields, not part of the
// operation being approved.
var excludedArgsKeys = map[string]struct{}{
	"dryRun":     {},
	"confirmed":  {},
	"approvalId": {},
}

// Canonicalize returns the RFC 8785-style canonical JSON of the tool arguments
// with dryRun, confirmed and approvalId removed. Both the MCP server and the
// approval service must produce identical bytes.
func Canonicalize(args json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errors.Wrap(err, "invalid JSON arguments")
	}
	if dec.More() {
		return nil, errors.New("invalid JSON arguments: trailing data")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	for key := range excludedArgsKeys {
		delete(obj, key)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ArgsHash returns the lowercase hex SHA-256 of the canonical arguments.
func ArgsHash(args json.RawMessage) (string, error) {
	canonical, err := Canonicalize(args)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch value := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if value {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		encoded, err := marshalString(value)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case json.Number:
		normalized, err := normalizeNumber(value)
		if err != nil {
			return err
		}
		buf.WriteString(normalized)
	case []any:
		buf.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encoded, err := marshalString(key)
			if err != nil {
				return err
			}
			buf.Write(encoded)
			buf.WriteByte(':')
			if err := writeCanonical(buf, value[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return errors.Errorf("unsupported JSON value type %T", v)
	}
	return nil
}

func marshalString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, errors.Wrap(err, "failed to encode JSON string")
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// normalizeNumber renders a JSON number deterministically. Integers keep their
// exact decimal form; other numbers use the shortest round-trippable float64
// representation. This is the stable-number-formatting part of RFC 8785 that
// both services rely on.
//
// Limitation: integers that do not fit in int64 (magnitude > 2^53) are parsed
// as float64 and lose precision. This is safe here because the canonical form
// is produced by the SAME shared code on both the MCP server and the approval
// service, so both sides always agree byte-for-byte — the hash is a binding
// between the two, not an interoperable RFC 8785 signature for third parties.
func normalizeNumber(n json.Number) (string, error) {
	s := n.String()
	if s == "" {
		return "", errors.New("empty JSON number")
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return s, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "", errors.Wrapf(err, "invalid JSON number %q", s)
	}
	return strconv.FormatFloat(f, 'g', -1, 64), nil
}
