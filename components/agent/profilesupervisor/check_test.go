package profilesupervisor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

func TestCheck_NilConfig(t *testing.T) {
	results := Check(context.Background(), nil)
	require.Len(t, results, 1)
	assert.Equal(t, checkup.StatusError, results[0].Status)
}

func TestCheck_EmptyWorkdir(t *testing.T) {
	cfg := &SupervisorConfig{}
	results := Check(context.Background(), cfg)
	require.Len(t, results, 1)
	assert.Equal(t, checkup.StatusError, results[0].Status)
	assert.Contains(t, results[0].Error, "workdir")
}

func TestCheck_NoModel(t *testing.T) {
	cfg := &SupervisorConfig{
		Workdir: "/tmp",
	}
	results := Check(context.Background(), cfg)
	require.Len(t, results, 1)
	assert.Equal(t, checkup.StatusError, results[0].Status)
	assert.Contains(t, results[0].Error, "model")
}
