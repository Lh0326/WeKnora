package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedProfileRows plants one row of every personal table for the subject so
// delete-atomicity assertions cover the full sweep surface.
func seedProfileRows(t *testing.T, db *gorm.DB, subjectID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, db.WithContext(ctx).Create(&types.LearningEvent{
		ID: "ev-1", TenantID: 10000, SubjectID: subjectID, KnowledgeBaseID: "kb-1",
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: 1, OccurredAt: now,
	}).Error)
	require.NoError(t, db.WithContext(ctx).Create(&types.MasteryState{
		TenantID: 10000, SubjectID: subjectID, KnowledgeBaseID: "kb-1", Slug: "concept/rag",
		Logit: 1.5, EvidenceCount: 2, LastEvidenceAt: now, FirstSeenAt: now,
	}).Error)
	require.NoError(t, db.WithContext(ctx).Create(&types.MemoryWikiMap{
		TenantID: 10000, SubjectID: subjectID, KnowledgeBaseID: "kb-1",
		NormalizedTopicKey: "rag", Slug: "concept/rag", TopicLabel: "RAG",
	}).Error)
	require.NoError(t, db.WithContext(ctx).Create(&types.LearningQuizAttempt{
		ID: "at-1", TenantID: 10000, SubjectID: subjectID, KnowledgeBaseID: "kb-1",
		QuizItemID: "q-1", Slug: "concept/rag", ChosenKey: "A", IsCorrect: true, AnsweredAt: now,
	}).Error)
	require.NoError(t, db.WithContext(ctx).Create(&types.LearningSkip{
		TenantID: 10000, SubjectID: subjectID, KnowledgeBaseID: "kb-1", Slug: "concept/rag",
		CreatedAt: now,
	}).Error)
}

// countRows is the rollback assertion helper: how many rows does the subject
// still own in the named table?
func countRows(t *testing.T, db *gorm.DB, table, subjectID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table(table).Where("subject_id = ?", subjectID).Count(&n).Error)
	return n
}

// TestDeleteProfileDataRollsBackOnMidTransactionFailure is the fault
// injection the review demanded: when any table's delete fails part-way
// through the sweep (here: a SQLite trigger aborts the learning_skips
// delete — the last table in the loop), NOTHING is removed, the opt-out is
// not recorded, and the epoch is not bumped. A crash between the sweep and
// the opt-out write can no longer leave a deleted profile without its
// resurrection guard.
func TestDeleteProfileDataRollsBackOnMidTransactionFailure(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	const subject = "web_user:rollback"
	seedProfileRows(t, db, subject)

	// Fault injection: abort every DELETE on learning_skips.
	require.NoError(t, db.Exec(
		"CREATE TRIGGER block_skips_delete BEFORE DELETE ON learning_skips " +
			"BEGIN SELECT RAISE(ABORT, 'injected failure'); END",
	).Error)

	err := repo.DeleteProfileData(ctx, 10000, subject, true)
	require.Error(t, err, "the injected fault must surface")

	// Everything survived: the transaction rolled back whole.
	for _, table := range []string{"learning_events", "mastery_states", "memory_wiki_map", "learning_quiz_attempts", "learning_skips"} {
		assert.EqualValues(t, 1, countRows(t, db, table, subject), "%s must survive the rolled-back delete", table)
	}
	prefs, err := repo.GetSubjectPrefs(ctx, subject)
	require.NoError(t, err)
	assert.Nil(t, prefs, "opt-out must not be recorded when the sweep failed")
	epoch, err := repo.GetSubjectEpoch(ctx, subject)
	require.NoError(t, err)
	assert.EqualValues(t, 0, epoch, "epoch must not advance when the delete failed")

	// Remove the fault: the same call now succeeds atomically.
	require.NoError(t, db.Exec("DROP TRIGGER block_skips_delete").Error)
	require.NoError(t, repo.DeleteProfileData(ctx, 10000, subject, true))
	for _, table := range []string{"learning_events", "mastery_states", "memory_wiki_map", "learning_quiz_attempts", "learning_skips"} {
		assert.EqualValues(t, 0, countRows(t, db, table, subject), "%s must be swept", table)
	}
	prefs, err = repo.GetSubjectPrefs(ctx, subject)
	require.NoError(t, err)
	require.NotNil(t, prefs)
	assert.True(t, prefs.CollectDisabled, "opt-out must land with the successful sweep")
	epoch, err = repo.GetSubjectEpoch(ctx, subject)
	require.NoError(t, err)
	assert.EqualValues(t, 1, epoch, "epoch must bump exactly once per delete")
}

