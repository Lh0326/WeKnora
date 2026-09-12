package learning

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TestListAllEventsPagedCoversBeyondSinglePage（评审 P2-A/P3 回归）：分页收
// 集必须覆盖超过单页的全量历史——旧的 10,000 条单次抓取上限只能整段跳过
// 大历史主体；现在游标循环读完为止，顺序为规范 (occurred_at, id) 序。
func TestListAllEventsPagedCoversBeyondSinglePage(t *testing.T) {
	repo := newStubRepo()
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

	// 25 events, appended in shuffled order, several sharing timestamps.
	order := []int{7, 2, 12, 0, 3, 19, 5, 8, 1, 24, 11, 4, 17, 6, 9, 14, 20, 10, 13, 18, 15, 22, 16, 21, 23}
	for _, i := range order {
		at := base.Add(time.Duration(i) * time.Minute)
		if i%3 == 0 {
			at = base.Add(30 * time.Minute) // tie batch
		}
		ev := &types.LearningEvent{
			ID:       "ev-" + string(rune('a'+i)),
			TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: 1,
			OccurredAt: at,
		}
		if err := repo.AppendEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	all, err := listAllEventsPaged(context.Background(), repo, scope, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 25 {
		t.Fatalf("paged collection must cover the whole history, got %d of 25", len(all))
	}
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if !prev.OccurredAt.Equal(cur.OccurredAt) {
			if prev.OccurredAt.After(cur.OccurredAt) {
				t.Fatalf("canonical order broken at %d: %v after %v", i, prev.OccurredAt, cur.OccurredAt)
			}
			continue
		}
		if prev.ID > cur.ID {
			t.Fatalf("tie order broken at %d: %s before %s", i, prev.ID, cur.ID)
		}
	}
}

// TestReconcileConvergesLateArrivingEvent（评审 P2-A 晚到事件回归）：一条
// occurred_at 早于既有历史的事件在事后到达并被在线折叠后，每日重放（如今
// 是全量分页、规范顺序）必须把持久化 fold 收敛到按时间顺序重放的结果——
// 在线到达顺序与重放顺序的差异由确定性重放抹平。
func TestReconcileConvergesLateArrivingEvent(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/rag", PageType: "concept", ChunkRefs: types.StringArray{"c1"}})
	runner := newTestRunner(repo, wiki, kbsWithTestKB())
	ctx := context.Background()
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}

	now := time.Now()
	// Two events folded in order, then a LATE one with an older timestamp.
	mk := func(id string, at time.Time, w float64) *types.LearningEvent {
		return &types.LearningEvent{
			ID: id, TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: w, OccurredAt: at,
		}
	}

	// Fold in arrival order: E1, E2, then the late E0 (older than both).
	state := FoldState{}
	for _, ev := range []*types.LearningEvent{
		mk("e1", now.Add(-2*time.Hour), 1),
		mk("e2", now.Add(-1*time.Hour), -2),
		mk("e0", now.Add(-5*time.Hour), 2), // the late arrival
	} {
		if err := repo.AppendEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
		state = FoldEvent(state, Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
	}
	row := &types.MasteryState{
		TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, Slug: "concept/rag",
	}
	state.ApplyTo(row)
	if err := repo.UpsertMastery(ctx, row); err != nil {
		t.Fatal(err)
	}

	// The negative update absorbs elapsed forgetting from a different prior
	// state. This changes logit, while the seen bounds alone would not detect it.
	expected := FoldState{}
	for _, ev := range []*types.LearningEvent{
		mk("e0", now.Add(-5*time.Hour), 2),
		mk("e1", now.Add(-2*time.Hour), 1),
		mk("e2", now.Add(-1*time.Hour), -2),
	} {
		expected = FoldEvent(expected, Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
	}

	if foldMatches(state, expected) {
		t.Fatal("test fixture must contain real arrival-order drift")
	}
	if err := runner.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetMastery(ctx, scope, "concept/rag")
	if err != nil || got == nil {
		t.Fatalf("mastery row missing after reconcile: %v", err)
	}
	if !foldMatches(StateFromModel(got), expected) {
		t.Fatalf("late event not converged: got %+v, want anchors first=%v last=%v count=%d",
			got, expected.FirstSeenAt, expected.LastEvidenceAt, expected.EvidenceCount)
	}
}
