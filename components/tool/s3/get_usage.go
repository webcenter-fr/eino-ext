package s3

import (
	"context"

	"emperror.dev/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const getUsageDescription = `
** General Purpose **
It computes the current storage usage for an S3 bucket: total size, object count, and percentage of a configured maximum.

** Output **
It returns a JSON object with the following fields:
- bucket: the bucket instance name.
- total_objects: total number of objects in the bucket.
- total_size_bytes: total size in bytes.
- total_size_human: total size in human-readable format (e.g. "15.3 GB").
- max_size_bytes: the configured maximum size (0 if not set).
- max_size_human: the configured maximum size in human-readable format.
- usage_percent: percentage used (only set if max_size_bytes > 0).
`

// GetUsageParams holds the parameters for GetUsageTool.
type GetUsageParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The S3 bucket instance to query."`
}

// GetUsageTool computes total usage for an S3 bucket.
type GetUsageTool struct {
	*baseTool
	tool.InvokableTool
}

type usageOutput struct {
	Bucket         string  `json:"bucket"`
	TotalObjects   int64   `json:"total_objects"`
	TotalSizeBytes int64   `json:"total_size_bytes"`
	TotalSizeHuman string  `json:"total_size_human"`
	MaxSizeBytes   int64   `json:"max_size_bytes,omitempty"`
	MaxSizeHuman   string  `json:"max_size_human,omitempty"`
	UsagePercent   float64 `json:"usage_percent,omitempty"`
}

// Invoke computes the total storage usage for the bucket.
func (t *GetUsageTool) Invoke(ctx context.Context, params *GetUsageParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	cfg := t.configs.GetConfig(params.Instance)

	var totalSize int64
	var totalObjects int64

	var continuationToken *string
	for {
		input := &s3sdk.ListObjectsV2Input{
			Bucket:            aws.String(cfg.BucketName),
			ContinuationToken: continuationToken,
		}

		resp, err := c.ListObjectsV2(ctx, input)
		if err != nil {
			return "", errors.Wrap(err, "failed to list objects for usage computation")
		}

		totalObjects += int64(len(resp.Contents))
		for _, obj := range resp.Contents {
			if obj.Size != nil {
				totalSize += *obj.Size
			}
		}

		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		continuationToken = resp.NextContinuationToken
	}

	out := usageOutput{
		Bucket:         params.Instance,
		TotalObjects:   totalObjects,
		TotalSizeBytes: totalSize,
		TotalSizeHuman: humanSize(totalSize),
	}

	b, err := json.Marshal(out)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal usage output")
	}
	return string(b), nil
}

// NewGetUsageTool creates a new GetUsageTool for the given configs.
func NewGetUsageTool(ctx context.Context, configs Configs) (*GetUsageTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	t := &GetUsageTool{baseTool: base}
	invokable, err := utils.InferTool("s3_get_usage", getUsageDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable

	return t, nil
}
