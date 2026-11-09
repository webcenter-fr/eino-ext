package approval

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalizeVectors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "sorted keys", in: `{"b":2,"a":1}`, want: `{"a":1,"b":2}`},
		{name: "nested", in: `{"z":[3,{"b":2,"a":1}],"a":"x"}`, want: `{"a":"x","z":[3,{"a":1,"b":2}]}`},
		{name: "excludes gate fields", in: `{"dryRun":true,"confirmed":true,"approvalId":"abc","a":1}`, want: `{"a":1}`},
		{name: "no html escaping", in: `{"a":"<>&"}`, want: `{"a":"<>&"}`},
		{name: "integer", in: `{"a":1}`, want: `{"a":1}`},
		{name: "float integral", in: `{"a":1.0}`, want: `{"a":1}`},
		{name: "float exponent", in: `{"a":1e3}`, want: `{"a":1000}`},
		{name: "float", in: `{"a":1.5}`, want: `{"a":1.5}`},
		{name: "null and bool", in: `{"a":null,"b":false}`, want: `{"a":null,"b":false}`},
		{name: "unicode", in: `{"a":"héllo"}`, want: `{"a":"héllo"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Canonicalize(json.RawMessage(tt.in))
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestCanonicalizeErrors(t *testing.T) {
	_, err := Canonicalize(json.RawMessage(`{`))
	assert.Error(t, err)
	_, err = Canonicalize(json.RawMessage(`[]`))
	assert.Error(t, err)
	_, err = Canonicalize(json.RawMessage(`{"a":1} {"b":2}`))
	assert.Error(t, err)
}

func TestArgsHashStableAcrossKeyOrder(t *testing.T) {
	a, err := ArgsHash(json.RawMessage(`{"b":2,"a":1,"dryRun":true}`))
	require.NoError(t, err)
	b, err := ArgsHash(json.RawMessage(`{"a":1,"b":2,"confirmed":true}`))
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.Len(t, a, 64)
}

func TestArgsHashChangesWithPayload(t *testing.T) {
	a, err := ArgsHash(json.RawMessage(`{"a":1}`))
	require.NoError(t, err)
	b, err := ArgsHash(json.RawMessage(`{"a":2}`))
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}
