package learning

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestPreciseCitationDoesNotSpreadToSiblingConcept(t *testing.T) {
	pages := []*types.WikiPage{
		{Slug: "concept/a", PageType: "concept", ChunkRefs: types.StringArray{"c1"}, SourceRefs: types.StringArray{"doc|Book"}},
		{Slug: "concept/b", PageType: "concept", ChunkRefs: types.StringArray{"c2"}, SourceRefs: types.StringArray{"doc|Book"}},
	}
	refs := types.References{{ID: "c1", KnowledgeID: "doc", KnowledgeBaseID: "kb"}}
	got := BenchTouchedSlugs(pages, refs, "kb")
	if len(got) != 1 || got[0] != "concept/a" {
		t.Fatalf("precise citation touches unrelated nodes: %v", got)
	}
	refs[0].ID = ""
	if got := BenchTouchedSlugs(pages, refs, "kb"); len(got) != 2 {
		t.Fatalf("document-only fallback lost: %v", got)
	}
}

func TestFeedbackRetriesCannotUnlockDirectEvidence(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	attempts := []types.LearningQuizAttempt{
		{Slug: "a", QuizItemID: "q", ChosenKey: "E", AnsweredAt: at},
		{Slug: "a", QuizItemID: "q", ChosenKey: "A", IsCorrect: true, AnsweredAt: at.Add(time.Minute)},
	}
	if len(CollectDirectFacts(attempts)["a"]) != 0 {
		t.Fatal("answer shown by unsure declaration became independent proof")
	}
	attempts = append(attempts, types.LearningQuizAttempt{Slug: "a", QuizItemID: "q", ChosenKey: "A", IsCorrect: true, AnsweredAt: at.Add(72 * time.Hour)})
	if len(CollectDirectFacts(attempts)["a"]) != 1 {
		t.Fatal("spaced practice failed to earn independent proof")
	}
}

func TestConcurrentReadsCreditOnlyOneTouch(t *testing.T) {
	svc, repo := readHookService(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "normal"); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := len(repo.snapshotEvents()); n != 1 {
		t.Fatalf("concurrent reads generated %d events", n)
	}
}

func TestZeroWeightEventCannotRenewMemory(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := FoldEvent(FoldState{}, quizCorrect(at))
	got := FoldEvent(state, Event{Type: types.LearningEventWikiToolRead, OccurredAt: at.Add(180 * 24 * time.Hour)})
	if got != state {
		t.Fatal("deduplicated read changed score, counters or decay clock")
	}
}

type failingKBLookup struct {
	interfaces.KnowledgeBaseRepository
}

func (failingKBLookup) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, errors.New("temporary database timeout")
}

func TestReconcileLookupFailureMustNotDeleteData(t *testing.T) {
	svc, repo, wiki, _, _ := maintenanceFixture(t)
	_ = svc
	runner := NewReconcileRunner(repo, wiki, failingKBLookup{})
	if err := runner.runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(repo.kbSweeps) != 0 {
		t.Fatalf("database failure triggered destructive sweep: %v", repo.kbSweeps)
	}
	rows, _ := repo.ListAllMastery(t.Context())
	if len(rows) == 0 {
		t.Fatal("learning history was deleted on a transient lookup failure")
	}
}

func TestQuizDraftRejectsDuplicateOptions(t *testing.T) {
	draft := validDraft()
	draft.Options["B"] = "  " + draft.Options["A"] + "  "
	if validateQuizDraft(draft, quizPage()) != nil {
		t.Fatal("same answer under two keys accepted")
	}
}

