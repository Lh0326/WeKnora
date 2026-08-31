package learning

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
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
