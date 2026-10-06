package pipe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"

	"github.com/webcenter-fr/eino-ext/components/tool/shell"
)

func TestCheck_NilConfig(t *testing.T) {
	results := Check(context.Background(), nil)
	require.Len(t, results, 1)
	assert.Equal(t, checkup.StatusError, results[0].Status)
	assert.Contains(t, results[0].Error, "no pipe config")
}

func TestCheck_NilShell(t *testing.T) {
	results := Check(context.Background(), &Config{})
	require.Len(t, results, 1)
	assert.Equal(t, checkup.StatusError, results[0].Status)
	assert.Contains(t, results[0].Error, "shell config")
}

func TestCheck_WriteToolGate(t *testing.T) {
	// With an empty shell.Config, shell.Check returns exactly one error result
	// ("workdir is required") without touching Dagger, so the pipe results
	// follow at fixed indexes.
	t.Run("gated without authorizer reports limited", func(t *testing.T) {
		results := Check(context.Background(), &Config{
			Shell:          &shell.Config{},
			WriteToolNames: []string{"kubernetes_resource_delete"},
		})
		require.Len(t, results, 3)
		assert.Equal(t, checkup.StatusError, results[0].Status)
		assert.Equal(t, checkup.StatusLimited, results[1].Status)
		assert.Contains(t, results[1].Message, "ExecutionAuthorizer")
		assert.Equal(t, checkup.StatusOK, results[2].Status)
	})

	t.Run("gated with authorizer is not limited", func(t *testing.T) {
		results := Check(context.Background(), &Config{
			Shell:               &shell.Config{},
			WriteToolNames:      []string{"kubernetes_resource_delete"},
			ExecutionAuthorizer: &fakeAuthorizer{approve: map[string]bool{}},
		})
		require.Len(t, results, 2)
		assert.Equal(t, checkup.StatusOK, results[1].Status)
	})

	t.Run("allowModelConfirmation is not limited", func(t *testing.T) {
		results := Check(context.Background(), &Config{
			Shell:                  &shell.Config{},
			WriteToolNames:         []string{"kubernetes_resource_delete"},
			AllowModelConfirmation: true,
		})
		require.Len(t, results, 2)
		assert.Equal(t, checkup.StatusOK, results[1].Status)
	})
}
