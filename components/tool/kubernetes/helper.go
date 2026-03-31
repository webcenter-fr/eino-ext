package kubernetes

import "github.com/goccy/go-json"

// MustMarshal is a helper function that marshals a value to JSON and panics if an error occurs. It is useful for quickly converting data structures to JSON without having to handle errors in the calling code.
func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// MustUnmarshal is a helper function that unmarshals JSON data into a specified Go data structure and panics if an error occurs. It is useful for quickly converting JSON data to Go structures without having to handle errors in the calling code.
func MustUnmarshal(data []byte, v any) {
	if err := json.Unmarshal(data, v); err != nil {
		panic(err)
	}
}
