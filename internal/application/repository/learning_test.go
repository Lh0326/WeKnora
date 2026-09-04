package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupLearningTestDB creates an in-memory SQLite database with the learning
// tables auto-migrated from the models, which also proves the model-declared
// indexes exist for the upsert conflict targets.
func setupLearningTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.LearningEvent{},
		&types.MasteryState{},
		&types.MemoryWikiMap{},
		&types.LearningEdge{},
		&types.LearningQuizItem{},
		&types.LearningQuizAttempt{},
		&types.LearningSubjectPrefs{},
	))
	return db
}

func learningTestScope() interfaces.LearningScope {
	return interfaces.LearningScope{
		TenantID:        10000,
		SubjectID:       "web_user:test-subject",
		KnowledgeBaseID: "kb-learning-test",
	}
}

func TestLearningRepositoryEventRoundTrip(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()
	scope := learningTestScope()

	at := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	event := &types.LearningEvent{
		TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: 1.0,
		SessionID: "sess-1", OccurredAt: at,
	}
	require.NoError(t, repo.AppendEvent(ctx, event))
	assert.NotEmpty(t, event.ID, "repository must mint the id")

	got, err := repo.ListEvents(ctx, scope, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "concept/rag", got[0].Slug)
	assert.Equal(t, types.LearningEventAnswerCite, got[0].Type)

	// since-filter: nothing before the event, the event itself after.
	none, err := repo.ListEvents(ctx, scope, at.Add(time.Second), 0)
	require.NoError(t, err)
	assert.Empty(t, none)

	// Another subject's scope must not see the event.
	other := scope
	other.SubjectID = "web_user:someone-else"
	otherEvents, err := repo.ListEvents(ctx, other, time.Time{}, 0)
	require.NoError(t, err)
	assert.Empty(t, otherEvents, "scope isolation broken")
}

func TestLearningRepositoryMasteryUpsertFoldsInPlace(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()
	scope := learningTestScope()

	first := &types.MasteryState{
		TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug: "concept/decay", Logit: 1.0, EvidenceCount: 1, PositiveCount: 1,
		Stability: 14, LastEvidenceAt: time.Now(), FirstSeenAt: time.Now(),
	}
	require.NoError(t, repo.UpsertMastery(ctx, first))

	// Second fold on the same node updates, never duplicates.
	first.Logit = 2.6
	first.EvidenceCount = 2
	first.PositiveCount = 2
	require.NoError(t, repo.UpsertMastery(ctx, first))

	got, err := repo.GetMastery(ctx, scope, "concept/decay")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.InDelta(t, 2.6, got.Logit, 1e-9)
	assert.Equal(t, 2, got.EvidenceCount)

	missing, err := repo.GetMastery(ctx, scope, "concept/never-touched")
	require.NoError(t, err)
	assert.Nil(t, missing, "untouched node must read as (nil, nil)")

	all, err := repo.ListMastery(ctx, scope)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestLearningRepositoryPrefsUpsert(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()

	prefs, err := repo.GetSubjectPrefs(ctx, 10000, "web_user:test-subject")
	require.NoError(t, err)
	assert.Nil(t, prefs, "unset prefs must read as (nil, nil)")

	require.NoError(t, repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{
		TenantID: 10000, SubjectID: "web_user:test-subject", CollectDisabled: true,
	}))
	require.NoError(t, repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{
		TenantID: 10000, SubjectID: "web_user:test-subject", CollectDisabled: false,
	}))
	prefs, err = repo.GetSubjectPrefs(ctx, 10000, "web_user:test-subject")
	require.NoError(t, err)
	require.NotNil(t, prefs)
	assert.False(t, prefs.CollectDisabled, "second upsert must overwrite the first")
}

func TestLearningRepositoryEdgeAndQuizInventory(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()

	edge := &types.LearningEdge{
		TenantID: 10000, KnowledgeBaseID: "kb-1",
		FromSlug: "concept/vector-search", ToSlug: "concept/hybrid-rerank",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}
	require.NoError(t, repo.UpsertEdge(ctx, edge))
	edge.Confidence = 0.95
	require.NoError(t, repo.UpsertEdge(ctx, edge)) // same key updates

	edges, err := repo.ListEdges(ctx, 10000, "kb-1")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.InDelta(t, 0.95, edges[0].Confidence, 1e-9)

	item := &types.LearningQuizItem{
		TenantID: 10000, KnowledgeBaseID: "kb-1", Slug: "concept/rag",
		Question:   "What does SourceRefs store?",
		Options:    types.QuizOptions{"A": "chunk ids", "B": "doc refs", "C": "slugs", "D": "urls"},
		CorrectKey: "B", ChunkRefs: types.RefList{"chunk-1"},
	}
	require.NoError(t, repo.UpsertQuizItem(ctx, item))
	require.NotEmpty(t, item.ID)
	assert.Equal(t, types.LearningQuizStatusActive, item.Status)

	items, err := repo.ListQuizItems(ctx, 10000, "kb-1", "concept/rag")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "What does SourceRefs store?", items[0].Question)
	assert.Equal(t, "B", items[0].CorrectKey)
	require.Len(t, items[0].ChunkRefs, 1)

	// JSONB round trip: options survive Value/Scan.
	assert.Equal(t, "chunk ids", items[0].Options["A"])
}

