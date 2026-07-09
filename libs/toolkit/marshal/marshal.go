package marshal

import (
	"emperror.dev/errors"
	"github.com/goccy/go-json"
)

func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(errors.Wrapf(err, "marshal.MustMarshal(%T)", v).Error())
	}
	return b
}

// Outputs marshals a slice of pre-encoded JSON messages into a single JSON
// array string. It is the shared implementation used by list tools that
// assemble their result from per-item JSON fragments.
func Outputs(outputs []json.RawMessage) (string, error) {
	data, err := json.Marshal(outputs)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal output")
	}
	return string(data), nil
}
