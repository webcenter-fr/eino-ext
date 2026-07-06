package marshal

import (
	"fmt"

	"github.com/goccy/go-json"
)

func MustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal.MustMarshal(%T): %v", v, err))
	}
	return b
}

func MustUnmarshal(data []byte, v any) {
	if err := json.Unmarshal(data, v); err != nil {
		panic(err)
	}
}
