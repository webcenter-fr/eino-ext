package opensearch

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/webcenter-fr/eino-ext/libs/toolkit/osclient"
	"github.com/webcenter-fr/eino-ext/server/approval/store"
	"github.com/webcenter-fr/eino-ext/server/approval/store/storetest"
)

func TestOpenSearchStoreConformance(t *testing.T) {
	url := os.Getenv("OPENSEARCH_URL")
	if url == "" {
		t.Skip("OPENSEARCH_URL is not set; skipping OpenSearch integration test")
	}
	storetest.Run(t, func(t *testing.T) store.ApprovalStore {
		s, err := New(context.Background(), &Config{
			Config: osclient.Config{URLs: []string{url}},
			Index:  "approval-records-test",
		})
		require.NoError(t, err)
		return s
	})
}
