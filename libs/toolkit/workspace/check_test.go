package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheck(t *testing.T) {
	t.Run("nil layout", func(t *testing.T) {
		results := Check(context.Background(), nil)
		require.Len(t, results, 1)
		assert.Equal(t, "error", results[0].Status)
	})

	t.Run("invalid root", func(t *testing.T) {
		results := Check(context.Background(), &Layout{Root: "/tmp", SessionKey: "session"})
		require.Len(t, results, 1)
		assert.Equal(t, "error", results[0].Status)
	})

	t.Run("valid layout", func(t *testing.T) {
		results := Check(context.Background(), &Layout{Root: t.TempDir(), SessionKey: "session"})
		require.Len(t, results, 1)
		assert.Equal(t, "ok", results[0].Status)
	})

	t.Run("require session fails closed", func(t *testing.T) {
		results := Check(context.Background(), &Layout{
			Root:           t.TempDir(),
			SessionKey:     "session",
			RequireSession: true,
		})
		require.Len(t, results, 1)
		assert.Equal(t, "error", results[0].Status)
	})
}