func TestQuizGenerationUsesOnlySuppliedEvidence(t *testing.T) {
	svc, repo, wiki, kbs, fake := maintenanceFixture(t)
	page := wiki.pages[testKB+"|concept"][0]
	page.ChunkRefs = append(page.ChunkRefs, "c-unavailable")
	fake.responses = []*types.ChatResponse{
		{Content: `{"questions":[{"question":"Unsupported?","options":{"A":"a","B":"b","C":"c","D":"d"},"correct_key":"A","explanation":"unseen chunk","chunk_refs":["c-unavailable"]}]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
	}
	if err := svc.runQuizPass(t.Context(), kbs.kbs[testKB], "m"); err != nil {
		t.Fatal(err)
	}
	items, _ := repo.ListQuizItems(t.Context(), 1, testKB, page.Slug)
	if len(items) != 0 {
		t.Fatal("question cited a page chunk the model was never given")
	}
}

func TestNegativeEvidenceCannotLiftDecayedFloor(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := FoldState{Logit: LogitFloor, EvidenceCount: 4, NegativeCount: 4, Stability: 1, LastEvidenceAt: at, FirstSeenAt: at}
	now := at.Add(180 * 24 * time.Hour)
	before := EffectiveP(state, now)
	got := EffectiveP(FoldEvent(state, Event{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now}), now)
	if got > before {
		t.Fatalf("wrong answer raised decayed floor: %.9f -> %.9f", before, got)
	}
}

func TestDelayedCorrectFeedbackUsesLatestGateFact(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")
	scope := newReadScope(1, "web_user:alice")
	old := time.Now().Add(-72 * time.Hour)
	item := &types.LearningQuizItem{ID: "q-delayed", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", CorrectKey: "A", Status: types.LearningQuizStatusActive}
	_ = storeGroundedQuiz(t, svc, repo, ctx, item)
	for _, id := range []string{item.ID, "q-other"} {
		_ = repo.InsertAttempt(ctx, &types.LearningQuizAttempt{TenantID: 1, KnowledgeBaseID: testKB, SubjectID: scope.SubjectID, Slug: item.Slug, QuizItemID: id, IsCorrect: true, ChosenKey: "A", AnsweredAt: old})
	}
	_ = repo.UpsertMastery(ctx, &types.MasteryState{TenantID: 1, KnowledgeBaseID: testKB, SubjectID: scope.SubjectID, Slug: item.Slug, Logit: 4, EvidenceCount: 2, PositiveCount: 2, Stability: 20, FirstSeenAt: old, LastEvidenceAt: old})
	result, err := svc.SubmitAnswer(ctx, testKB, item.ID, "A")
	if err != nil {
		t.Fatal(err)
	}
	row, _ := repo.GetMastery(ctx, scope, item.Slug)
	attempts, _ := repo.ListAttempts(ctx, scope, item.Slug)
	facts := CollectDirectFacts(attempts)[item.Slug]
	now := time.Now()
	want := NextReviewDays(StateFromModel(row), now, tierDownThreshold(gatedAnchoredLevel(StateFromModel(row), facts, now).Level))
	if want == nil || result.NextReviewDays == nil || math.Abs(*want-*result.NextReviewDays) > 0.001 {
		t.Fatalf("answer response schedule %v differs from refreshed profile schedule %v", result.NextReviewDays, want)
	}
}

func TestDisabledQuizCannotBeSubmitted(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")
	_ = repo.UpsertQuizItem(ctx, &types.LearningQuizItem{ID: "q-disabled", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", CorrectKey: "A", Status: types.LearningQuizStatusDisabled})
	if _, err := svc.SubmitAnswer(ctx, testKB, "q-disabled", "A"); err != ErrQuizNotFound {
		t.Fatalf("disabled question accepted: %v", err)
	}
}

func TestTopicMappingHonorsOptOut(t *testing.T) {
	svc, repo, _, kbs, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	kbs.kbs[testKB].WikiConfig = &types.WikiConfig{SynthesisModelID: "m"}
	repo.topicStats = []types.MemoryTopicStat{{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "rag", Topic: "RAG"}}
	repo.affinity = []types.MemoryDocAffinity{{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1"}}
	if err := repo.UpsertSubjectPrefs(t.Context(), &types.LearningSubjectPrefs{TenantID: 1, SubjectID: "web_user:alice", CollectDisabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(repo.snapshotEvents()) != 0 || len(repo.maps) != 0 || fake.calls.Load() != 0 {
		t.Fatal("opted-out subject received background mapping work")
	}
}

func TestEdgeBatchRejectsNewCycle(t *testing.T) {
	svc, repo, wiki, kbs, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	wiki.pages[testKB+"|concept"][1].OutLinks = types.StringArray{"concept/rag"}
	fake.responses = []*types.ChatResponse{{Content: `{"pairs":[{"from":"concept/rag","to":"concept/decay","relation":"prerequisite","confidence":0.9},{"from":"concept/decay","to":"concept/rag","relation":"prerequisite","confidence":0.95}]}`, FinishReason: "stop"}}
	if err := svc.runEdgePass(t.Context(), kbs.kbs[testKB], "m"); err != nil {
		t.Fatal(err)
	}
	edges, err := repo.ListEdges(t.Context(), 1, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].FromSlug != "concept/decay" {
		t.Fatalf("expected the strongest acyclic edge, got %+v", edges)
	}
}

func TestEdgePassSplitsModelBatches(t *testing.T) {
	svc, _, wiki, kbs, fake := maintenanceFixture(t)
	for i := 0; i < 85; i++ {
		wiki.addPage(testKB, &types.WikiPage{Slug: fmt.Sprintf("concept/n%03d", i), PageType: "concept", Title: fmt.Sprintf("N%03d", i)})
	}
	pairs := edgeCandidatePairs(wiki.pages[testKB+"|concept"], nil)
	wantCalls := (len(pairs) + edgeBatchSize - 1) / edgeBatchSize
	if wantCalls < 2 {
		t.Fatalf("fixture only generated %d pairs", len(pairs))
	}
	for i := 0; i < wantCalls; i++ {
		fake.responses = append(fake.responses, &types.ChatResponse{Content: `{"pairs":[]}`, FinishReason: "stop"})
	}
	if err := svc.runEdgePass(t.Context(), kbs.kbs[testKB], "m"); err != nil {
		t.Fatal(err)
	}
	if got := int(fake.calls.Load()); got != wantCalls {
		t.Fatalf("calls = %d, want %d", got, wantCalls)
	}
}

func TestSummarySentenceLimit(t *testing.T) {
	for _, tc := range []struct {
		input string
		n     int
		want  string
	}{
		{"One. Two. Three.", 2, "One. Two."},
		{"第一句。第二句！第三句？", 2, "第一句。第二句！"},
		{"No terminator", 2, "No terminator"},
		{"Text.", 0, ""},
	} {
		if got := firstSentences(tc.input, tc.n); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
