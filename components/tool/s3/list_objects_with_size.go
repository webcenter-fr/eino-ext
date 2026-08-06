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

const listObjectsWithSizeDescription = `
** General Purpose **
It lists objects in an S3 bucket with detailed size information, sorted by size (largest first) by default. This helps identify large files consuming storage.

** Output **
It returns a JSON array of objects with the following fields:
- key: the object key (path).
- size: the object size in bytes.
- size_human: the object size in human-readable format (e.g. "1.5 GB").
- last_modified: the last modified time in RFC3339 format.
`

// ListObjectsWithSizeParams holds the parameters for ListObjectsWithSizeTool.
type ListObjectsWithSizeParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The S3 bucket instance to list."`
	Prefix   string `json:"prefix,omitempty" jsonschema:"(optional) List only objects with this prefix (acts as a directory path)."`
	MaxKeys  int    `json:"max_keys,omitempty" validate:"omitempty,min=1,max=1000" jsonschema:"(optional) Maximum number of results to return. Default 200, max 1000."`
	SortBy   string `json:"sort_by,omitempty" validate:"omitempty,oneof=alphanumeric size last_modified" jsonschema:"(optional) Sort order: alphanumeric (by key name), size (largest first, default), last_modified (most recent first). Default is size."`
	Filter   string `json:"filter,omitempty" jsonschema:"(optional) A Go RE2 regex applied on each result JSON. Keep only results that match."`
}

// ListObjectsWithSizeTool lists objects with detailed size information.
type ListObjectsWithSizeTool struct {
	*baseTool
	tool.InvokableTool
}

// Invoke lists objects with size details, sorted by size descending by default.
func (t *ListObjectsWithSizeTool) Invoke(ctx context.Context, params *ListObjectsWithSizeParams) (string, error) {
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
		sortBy = SortSize
	}

	var allEntries []objectEntry

	var continuationToken *string
	for {
		input := &s3sdk.ListObjectsV2Input{
			Bucket:            aws.String(cfg.BucketName),
			ContinuationToken: continuationToken,
		}
		if params.Prefix != "" {
			input.Prefix = aws.String(params.Prefix)
		}

		resp, err := c.ListObjectsV2(ctx, input)
		if err != nil {
			return "", errors.Wrap(err, "failed to list objects")
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
			allEntries = append(allEntries, entry)
		}

		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		continuationToken = resp.NextContinuationToken
	}

	sortObjectEntries(allEntries, sortBy)

	if len(allEntries) > int(maxKeys) {
		allEntries = allEntries[:maxKeys]
	}

	outputs := make([]json.RawMessage, 0, len(allEntries))
	for _, entry := range allEntries {
		outputJSON := json.RawMessage(marshal.MustMarshal(entry))
		if !filter.Match(outputJSON, re) {
			continue
		}
		outputs = append(outputs, outputJSON)
	}

	return marshalOutputs(outputs)
}

// NewListObjectsWithSizeTool creates a new ListObjectsWithSizeTool for the given configs.
func NewListObjectsWithSizeTool(ctx context.Context, configs Configs) (*ListObjectsWithSizeTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	t := &ListObjectsWithSizeTool{baseTool: base}
	invokable, err := utils.InferTool("s3_list_objects_with_size", listObjectsWithSizeDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable

	return t, nil
}
