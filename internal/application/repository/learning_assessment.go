package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// Use the learning transaction rather than the Wiki repository's independent
// connection. No model, author metadata, or source content enters the response.
// This preserves the transaction's isolation level; it does not upgrade a
// READ COMMITTED transaction to a repeatable shared-content snapshot.
func (r *learningRepository) ListComponentAssessmentSources(ctx context.Context, tenant uint64, kb string) ([]*types.WikiPage, error) {
	pages := []*types.WikiPage{}
	err := r.database(ctx).
		Select("tenant_id", "knowledge_base_id", "slug", "content", "status").
		Where("tenant_id = ? AND knowledge_base_id = ? AND status <> ?", tenant, kb, types.WikiPageStatusArchived).
		Order("slug ASC").Find(&pages).Error
	return pages, err
}
