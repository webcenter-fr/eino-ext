package postgresql

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
	"github.com/webcenter-fr/eino-ext/server/approval/store/storetest"
)

func TestPostgreSQLStoreConformance(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not set; skipping PostgreSQL integration test")
	}
	storetest.Run(t, func(t *testing.T) store.ApprovalStore {
		s, err := New(context.Background(), &Config{DSN: dsn, Table: "approval_records_test"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
