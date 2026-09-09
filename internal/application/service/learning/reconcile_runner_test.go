package learning

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newTestRunner builds a runner with a zero startup delay and a long
// interval so tests can drive runOnce deterministically without waiting.
// kbRepo maps kbID → KB (absent key = deleted KB, the orphan case).
func newTestRunner(repo *stubLearningRepo, wiki *stubWikiRepo, kbs *stubKBRepo) *ReconcileRunner {
	if kbs == nil {
		kbs = &stubKBRepo{kbs: map[string]*types.KnowledgeBase{}}
	}
	r := NewReconcileRunner(repo, wiki, kbs)
	r.startupDelay = 0
	r.interval = time.Hour
	return r
}

// TestReconcileRunOnceSweepsOrphanKB: a KB that no longer resolves
// (soft-deleted upstream) must have its learning data swept instead of
// slug-reconciled — its lingering wiki_pages would otherwise make stale
// slugs look live.
func TestReconcileRunOnceSweepsOrphanKB(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	kbs := &stubKBRepo{kbs: map[string]*types.KnowledgeBase{}} // testKB absent = deleted
	runner := newTestRunner(repo, wiki, kbs)

	orphan := &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
		Logit: 1.0, EvidenceCount: 1, PositiveCount: 1,
	}
	if err := repo.UpsertMastery(context.Background(), orphan); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}
	if len(repo.kbSweeps) != 1 || repo.kbSweeps[0][1] != testKB {
		t.Fatalf("kbSweeps = %v, want one sweep of %s", repo.kbSweeps, testKB)
	}
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	if got, _ := repo.GetMastery(context.Background(), scope, "concept/rag"); got != nil {
		t.Fatal("orphan KB mastery must be swept")
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("sweep must not run slug reconciliation, deletes = %v", repo.deleted)
	}
}

// kbsWithTestKB marks the fixture KB as existing so the orphan sweep
// leaves it alone in reconciliation tests.
func kbsWithTestKB() *stubKBRepo {
	return &stubKBRepo{kbs: map[string]*types.KnowledgeBase{testKB: {ID: testKB, TenantID: 1}}}
}

func TestReconcileRunOnceMigratesStaleSlug(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	// The page was renamed: "concept/retrieval" is live and keeps the old
	// name as an alias; "concept/rag" no longer exists.
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/retrieval", PageType: "concept",
		ChunkRefs: types.StringArray{"c1"}, Aliases: types.StringArray{"rag"},
	})
	runner := newTestRunner(repo, wiki, kbsWithTestKB())

	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	old := &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
		Logit: 2.0, EvidenceCount: 2, PositiveCount: 2, Stability: 24,
		LastEvidenceAt: time.Now(), FirstSeenAt: time.Now(),
	}
	if err := repo.UpsertMastery(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	if got, _ := repo.GetMastery(context.Background(), scope, "concept/rag"); got != nil {
		t.Fatal("stale row must be retired")
	}
	if got, _ := repo.GetMastery(context.Background(), scope, "concept/retrieval"); got == nil {
		t.Fatal("migrated row missing under live slug")
	} else if got.Logit != 2.0 || got.EvidenceCount != 2 {
		t.Fatalf("migration altered the fold: %+v", got)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "concept/rag" {
		t.Fatalf("deletes = %v, want [concept/rag]", repo.deleted)
	}
}

// TestReconcileRunOnceMergesWhenTargetHasState is the rename-with-continued-
// usage regression: the stale slug migrates onto a live target that already
// carries post-rename evidence, and the result must be the full event replay
// (clamp included), never the stale fold overwriting the live one.
func TestReconcileRunOnceMergesWhenTargetHasState(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	// Renamed page: "concept/retrieval" is live and keeps "rag" as an alias.
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/retrieval", PageType: "concept",
		ChunkRefs: types.StringArray{"c1"}, Aliases: types.StringArray{"rag"},
	})
	runner := newTestRunner(repo, wiki, kbsWithTestKB())

	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Pre-rename history under the stale slug…
	for i, w := range []float64{WeightAnswerCite, WeightAnswerCite} {
		if err := repo.AppendEvent(context.Background(), &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite,
			Weight: w, OccurredAt: base.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// …and post-rename evidence under the live slug.
	if err := repo.AppendEvent(context.Background(), &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/retrieval", Type: types.LearningEventQuizCorrect,
		Weight: WeightQuizCorrect, OccurredAt: base.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Fold both sides the way the collector would have.
	for _, slug := range []string{"concept/rag", "concept/retrieval"} {
		events, _ := repo.ListEvents(context.Background(), scope, time.Time{}, 0)
		state := FoldState{}
		row := &types.MasteryState{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: slug,
		}
		for _, ev := range events {
			if ev.Slug == slug {
				state = FoldEvent(state, Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
			}
		}
		state.ApplyTo(row)
		if err := repo.UpsertMastery(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	if err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	if got, _ := repo.GetMastery(context.Background(), scope, "concept/rag"); got != nil {
		t.Fatal("stale row must be retired")
	}
	got, _ := repo.GetMastery(context.Background(), scope, "concept/retrieval")
	if got == nil {
		t.Fatal("merged row missing under live slug")
	}
	// Replay of 2 citations + 1 quiz_correct: 1.0+1.0+2.2 = 4.2 → clamped to
	// the cap. An overwrite with the stale fold would read 2.0/2 evidence.
	if got.Logit != LogitCap {
		t.Fatalf("merged logit = %v, want %v (replay with clamp)", got.Logit, LogitCap)
	}
	if got.EvidenceCount != 3 || got.PositiveCount != 3 {
		t.Fatalf("merged counters = %d/%d, want 3/3", got.EvidenceCount, got.PositiveCount)
	}
	if want := derivedStability(3, 0); math.Abs(got.Stability-want) > 1e-9 {
		t.Fatalf("merged stability = %v, want %v", got.Stability, want)
	}
}

func TestReconcileRunOnceLeavesLiveSlugsAlone(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, nil))
	runner := newTestRunner(repo, wiki, kbsWithTestKB())

	row := &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
		Logit: 1.0, EvidenceCount: 1, PositiveCount: 1,
	}
	if err := repo.UpsertMastery(context.Background(), row); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("live slug was touched: deletes=%v", repo.deleted)
	}
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	if got, _ := repo.GetMastery(context.Background(), scope, "concept/rag"); got == nil {
		t.Fatal("live row must stay")
	}
}

func TestReconcileRunOnceKeepsUnmatchedForNextRound(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}} // no pages at all
	runner := newTestRunner(repo, wiki, kbsWithTestKB())

	row := &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/orphan",
		Logit: 1.0, EvidenceCount: 1, PositiveCount: 1,
	}
	if err := repo.UpsertMastery(context.Background(), row); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	if got, _ := repo.GetMastery(context.Background(), scope, "concept/orphan"); got == nil {
		t.Fatal("unmatched row must be kept for the next round")
	}
}

