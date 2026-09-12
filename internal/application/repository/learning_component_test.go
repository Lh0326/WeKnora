package repository

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestComponentCatalogueAndPersonalLifecycle(t *testing.T) {
	db := setupLearningTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.LearningComponent{}))
	base := NewLearningRepository(db)
	repo := base.(interfaces.LearningComponentRepository)
	ctx := context.Background()
	row := types.LearningComponent{ID: "component", TenantID: 1, KnowledgeBaseID: "kb", Version: "v1", Definition: types.ComponentDefinition{Title: "通用学习材料"}}
	require.NoError(t, repo.SaveComponents(ctx, []types.LearningComponent{row}))
	foreign := row
	foreign.TenantID = 2
	foreign.Definition.Title = "incorrect replacement"
	require.Error(t, repo.SaveComponents(ctx, []types.LearningComponent{foreign}))
	require.NoError(t, base.AppendEvent(ctx, &types.LearningEvent{TenantID: 1, KnowledgeBaseID: "kb", SubjectID: "alice", Slug: "kc:component", Type: types.LearningEventComponent, OccurredAt: time.Now()}))
	exported, err := base.ListEventsBySubject(ctx, "alice")
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.NoError(t, base.DeleteProfileData(ctx, 1, "alice", true))
	exported, err = base.ListEventsBySubject(ctx, "alice")
	require.NoError(t, err)
	require.Empty(t, exported)
	definitions, err := repo.ListComponents(ctx, 1, "kb")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "通用学习材料", definitions[0].Definition.Title)
	foreignRows, err := repo.ListComponents(ctx, 2, "kb")
	require.NoError(t, err)
	require.Empty(t, foreignRows)
}

func TestComponentMemoryHonorsResetWithoutDeletingAssistantMemory(t *testing.T) {
	db := setupLearningTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.MemoryDocAffinity{}, &types.MemoryTopicStat{}))
	base := NewLearningRepository(db)
	repo := base.(interfaces.LearningComponentMemoryRepository)
	ctx := context.Background()
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "alice", KnowledgeBaseID: "kb"}
	old := time.Now().Add(-time.Hour)
	affinity := types.MemoryDocAffinity{ID: "doc-use", TenantID: 1, SubjectID: "alice", KnowledgeBaseID: "kb", KnowledgeID: "doc", Hits: 5, LastUsedAt: old}
	topic := types.MemoryTopicStat{ID: "topic", TenantID: 1, SubjectID: "alice", NormalizedKey: "events", LastSeenAt: old}
	mapping := types.MemoryWikiMap{TenantID: 1, SubjectID: "alice", KnowledgeBaseID: "kb", NormalizedTopicKey: "events", Slug: "concept/events", Confidence: .8, UpdatedAt: old}
	require.NoError(t, db.Create(&affinity).Error)
	require.NoError(t, db.Create(&topic).Error)
	require.NoError(t, db.Create(&mapping).Error)
	a, m, err := repo.ListComponentMemoryInputs(ctx, scope)
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Len(t, m, 1)
	require.NoError(t, base.DeleteProfileData(ctx, 1, "alice", false))
	// Pin fixture timestamps explicitly; consecutive time.Now calls can be
	// identical on Windows. Equality at the reset boundary must be excluded.
	resetAt := old.Add(time.Minute)
	require.NoError(t, db.Model(&types.LearningSubjectEpoch{}).Where("subject_id = ?", "alice").Update("updated_at", resetAt).Error)
	// Simulate maintenance recreating a mapping without new user activity.
	mapping.UpdatedAt = resetAt
	require.NoError(t, db.Create(&mapping).Error)
	a, m, err = repo.ListComponentMemoryInputs(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, a)
	require.Empty(t, m)
	var count int64
	require.NoError(t, db.Model(&types.MemoryDocAffinity{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	newInputAt := resetAt.Add(time.Minute)
	require.NoError(t, db.Model(&topic).Update("last_seen_at", newInputAt).Error)
	require.NoError(t, db.Model(&affinity).Update("last_used_at", newInputAt).Error)
	a, m, err = repo.ListComponentMemoryInputs(ctx, scope)
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Empty(t, m, "mapping timestamp equals reset boundary")
	require.NoError(t, db.Model(&types.MemoryWikiMap{}).Where("subject_id = ? AND normalized_topic_key = ?", "alice", "events").Update("updated_at", newInputAt).Error)
	a, m, err = repo.ListComponentMemoryInputs(ctx, scope)
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Len(t, m, 1)
	scope.SubjectID = "bob"
	a, m, err = repo.ListComponentMemoryInputs(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, a)
	require.Empty(t, m)
}
