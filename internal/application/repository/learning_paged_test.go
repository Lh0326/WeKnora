package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListEventsPagedDeterministicAcrossPages：评审 P2-A 回归——同时间戳的
// 一批事件按 id 决胜，页边界两侧顺序稳定；乱序插入不影响规范顺序；分页
// 拼起来等于全量。
func TestListEventsPagedDeterministicAcrossPages(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	scope := learningTestScope()

	// 10 events: five share one timestamp (tie batch), the rest spread out.
	// Inserted in REVERSE canonical order to prove the reader sorts, not the
	// inserter.
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	var inserted []types.LearningEvent
	for i := 9; i >= 0; i-- {
		at := base.Add(time.Duration(i) * time.Minute)
		if i%2 == 0 {
			at = base.Add(2 * time.Minute) // the tie batch
		}
		ev := types.LearningEvent{
			ID:  "ev-" + string(rune('a'+i)),
			TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: 1,
			OccurredAt: at,
		}
		require.NoError(t, db.WithContext(ctx).Create(&ev).Error)
		inserted = append(inserted, ev)
	}

	// Page through with a tiny page size so the tie batch straddles pages.
	var paged []types.LearningEvent
	var after time.Time
	var afterID string
	for {
		page, err := repo.ListEventsPaged(ctx, scope, after, afterID, 3)
		require.NoError(t, err)
		paged = append(paged, page...)
		if len(page) < 3 {
			break
		}
		last := page[len(page)-1]
		after, afterID = last.OccurredAt, last.ID
	}
	require.Len(t, paged, 10)

	// Canonical order: occurred_at ASC, id ASC inside ties.
	for i := 1; i < len(paged); i++ {
		prev, cur := paged[i-1], paged[i]
		if !prev.OccurredAt.Equal(cur.OccurredAt) {
			assert.True(t, prev.OccurredAt.Before(cur.OccurredAt), "page %d: time order broken", i)
			continue
		}
		assert.Less(t, prev.ID, cur.ID, "page %d: tie order by id broken", i)
	}
	// The tie batch itself: all five even-indexed events share a timestamp.
	ties := 0
	for _, ev := range paged {
		if ev.OccurredAt.Equal(base.Add(2 * time.Minute)) {
			ties++
		}
	}
	assert.Equal(t, 5, ties)
}

// TestMigrateMasteryAtomicRollback：评审 P3 回归——迁移事务的最后一步失败时
// （这里注入触发器阻断源行删除），目标行 upsert 与 attempts 改挂必须一并
// 回滚：一个人的掌握度不能同时出现在两行，直接证据不能悬在死 slug 下。
func TestMigrateMasteryAtomicRollback(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db).(*learningRepository)
	ctx := context.Background()
	const subject = "web_user:migrate"
	scope := interfaces.LearningScope{TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1"}

	old := &types.MasteryState{
		TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1", Slug: "concept/old",
		Logit: 2, EvidenceCount: 3, LastEvidenceAt: time.Now(), FirstSeenAt: time.Now(),
	}
	require.NoError(t, db.WithContext(ctx).Create(old).Error)
	attempt := &types.LearningQuizAttempt{
		ID: "at-m", TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1",
		QuizItemID: "q1", Slug: "concept/old", ChosenKey: "A", IsCorrect: true, AnsweredAt: time.Now(),
	}
	require.NoError(t, db.WithContext(ctx).Create(attempt).Error)

	// Fault injection: abort the source-row retirement (the final step).
	require.NoError(t, db.Exec(
		"CREATE TRIGGER block_mastery_delete BEFORE DELETE ON mastery_states " +
			"BEGIN SELECT RAISE(ABORT, 'injected failure'); END",
	).Error)

	target := &types.MasteryState{
		TenantID: 10000, SubjectID: subject, KnowledgeBaseID: "kb-1", Slug: "concept/new",
		Logit: 2, EvidenceCount: 3,
	}
	err := repo.MigrateMastery(ctx, scope, "concept/old", "concept/new", target)
	require.Error(t, err, "injected fault must surface")

	// Whole transaction rolled back: no target row, attempt still on the
	// old slug, source row intact (no duplicated state).
	var n int64
	require.NoError(t, db.Model(&types.MasteryState{}).Where(
		"subject_id = ? AND slug = ?", subject, "concept/new").Count(&n).Error)
	assert.EqualValues(t, 0, n, "target row must not survive the failed move")
	require.NoError(t, db.Model(&types.LearningQuizAttempt{}).Where(
		"subject_id = ? AND slug = ?", subject, "concept/old").Count(&n).Error)
	assert.EqualValues(t, 1, n, "attempt must stay on the old slug when the move failed")

	// Clear the fault: the same move succeeds and re-tags the attempt.
	require.NoError(t, db.Exec("DROP TRIGGER block_mastery_delete").Error)
	require.NoError(t, repo.MigrateMastery(ctx, scope, "concept/old", "concept/new", target))
	require.NoError(t, db.Model(&types.MasteryState{}).Where(
		"subject_id = ? AND slug = ?", subject, "concept/new").Count(&n).Error)
	assert.EqualValues(t, 1, n)
	require.NoError(t, db.Model(&types.MasteryState{}).Where(
		"subject_id = ? AND slug = ?", subject, "concept/old").Count(&n).Error)
	assert.EqualValues(t, 0, n, "source row must be retired by the successful move")
	require.NoError(t, db.Model(&types.LearningQuizAttempt{}).Where(
		"subject_id = ? AND slug = ?", subject, "concept/new").Count(&n).Error)
	assert.EqualValues(t, 1, n, "attempt must follow the node onto the live slug")
}
