package profilesupervisor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProfileSupervisor_NilConfig(t *testing.T) {
	_, err := NewProfileSupervisor(context.Background(), nil)
	assert.Error(t, err)
}

func TestNewProfileSupervisor_EmptyWorkdir(t *testing.T) {
	cfg := &SupervisorConfig{Workdir: t.TempDir()}
	_, err := NewProfileSupervisor(context.Background(), cfg)
	assert.Error(t, err)
	require.Contains(t, err.Error(), "Model")
}
