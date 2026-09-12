package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiListAllExcludesSoftDeletedPublishedLearningSources(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()
	live := makeWikiPage("kb-source", "concept/live", "concept", types.WikiPageStatusPublished)
	deleted := makeWikiPage("kb-source", "concept/deleted", "concept", types.WikiPageStatusPublished)
	archived := makeWikiPage("kb-source", "concept/archived", "concept", types.WikiPageStatusArchived)
	otherKB := makeWikiPage("kb-other", "concept/other", "concept", types.WikiPageStatusPublished)
	for _, page := range []*types.WikiPage{live, deleted, archived, otherKB} {
		require.NoError(t, repo.Create(ctx, page))
	}
	require.NoError(t, db.Delete(deleted).Error)
	var retained types.WikiPage
	require.NoError(t, db.Unscoped().First(&retained, "id = ?", deleted.ID).Error)
	require.True(t, retained.DeletedAt.Valid, "the deleted row physically remains")
	require.Equal(t, types.WikiPageStatusPublished, retained.Status, "soft deletion does not rewrite status")

	visible, err := repo.ListAll(ctx, "kb-source")
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, live.ID, visible[0].ID)
	require.False(t, visible[0].DeletedAt.Valid)
}
