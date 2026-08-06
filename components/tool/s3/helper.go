package s3

import (
	"sort"

	"github.com/dustin/go-humanize"
	"github.com/goccy/go-json"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// SortOrder defines how directory/list results are ordered.
type SortOrder string

const (
	// SortAlphanumeric sorts entries alphabetically by key.
	SortAlphanumeric SortOrder = "alphanumeric"
	// SortSize sorts entries by size, descending.
	SortSize SortOrder = "size"
	// SortLastModified sorts entries by last modification time, descending.
	SortLastModified SortOrder = "last_modified"
)

func marshalOutputs(outputs []json.RawMessage) (string, error) {
	return marshal.Outputs(outputs)
}

func validateParams(v any) error {
	return validate.Struct(v)
}

func humanSize(bytes int64) string {
	return humanize.Bytes(uint64(bytes))
}

// objectEntry holds object metadata for sorting and output.
type objectEntry struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	SizeHuman    string `json:"size_human"`
	LastModified string `json:"last_modified"`
	IsDir        bool   `json:"is_dir,omitempty"`
}

func sortObjectEntries(entries []objectEntry, order SortOrder) {
	switch order {
	case SortSize:
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Size > entries[j].Size
		})
	case SortLastModified:
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].LastModified > entries[j].LastModified
		})
	default:
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Key < entries[j].Key
		})
	}
}
