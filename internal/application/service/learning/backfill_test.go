package learning

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestRunBackfillReplaysAffinityIdempotently(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Rag Doc"}))
	wiki.addPage(testKB, testWikiPage("concept/decay", nil, []string{"d1|Decay Doc", "d2|Other Doc"}))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")

	used := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	repo.affinity = []types.MemoryDocAffinity{
		// alice used d1 (touches rag + decay) and d2 (touches decay).
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1", Hits: 5, LastUsedAt: used},
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d2", Hits: 1, LastUsedAt: used.Add(time.Hour)},
		// bob opted out: must not be backfilled.
		{TenantID: 1, SubjectID: "web_user:bob", KnowledgeBaseID: testKB, KnowledgeID: "d1", Hits: 3, LastUsedAt: used},
	}
	repo.prefs["web_user:bob"] = &types.LearningSubjectPrefs{CollectDisabled: true}

	if err := svc.RunBackfill(context.Background()); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}
	events := repo.snapshotEvents()
	if len(events) != 2 {
		t.Fatalf("first backfill events = %d, want 2 (alice only): %+v", len(events), events)
	}
	bySlug := map[string]types.LearningEvent{}
	for _, e := range events {
		if e.Type != types.LearningEventBackfillCite {
			t.Errorf("type = %q, want backfill_cite", e.Type)
		}
		if e.SubjectID != "web_user:alice" {
			t.Errorf("subject = %q, want alice (bob opted out)", e.SubjectID)
		}
		bySlug[e.Slug] = e
	}

	// Weight: doc-grained discount with saturation min(hits,8)/8.
	rag := bySlug["concept/rag"]
	wantRag := WeightAnswerCite * BackfillDiscount * (math.Min(1, backfillMaxHits) / backfillMaxHits)
	if math.Abs(rag.Weight-wantRag) > 1e-12 {
		t.Errorf("rag weight = %v, want %v", rag.Weight, wantRag)
	}
	if !rag.OccurredAt.Equal(used) {
		t.Errorf("rag occurred_at = %v, want affinity last_used %v", rag.OccurredAt, used)
	}
	decay := bySlug["concept/decay"]
	wantDecay := WeightAnswerCite * BackfillDiscount * (math.Min(2, backfillMaxHits) / backfillMaxHits)
	if math.Abs(decay.Weight-wantDecay) > 1e-12 {
		t.Errorf("decay weight = %v, want %v (two docs)", decay.Weight, wantDecay)
	}
	if !decay.OccurredAt.Equal(used.Add(time.Hour)) {
		t.Errorf("decay occurred_at = %v, want latest last_used", decay.OccurredAt)
	}

	// Idempotency proof: a full re-run appends nothing.
	before := repo.appends.Load()
	if err := svc.RunBackfill(context.Background()); err != nil {
		t.Fatalf("backfill re-run failed: %v", err)
	}
	if repo.appends.Load() != before {
		t.Fatalf("re-run appended %d new events, want zero", repo.appends.Load()-before)
	}
	if len(repo.snapshotEvents()) != 2 {
		t.Fatalf("event count after re-run = %d, want still 2", len(repo.snapshotEvents()))
	}
}

// TestRunBackfillDoesNotResurrectDeletedProfile is the deletion tombstone
// regression: backfill idempotency used to live in the backfill_cite event
// rows themselves, which DeleteLearningDataBySubject removes — so deleting a
// profile and restarting the server re-derived the same events from the
// still-present affinity rows and the "deleted" profile came back to life.
// The mark table must survive the subject-level delete and block the re-run.
func TestRunBackfillDoesNotResurrectDeletedProfile(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Rag Doc"}))
	wiki.addPage(testKB, testWikiPage("concept/decay", nil, []string{"d1|Decay Doc"}))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")

	used := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	repo.affinity = []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1", Hits: 5, LastUsedAt: used},
	}
	if err := svc.RunBackfill(context.Background()); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}
	if n := len(repo.snapshotEvents()); n != 2 {
		t.Fatalf("first backfill events = %d, want 2", n)
	}

	// The user deletes their learning profile (without opting out of
	// collection): events, mastery and attempts are gone, affinity is NOT
	// (it belongs to the memory subsystem, a different deletion surface).
	if err := repo.DeleteLearningDataBySubject(context.Background(), "web_user:alice"); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("events after profile delete = %d, want 0", n)
	}

	// Server restart: the reconcile runner rides RunBackfill on startup.
	// Without the surviving mark this re-appends the whole history.
	if err := svc.RunBackfill(context.Background()); err != nil {
		t.Fatalf("post-delete backfill failed: %v", err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("post-delete backfill resurrected %d events, want 0 (deleted must stay deleted)", n)
	}
}

// TestRunBackfillMarksNewScopeOnly: a scope whose mark was swept with its KB
// stays eligible; a scope marked under a live KB is skipped even when its
// event history is empty (fresh extraction, no organic events yet).
func TestRunBackfillMarkSurvivesSubjectDeleteOnly(t *testing.T) {
	repo := newStubRepo()
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	repo.MarkBackfillDone(context.Background(), scope)

	done, err := repo.BackfillDone(context.Background(), scope)
	if err != nil || !done {
		t.Fatalf("BackfillDone after mark = %v, %v; want true, nil", done, err)
	}

	// Subject-level delete keeps the mark (resurrection guard)…
	if err := repo.DeleteLearningDataBySubject(context.Background(), "web_user:alice"); err != nil {
		t.Fatal(err)
	}
	if done, _ := repo.BackfillDone(context.Background(), scope); !done {
		t.Fatal("subject delete must keep the backfill mark")
	}

	// …the KB orphan sweep clears it with the rest of the KB's data.
	if err := repo.DeleteLearningDataByKB(context.Background(), 1, testKB); err != nil {
		t.Fatal(err)
	}
	if done, _ := repo.BackfillDone(context.Background(), scope); done {
		t.Fatal("KB sweep must clear the backfill mark")
	}
}

func TestRunBackfillNoOpWhenGateClosed(t *testing.T) {
	repo := newStubRepo()
	svc := testService(repo, nil)
	t.Setenv("LEARNING_ENABLE", "")

	repo.affinity = []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1", Hits: 5},
	}
	if err := svc.RunBackfill(context.Background()); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}
	if repo.appends.Load() != 0 {
		t.Fatalf("gate closed backfill appended %d events", repo.appends.Load())
	}
}

// TestRunBackfillSkipsOrphanKB: affinity rows of a deleted KB linger in
// the memory subsystem, but the backfill must not resurrect the learning
// data the orphan sweep removed — the KB guard short-circuits the scope.
func TestRunBackfillSkipsOrphanKB(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Rag Doc"}))
	// kbRepo resolves nothing: every KB is an orphan.
	svc := NewService(repo, wiki, nil, nil, &stubKBRepo{kbs: map[string]*types.KnowledgeBase{}}, nil)
	t.Setenv("LEARNING_ENABLE", "true")

	repo.affinity = []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1", Hits: 5,
			LastUsedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
	}
	if err := svc.RunBackfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("backfill events = %d, want 0 for orphan KB", n)
	}
}
