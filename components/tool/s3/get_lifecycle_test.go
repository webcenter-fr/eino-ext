package s3

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
)

type mockAPIError struct {
	code string
	msg  string
}

func (e *mockAPIError) Error() string                 { return e.code + ": " + e.msg }
func (e *mockAPIError) ErrorMessage() string          { return e.msg }
func (e *mockAPIError) ErrorCode() string             { return e.code }
func (e *mockAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestGetLifecycleTool(t *testing.T) {
	ctx := context.Background()

	rules := []types.LifecycleRule{
		{
			ID:     aws.String("expire-logs"),
			Status: types.ExpirationStatusEnabled,
			Filter: &types.LifecycleRuleFilter{
				Prefix: aws.String("logs/"),
			},
			Expiration: &types.LifecycleExpiration{
				Days: aws.Int32(30),
			},
		},
		{
			ID:     aws.String("transition-old-data"),
			Status: types.ExpirationStatusEnabled,
			Filter: &types.LifecycleRuleFilter{
				Prefix: aws.String("data/"),
			},
			Transitions: []types.Transition{
				{
					Days:         aws.Int32(90),
					StorageClass: types.TransitionStorageClassStandardIa,
				},
			},
		},
	}

	configs := testConfigs()
	mc := newMockLifecycleClient(rules, nil)
	tool, err := newGetLifecycleToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &GetLifecycleParams{Instance: "prod-logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var out lifecycleOutput
	err = json.Unmarshal([]byte(result), &out)
	assert.NoError(t, err)
	assert.True(t, out.HasLifecycle)
	assert.Len(t, out.Rules, 2)
	assert.Equal(t, "expire-logs", out.Rules[0].ID)
	assert.Equal(t, "Enabled", out.Rules[0].Status)
	assert.Contains(t, out.Rules[0].Filter, "logs/")
	assert.NotEmpty(t, out.Message)
}

func TestGetLifecycleToolError(t *testing.T) {
	ctx := context.Background()

	configs := testConfigs()
	mc := newMockLifecycleClient(nil, &types.NoSuchBucket{})
	tool, err := newGetLifecycleToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &GetLifecycleParams{Instance: "prod-logs"}
	_, err = tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.Error(t, err)
}

func TestGetLifecycleToolNoSuchLifecycleConfig(t *testing.T) {
	ctx := context.Background()

	configs := testConfigs()
	mc := newMockLifecycleClient(nil, &mockAPIError{code: "NoSuchLifecycleConfiguration", msg: "no lifecycle"})
	tool, err := newGetLifecycleToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &GetLifecycleParams{Instance: "prod-logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var out lifecycleOutput
	err = json.Unmarshal([]byte(result), &out)
	assert.NoError(t, err)
	assert.False(t, out.HasLifecycle)
	assert.Contains(t, out.Message, "No lifecycle configuration")
}

func TestGetLifecycleToolEmptyRules(t *testing.T) {
	ctx := context.Background()

	configs := testConfigs()
	mc := newMockLifecycleClient([]types.LifecycleRule{}, nil)
	tool, err := newGetLifecycleToolWithClients(configs, map[string]Client{"prod-logs": mc})
	assert.NoError(t, err)

	params := &GetLifecycleParams{Instance: "prod-logs"}
	result, err := tool.InvokableRun(ctx, mustMarshal(t, params))
	assert.NoError(t, err)

	var out lifecycleOutput
	err = json.Unmarshal([]byte(result), &out)
	assert.NoError(t, err)
	assert.False(t, out.HasLifecycle)
}

func TestGetLifecycleToolInvalidInstance(t *testing.T) {
	ctx := context.Background()
	configs := testConfigs()

	tool, err := NewGetLifecycleTool(ctx, configs)
	assert.NoError(t, err)

	_, err = tool.InvokableRun(ctx, `{"instance":"unknown"}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
