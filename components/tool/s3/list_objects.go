package s3

import (
	"context"
	"time"

	"emperror.dev/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/filter"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/marshal"
)

const listObjectsDescription = `
** General Purpose **
It lists directories and/or files in an S3 bucket with optional prefix filtering and sorting.

** Output **
It returns a JSON array of objects, where each object represents a file or directory with the following fields:
- key: the object key (path).
- size: the object size in bytes.
- size_human: the object size in human-readable format (e.g. "1.5 MB").
- last_modified: the last modified time in RFC3339 format.
- is_dir: true if the entry represents a directory (common prefix).
`

// ListObjectsParams holds the parameters for ListObjectsTool.
type ListObjectsParams struct {
	Instance  string `json:"instance" validate:"required" jsonschema:"(required) The S3 bucket instance to list."`
	Prefix    string `json:"prefix,omitempty" jsonschema:"(optional) List only objects with this prefix (acts as a directory path, e.g. logs/2024/)."`
	Delimiter string `json:"delimiter,omitempty" jsonschema:"(optional) Delimiter for grouping objects into directories. Use '/' to list directories. Leave empty to list all objects recursively."`
	MaxKeys   int    `json:"max_keys,omitempty" validate:"omitempty,min=1,max=1000" jsonschema:"(optional) Maximum number of results to return. Default 200, max 1000."`
	SortBy    string `json:"sort_by,omitempty" validate:"omitempty,oneof=alphanumeric size last_modified" jsonschema:"(optional) Sort order: alphanumeric (by key name), size (largest first), last_modified (most recent first). Default is alphanumeric."`
	Filter    string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each result JSON. Keep only results that match."`
}

// ListObjectsTool lists objects and directories in an S3 bucket.
type ListObjectsTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke lists objects in the bucket.
func (t *ListObjectsTool) Invoke(ctx context.Context, params *ListObjectsParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	re, err := filter.Compile(params.Filter)
	if err != nil {
		return "", errors.Wrap(err, "error when compile regex")
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	cfg := t.configs.GetConfig(params.Instance)

	maxKeys := int32(200)
	if params.MaxKeys > 0 {
		maxKeys = int32(params.MaxKeys)
	}

	sortBy := SortOrder(params.SortBy)
	if sortBy == "" {
		sortBy = SortAlphanumeric
	}

	var entries []objectEntry

	var continuationToken *string
	for {
		input := &s3sdk.ListObjectsV2Input{
			Bucket:            aws.String(cfg.BucketName),
			ContinuationToken: continuationToken,
		}

		if params.Prefix != "" {
			input.Prefix = aws.String(params.Prefix)
		}
		if params.Delimiter != "" {
			input.Delimiter = aws.String(params.Delimiter)
		}

		resp, err := c.ListObjectsV2(ctx, input)
		if err != nil {
			return "", errors.Wrap(err, "failed to list objects")
		}

		for _, prefix := range resp.CommonPrefixes {
			entries = append(entries, objectEntry{
				Key:   *prefix.Prefix,
				IsDir: true,
			})
		}

		for _, obj := range resp.Contents {
			entry := objectEntry{
				Key: *obj.Key,
			}
			if obj.Size != nil {
				entry.Size = *obj.Size
				entry.SizeHuman = humanSize(*obj.Size)
			}
			if obj.LastModified != nil {
				entry.LastModified = obj.LastModified.Format(time.RFC3339)
			}
			entries = append(entries, entry)
		}

		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		continuationToken = resp.NextContinuationToken
	}

	sortObjectEntries(entries, sortBy)

	// Collect up to maxKeys matches after filtering.
	outputs := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		outputJSON := json.RawMessage(marshal.MustMarshal(entry))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
		if len(outputs) >= int(maxKeys) {
			break
		}
	}

	return marshalOutputs(outputs)
}

// NewListObjectsTool creates a new ListObjectsTool for the given configs.
func NewListObjectsTool(ctx context.Context, configs Configs) (*ListObjectsTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	t := &ListObjectsTool{baseTool: base}
	invokable, err := utils.InferTool("s3_list_objects", listObjectsDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable

	return t, nil
}