func TestReconcileRunnerStartNoOpWhenGateClosed(t *testing.T) {
	repo := newStubRepo()
	runner := newTestRunner(repo, nil, nil)
	svc := testService(repo, nil)
	t.Setenv("LEARNING_ENABLE", "")

	runner.Start(context.Background(), svc)
	if runner.Started() {
		t.Fatal("runner must not start with the gate closed")
	}
	// Start is a no-op, Stop must therefore not hang on a never-opened
	// done channel either.
	runner.Stop()
}

func TestKebabSlugMirrorsWikiRules(t *testing.T) {
	cases := map[string]string{
		"RAG Pipeline":   "rag-pipeline",
		"lazy_decay":     "lazy-decay",
		"  spaced  out ": "spaced-out",
		"Mix--Doubles":   "mix-doubles",
		"Drop!Punct?":    "droppunct",
		"中文主题":           "中文主题",
	}
	for in, want := range cases {
		if got := kebabSlug(in); got != want {
			t.Errorf("kebabSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReconcileRunOnceRepairsFoldDrift: a mastery row that lost a
// contribution — the crash window between AppendEvent and foldOne — is
// rewritten from the from-scratch replay, and zero-weight events (deduped
// re-reads) must not count as evidence while replaying.
func TestReconcileRunOnceRepairsFoldDrift(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Rag Doc"}))
	runner := newTestRunner(repo, wiki, kbsWithTestKB())
	ctx := context.Background()
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	append := func(slug, evType string, weight float64, at time.Time) {
		t.Helper()
		if err := repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: slug, Type: evType, Weight: weight, OccurredAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	append("concept/rag", types.LearningEventAnswerCite, WeightAnswerCite, base)
	append("concept/rag", types.LearningEventAnswerCite, WeightAnswerCite, base.Add(time.Hour))
	append("concept/rag", types.LearningEventWikiToolRead, 0, base.Add(2*time.Hour))
	// The third citation's fold was lost (crash between append and fold).
	append("concept/rag", types.LearningEventAnswerCite, WeightAnswerCite, base.Add(3*time.Hour))

	// Persisted state reflects only the first two citations.
	if err := repo.UpsertMastery(ctx, &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
		Logit: 2 * WeightAnswerCite, EvidenceCount: 2, PositiveCount: 2,
		Stability:      derivedStability(2, 0),
		LastEvidenceAt: base.Add(time.Hour), FirstSeenAt: base,
	}); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(ctx); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	got, err := repo.GetMastery(ctx, scope, "concept/rag")
	if err != nil || got == nil {
		t.Fatalf("repaired row missing: %v, %v", got, err)
	}
	want := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base.Add(time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base.Add(3 * time.Hour)},
	})
	if got.EvidenceCount != want.EvidenceCount || got.PositiveCount != want.PositiveCount {
		t.Fatalf("repaired counters = %+v, want replay %+v", got, want)
	}
	if math.Abs(got.Logit-want.Logit) > 1e-9 {
		t.Fatalf("repaired logit = %v, want %v", got.Logit, want.Logit)
	}
	if !got.LastEvidenceAt.Equal(want.LastEvidenceAt) {
		t.Fatalf("repaired last_evidence_at = %v, want %v", got.LastEvidenceAt, want.LastEvidenceAt)
	}
}

// TestReconcileRunOnceWritesMissingFold: events exist for a node whose row
// was never written (first fold lost). The audit walks the subject through
// its other folded node and must materialise the missing fold exactly.
func TestReconcileRunOnceWritesMissingFold(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Rag Doc"}))
	wiki.addPage(testKB, testWikiPage("concept/decay", []string{"c1"}, []string{"d1|Decay Doc"}))
	runner := newTestRunner(repo, wiki, kbsWithTestKB())
	ctx := context.Background()
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/decay", Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite,
		OccurredAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	// The subject is visible to the walk through this other folded node…
	if err := repo.UpsertMastery(ctx, &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
		Logit: WeightAnswerCite, EvidenceCount: 1, PositiveCount: 1,
		Stability: derivedStability(1, 0), LastEvidenceAt: base, FirstSeenAt: base,
	}); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(ctx); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	got, err := repo.GetMastery(ctx, scope, "concept/decay")
	if err != nil || got == nil {
		t.Fatalf("missing fold not materialised: %v, %v", got, err)
	}
	if got.EvidenceCount != 1 || math.Abs(got.Logit-WeightAnswerCite) > 1e-9 {
		t.Fatalf("materialised fold = %+v, want one folded citation", got)
	}
}

// TestReconcileRunOnceNoRewriteOfConvergedState: a subject whose folds
// already equal the replay (including zero-weight events in history) must
// come out of the audit untouched — drift repair never manufactures writes.
func TestReconcileRunOnceNoRewriteOfConvergedState(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Rag Doc"}))
	runner := newTestRunner(repo, wiki, kbsWithTestKB())
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite,
		OccurredAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventWikiToolRead, Weight: 0,
		OccurredAt: base.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	state := FoldEvent(FoldState{}, Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base})
	row := &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
	}
	state.ApplyTo(row)
	if err := repo.UpsertMastery(ctx, row); err != nil {
		t.Fatal(err)
	}

	// The seeding upsert itself counts; measure from here.
	before := repo.masteryUpserts.Load()
	if err := runner.runOnce(ctx); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}
	if n := repo.masteryUpserts.Load() - before; n != 0 {
		t.Fatalf("converged state rewritten %d times, want zero", n)
	}
}