func TestLearningRepositoryAttemptHistory(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()
	scope := learningTestScope()

	at := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	for i, correct := range []bool{false, true} {
		require.NoError(t, repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
			TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
			QuizItemID: "item-1", Slug: "concept/rag",
			ChosenKey: "B", IsCorrect: correct, AnsweredAt: at.Add(time.Duration(i) * time.Minute),
		}))
	}

	attempts, err := repo.ListAttempts(ctx, scope, "concept/rag")
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	assert.False(t, attempts[0].IsCorrect)
	assert.True(t, attempts[1].IsCorrect)
}

func TestLearningRepositoryListMasteryByKB(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()

	write := func(tenantID uint64, subjectID, kbID, slug string) {
		require.NoError(t, repo.UpsertMastery(ctx, &types.MasteryState{
			TenantID: tenantID, SubjectID: subjectID, KnowledgeBaseID: kbID, Slug: slug,
			Logit: 1.5, EvidenceCount: 2,
		}))
	}
	// Two subjects in the KB, plus foreign rows the query must not see:
	// another tenant (same kb) and another kb (same tenant).
	write(10000, "web_user:bob", "kb-1", "concept/zeta")
	write(10000, "web_user:alice", "kb-1", "concept/alpha")
	write(20000, "web_user:eve", "kb-1", "concept/alpha")
	write(10000, "web_user:alice", "kb-2", "concept/alpha")

	rows, err := repo.ListMasteryByKB(ctx, 10000, "kb-1")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// Deterministic order: subject then slug — alice/alpha before bob/zeta.
	assert.Equal(t, "web_user:alice", rows[0].SubjectID)
	assert.Equal(t, "concept/alpha", rows[0].Slug)
	assert.Equal(t, "web_user:bob", rows[1].SubjectID)
	assert.Equal(t, "concept/zeta", rows[1].Slug)
}

func TestLearningRepositoryListMaintenanceMarks(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	ctx := context.Background()

	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	events := []types.LearningEvent{
		// In window, in vocabulary: kept.
		{TenantID: 10000, SubjectID: "web_user:a", KnowledgeBaseID: "kb-1", Slug: "concept/rag",
			Type: types.LearningEventSelfAssessDownDocGap, OccurredAt: at.Add(-24 * time.Hour)},
		{TenantID: 10000, SubjectID: "web_user:b", KnowledgeBaseID: "kb-1", Slug: "concept/rag",
			Type: types.LearningEventSelfAssessUp, OccurredAt: at.Add(-48 * time.Hour)},
		// Out of the since cutoff: dropped.
		{TenantID: 10000, SubjectID: "web_user:a", KnowledgeBaseID: "kb-1", Slug: "concept/rag",
			Type: types.LearningEventSelfAssessDownAll, OccurredAt: at.Add(-40 * 24 * time.Hour)},
		// In window but not a self-assessment: dropped.
		{TenantID: 10000, SubjectID: "web_user:a", KnowledgeBaseID: "kb-1", Slug: "concept/rag",
			Type: types.LearningEventQuizCorrect, OccurredAt: at.Add(-time.Hour)},
		// Foreign scope: dropped.
		{TenantID: 20000, SubjectID: "web_user:e", KnowledgeBaseID: "kb-1", Slug: "concept/rag",
			Type: types.LearningEventSelfAssessUp, OccurredAt: at.Add(-time.Hour)},
		{TenantID: 10000, SubjectID: "web_user:a", KnowledgeBaseID: "kb-2", Slug: "concept/rag",
			Type: types.LearningEventSelfAssessUp, OccurredAt: at.Add(-time.Hour)},
	}
	for i := range events {
		require.NoError(t, repo.AppendEvent(ctx, &events[i]))
	}

	marks, err := repo.ListMaintenanceMarks(ctx, 10000, "kb-1", at.Add(-30*24*time.Hour))
	require.NoError(t, err)
	require.Len(t, marks, 2)
	// Newest first.
	assert.Equal(t, types.LearningEventSelfAssessDownDocGap, marks[0].Type)
	assert.Equal(t, types.LearningEventSelfAssessUp, marks[1].Type)
}