// TestDeleteProfileDataEpochBumpsPerDelete: repeated deletes keep counting
// generations — a writer that captured epoch 1 must still be fenced after
// the second delete even though collection may have been re-enabled in
// between.
func TestDeleteProfileDataEpochBumpsPerDelete(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	const subject = "web_user:gen"

	for i := 1; i <= 3; i++ {
		require.NoError(t, repo.DeleteProfileData(ctx, 10000, subject, i%2 == 1))
		epoch, err := repo.GetSubjectEpoch(ctx, subject)
		require.NoError(t, err)
		assert.EqualValues(t, i, epoch)
	}
	// The opt-out flag reflects only the LAST call's choice, but the epoch
	// counts every delete.
	prefs, _ := repo.GetSubjectPrefs(ctx, subject)
	require.NotNil(t, prefs)
	assert.True(t, prefs.CollectDisabled)
}

// TestApplyTopicMappingDiscardsStaleEpoch is the in-flight-resurrection
// fence: a topic adjudication that captured epoch 0 lands its result only
// if no delete happened meanwhile; after the delete bumps the epoch to 1
// the same write is discarded whole — no mapping, no event, no fold.
func TestApplyTopicMappingDiscardsStaleEpoch(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	const subject = "web_user:inflight"
	scope := interfaces.LearningScope{TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1"}

	mapping := func() *types.MemoryWikiMap {
		return &types.MemoryWikiMap{
			TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1",
			NormalizedTopicKey: "rag", Slug: "concept/rag", TopicLabel: "RAG",
			Confidence: 0.9, DecidedBy: types.LearningMapDecidedByLLM,
		}
	}
	event := func() *types.LearningEvent {
		return &types.LearningEvent{
			TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1",
			Slug: "concept/rag", Type: types.LearningEventTopicSignal,
			Weight: 1.2, OccurredAt: time.Now(),
		}
	}
	fold := func(existing *types.MasteryState) types.MasteryState {
		if existing == nil {
			existing = &types.MasteryState{
				TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1", Slug: "concept/rag",
			}
		}
		existing.Logit += 1.2
		return *existing
	}

	// Fresh epoch 0 write lands: mapping + event + fold all visible.
	require.NoError(t, repo.ApplyTopicMapping(ctx, 0, scope, mapping(), event(), fold))
	assert.EqualValues(t, 1, countRows(t, db, "memory_wiki_map", subject))
	assert.EqualValues(t, 1, countRows(t, db, "learning_events", subject))
	row, err := repo.GetMastery(ctx, scope, "concept/rag")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.InDelta(t, 1.2, row.Logit, 1e-9)

	// The delete bumps the epoch mid-flight.
	require.NoError(t, repo.DeleteProfileData(ctx, 10000, subject, false))
	assert.EqualValues(t, 0, countRows(t, db, "learning_events", subject), "delete must sweep the event")

	// The stale writer's result is discarded whole.
	err = repo.ApplyTopicMapping(ctx, 0, scope, mapping(), event(), fold)
	require.True(t, errors.Is(err, interfaces.ErrLearningEpochAdvanced),
		"stale epoch must fence the write, got: %v", err)
	assert.EqualValues(t, 0, countRows(t, db, "memory_wiki_map", subject), "no resurrection via mapping")
	assert.EqualValues(t, 0, countRows(t, db, "learning_events", subject), "no resurrection via event")
	row, err = repo.GetMastery(ctx, scope, "concept/rag")
	require.NoError(t, err)
	assert.Nil(t, row, "no resurrection via mastery fold")

	// A writer that re-reads the new epoch succeeds again — the fence
	// blocks stale writes, not collection itself.
	require.NoError(t, repo.ApplyTopicMapping(ctx, 1, scope, mapping(), event(), fold))
	assert.EqualValues(t, 1, countRows(t, db, "memory_wiki_map", subject))
}

// TestApplyTopicMappingMappingOnlySkipsEventAndFold: the deduped-signal
// case — the mapping upsert commits while no projection event or fold is
// written (nil event, nil fold).
func TestApplyTopicMappingMappingOnlySkipsEventAndFold(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	const subject = "web_user:dedup"
	scope := interfaces.LearningScope{TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1"}

	m := &types.MemoryWikiMap{
		TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1",
		NormalizedTopicKey: "rag", Slug: "concept/rag", TopicLabel: "RAG",
		Confidence: 0.9, DecidedBy: types.LearningMapDecidedByLLM,
	}
	require.NoError(t, repo.ApplyTopicMapping(ctx, 0, scope, m, nil, nil))
	assert.EqualValues(t, 1, countRows(t, db, "memory_wiki_map", subject))
	assert.EqualValues(t, 0, countRows(t, db, "learning_events", subject))
	row, err := repo.GetMastery(ctx, scope, "concept/rag")
	require.NoError(t, err)
	assert.Nil(t, row)

	require.Error(t, repo.ApplyTopicMapping(ctx, 0, scope, nil, nil, nil),
		"nil mapping is a programming error, not a silent no-op")
}
