package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestComponentSourceSoftDeleteRejectedDespitePublishedStatus(t *testing.T) {
	rows, pages := componentFixture(t)
	component := rows[0]
	live := *pages[0]
	live.Status = types.WikiPageStatusPublished
	defs := []types.ComponentDefinition{component.Definition}

	validated, err := ValidateComponentPack(component.TenantID, component.KnowledgeBaseID, defs, []*types.WikiPage{&live})
	require.NoError(t, err)
	require.Equal(t, component.Version, validated[0].Version)
	require.True(t, componentAvailable(component, []*types.WikiPage{&live}))

	deleted := live
	deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	// Tenant, KB, publication status, content and the recorded hash still match.
	// A raw SQL export can contain exactly this row after GORM soft deletion.
	_, err = ValidateComponentPack(component.TenantID, component.KnowledgeBaseID, defs, []*types.WikiPage{&deleted})
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
	require.False(t, componentAvailable(component, []*types.WikiPage{&deleted}))
	require.Equal(t, types.WikiPageStatusPublished, deleted.Status)
	require.Equal(t, component.Definition.Sources[0].Hash, componentHash(componentNormalize(deleted.Content)))

	// Follow GORM's nullable timestamp semantics, rather than testing Time alone.
	deleted.DeletedAt.Valid = false
	validated, err = ValidateComponentPack(component.TenantID, component.KnowledgeBaseID, defs, []*types.WikiPage{&deleted})
	require.NoError(t, err)
	require.Equal(t, component.Version, validated[0].Version)
	require.True(t, componentAvailable(component, []*types.WikiPage{&deleted}))
}

func TestComponentDeletedSourceCannotOverrideLiveSameSlugInExport(t *testing.T) {
	rows, pages := componentFixture(t)
	component := rows[0]
	live := *pages[0]
	deleted := live
	deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	deleted.Content = "已经删除的旧正文，不包含当前有效组件需要的完整来源引文。"
	for _, exported := range [][]*types.WikiPage{{&live, &deleted}, {&deleted, &live}} {
		validated, err := ValidateComponentPack(component.TenantID, component.KnowledgeBaseID, []types.ComponentDefinition{component.Definition}, exported)
		require.NoError(t, err)
		require.Equal(t, component.Version, validated[0].Version)
		require.True(t, componentAvailable(component, exported))
	}
}
