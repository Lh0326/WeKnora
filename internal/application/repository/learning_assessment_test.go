package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAssessmentComponentSourcesShareTransactionAndScope(t *testing.T) {
	db := setupLearningTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}))
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	r := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	pages := []types.WikiPage{
		{ID: "own", TenantID: 1, KnowledgeBaseID: "kb", Slug: "own", Content: "same-transaction source"},
		{ID: "tenant", TenantID: 2, KnowledgeBaseID: "kb", Slug: "other-tenant", Content: "private other tenant"},
		{ID: "kb", TenantID: 1, KnowledgeBaseID: "other", Slug: "other-kb", Content: "private other kb"},
		{ID: "archived", TenantID: 1, KnowledgeBaseID: "kb", Slug: "archived", Status: types.WikiPageStatusArchived},
	}
	sentinel := errors.New("rollback read fixture")
	err = r.WithSubject(ctx, "alice", 0, false, func(tx context.Context) error {
		require.NoError(t, r.database(tx).Create(&pages).Error)
		// The rows are uncommitted and the connection pool has one connection.
		// Reading outside this transaction would not see them (or would block).
		sources, e := r.ListComponentAssessmentSources(tx, 1, "kb")
		require.NoError(t, e)
		require.Len(t, sources, 1)
		require.Equal(t, "own", sources[0].Slug)
		require.Equal(t, "same-transaction source", sources[0].Content)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	var count int64
	require.NoError(t, db.Model(&types.WikiPage{}).Count(&count).Error)
	require.Zero(t, count, "source reads must not escape or commit the caller transaction")
}
