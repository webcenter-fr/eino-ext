package s3

import (
	"context"
	"fmt"

	"emperror.dev/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/goccy/go-json"
)

const getLifecycleDescription = `
** General Purpose **
It retrieves the lifecycle configuration for an S3 bucket. Lifecycle rules control how data is managed over time, including automatic deletion, transition to cheaper storage tiers, and expiration of incomplete uploads. This helps understand if data is being retained or cleaned up automatically.

** Output **
It returns a JSON object with the following fields:
- bucket: the bucket instance name.
- has_lifecycle: true if lifecycle rules are configured.
- rules: array of lifecycle rules, each with:
  - id: the rule identifier.
  - status: "Enabled" or "Disabled".
  - filter: the rule's filter description.
  - actions: list of actions (Transitions, Expiration, NoncurrentVersionExpiration, etc.).
- message: human-readable summary of what the lifecycle does.
`

// GetLifecycleParams holds the parameters for GetLifecycleTool.
type GetLifecycleParams struct {
	Instance string `json:"instance" validate:"required" jsonschema:"(required) The S3 bucket instance to query."`
}

// GetLifecycleTool retrieves lifecycle configuration for an S3 bucket.
type GetLifecycleTool struct {
	*baseTool
	tool.InvokableTool
}

type lifecycleRuleOutput struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Filter  string   `json:"filter"`
	Actions []string `json:"actions"`
}

type lifecycleOutput struct {
	Bucket       string                `json:"bucket"`
	HasLifecycle bool                  `json:"has_lifecycle"`
	Rules        []lifecycleRuleOutput `json:"rules,omitempty"`
	Message      string                `json:"message"`
}

// Invoke retrieves the lifecycle configuration for the bucket.
func (t *GetLifecycleTool) Invoke(ctx context.Context, params *GetLifecycleParams) (string, error) {
	if err := validateParams(params); err != nil {
		return "", err
	}

	c, err := t.client(params.Instance)
	if err != nil {
		return "", err
	}

	cfg := t.configs.GetConfig(params.Instance)

	out := lifecycleOutput{
		Bucket: params.Instance,
	}

	lc, err := c.GetBucketLifecycleConfiguration(ctx, &s3sdk.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(cfg.BucketName),
	})
	if err != nil {
		if isNoSuchLifecycleError(err) {
			out.HasLifecycle = false
			out.Message = "No lifecycle configuration is set on this bucket. Data is not automatically expired or transitioned."
			b, err := json.Marshal(out)
			if err != nil {
				return "", errors.Wrap(err, "failed to marshal lifecycle output")
			}
			return string(b), nil
		}
		return "", errors.Wrap(err, "failed to get lifecycle configuration")
	}

	out.HasLifecycle = true
	for _, rule := range lc.Rules {
		ruleOut := lifecycleRuleOutput{
			ID:     aws.ToString(rule.ID),
			Status: string(rule.Status),
		}

		if rule.Filter != nil {
			if rule.Filter.Prefix != nil {
				ruleOut.Filter = fmt.Sprintf("prefix=%s", *rule.Filter.Prefix)
			} else if rule.Filter.Tag != nil {
				ruleOut.Filter = fmt.Sprintf("tag=%s=%s", *rule.Filter.Tag.Key, *rule.Filter.Tag.Value)
			} else {
				ruleOut.Filter = "(all objects)"
			}
		}

		for _, action := range rule.Transitions {
			ruleOut.Actions = append(ruleOut.Actions, fmt.Sprintf("Transition to %s after %d days", action.StorageClass, aws.ToInt32(action.Days)))
		}
		for _, action := range rule.NoncurrentVersionTransitions {
			ruleOut.Actions = append(ruleOut.Actions, fmt.Sprintf("Noncurrent version transition to %s after %d days", action.StorageClass, aws.ToInt32(action.NoncurrentDays)))
		}
		if rule.Expiration != nil {
			if rule.Expiration.Days != nil {
				ruleOut.Actions = append(ruleOut.Actions, fmt.Sprintf("Expire after %d days", *rule.Expiration.Days))
			}
			if rule.Expiration.ExpiredObjectDeleteMarker != nil && *rule.Expiration.ExpiredObjectDeleteMarker {
				ruleOut.Actions = append(ruleOut.Actions, "Delete expired object delete markers")
			}
		}
		if rule.NoncurrentVersionExpiration != nil {
			if rule.NoncurrentVersionExpiration.NoncurrentDays != nil {
				ruleOut.Actions = append(ruleOut.Actions, fmt.Sprintf("Expire noncurrent versions after %d days", *rule.NoncurrentVersionExpiration.NoncurrentDays))
			}
		}
		if rule.AbortIncompleteMultipartUpload != nil {
			if rule.AbortIncompleteMultipartUpload.DaysAfterInitiation != nil {
				ruleOut.Actions = append(ruleOut.Actions, fmt.Sprintf("Abort incomplete multipart uploads after %d days", *rule.AbortIncompleteMultipartUpload.DaysAfterInitiation))
			}
		}

		if len(ruleOut.Actions) == 0 {
			ruleOut.Actions = []string{"(no actions configured)"}
		}

		out.Rules = append(out.Rules, ruleOut)
	}

	if len(out.Rules) == 0 {
		out.HasLifecycle = false
		out.Message = "No lifecycle rules are configured on this bucket."
	} else {
		out.Message = fmt.Sprintf("%d lifecycle rule(s) configured. Review rules for automatic data expiration or transitions.", len(out.Rules))
	}

	b, err := json.Marshal(out)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal lifecycle output")
	}
	return string(b), nil
}

// NewGetLifecycleTool creates a new GetLifecycleTool for the given configs.
func NewGetLifecycleTool(ctx context.Context, configs Configs) (*GetLifecycleTool, error) {
	base, err := newBaseTool(ctx, configs)
	if err != nil {
		return nil, err
	}

	t := &GetLifecycleTool{baseTool: base}
	invokable, err := utils.InferTool("s3_get_lifecycle", getLifecycleDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable

	return t, nil
}
