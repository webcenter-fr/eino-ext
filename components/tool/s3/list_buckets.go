package s3

import (
	"context"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/toolutil"
)

const listBucketsDescription = `
** General Purpose **
It lists all configured S3 bucket instances with their names and descriptions.

** Output **
It returns a JSON array of objects, where each object represents a bucket instance with the following fields:
- name: the logical instance name of the bucket.
- bucket_name: the actual bucket name on the S3 server.
- endpoint: the S3 endpoint URL.
- description: the human-readable description of the bucket's purpose.
`

// BucketListTool lists all configured S3 bucket instances.
type BucketListTool struct {
	*baseTool
	tool.InvokableTool
}

// BucketListParams holds the parameters for BucketListTool (none required).
type BucketListParams struct{}

type bucketListItem struct {
	Name        string `json:"name"`
	BucketName  string `json:"bucket_name"`
	Endpoint    string `json:"endpoint"`
	Description string `json:"description"`
}

// Invoke returns the configured bucket instances as a JSON string array.
func (t *BucketListTool) Invoke(ctx context.Context, params *BucketListParams) (string, error) {
	items := make([]bucketListItem, 0, len(t.knownInstances))
	for _, name := range t.knownInstances {
		cfg := t.configs.GetConfig(name)
		items = append(items, bucketListItem{
			Name:        name,
			BucketName:  cfg.BucketName,
			Endpoint:    cfg.Endpoint,
			Description: cfg.Description,
		})
	}

	b, err := json.Marshal(items)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal bucket list")
	}
	return string(b), nil
}

// NewBucketListTool creates a new BucketListTool for the given configs.
func NewBucketListTool(ctx context.Context, configs Configs) (*BucketListTool, error) {
	t := &BucketListTool{baseTool: newBaseToolWithClients(configs, nil)}

	invokable, err := utils.InferTool("s3_list_buckets", listBucketsDescription, t.Invoke,
		utils.WithUnmarshalArguments(toolutil.EmptyJSONUnmarshaler[*BucketListParams]()))
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable

	return t, nil
}