// TestReconcileMovesSkipOnlySubject: the P1 regression lock. A subject who
// ONLY ever skipped a node (no folded mastery — the "看一眼就会了"
// archetype) must still receive alias repair: a rename that goes unrepaired
// would silently resurrect the retired recommendation.
func TestReconcileMovesSkipOnlySubject(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	// The skipped page was renamed after the skip was declared.
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/retrieval", PageType: "concept",
		ChunkRefs: types.StringArray{"c1"}, Aliases: types.StringArray{"rag"},
	})
	runner := newTestRunner(repo, wiki, kbsWithTestKB())

	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	declared := time.Now().Add(-24 * time.Hour)
	if err := repo.AddSkip(context.Background(), scope, "concept/rag", declared); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce failed: %v", err)
	}

	skips, err := repo.ListSkips(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 1 {
		t.Fatalf("skips = %v, want exactly the migrated one", skips)
	}
	if at, ok := skips["concept/retrieval"]; !ok || at != declared {
		t.Fatalf("skip must move to the live slug keeping its declared-at, got %v", skips)
	}
	if _, ok := skips["concept/rag"]; ok {
		t.Fatal("stale skip slug must be retired")
	}
}


// TestReconcileRepairsEdges: the F2 storage-side lock. Edges whose
// endpoints drifted with a rename follow the alias index; edges pointing
// at pages that resolve nowhere are dropped — neither may keep gating the
// target forever.
func TestReconcileRepairsEdges(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	// Live page renamed from "rag"; the "ghost" page no longer exists.
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/retrieval", PageType: "concept",
		ChunkRefs: types.StringArray{"c1"}, Aliases: types.StringArray{"rag"},
	})
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/user", PageType: "concept"})
	runner := newTestRunner(repo, wiki, kbsWithTestKB())

	ctx := context.Background()
	if err := repo.UpsertEdge(ctx, &types.LearningEdge{
		TenantID: 1, KnowledgeBaseID: testKB,
		FromSlug: "concept/rag", ToSlug: "concept/user",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertEdge(ctx, &types.LearningEdge{
		TenantID: 1, KnowledgeBaseID: testKB,
		FromSlug: "concept/ghost", ToSlug: "concept/user",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	if err := runner.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	edges, err := repo.ListEdges(ctx, 1, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("one edge must survive (renamed, not dropped), got %v", edges)
	}
	if edges[0].FromSlug != "concept/retrieval" || edges[0].ToSlug != "concept/user" {
		t.Fatalf("rename must migrate the edge onto the live slug, got %+v", edges[0])
	}
}
