package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const testKB = "kb-test"

func testService(repo *stubLearningRepo, wiki *stubWikiRepo) *Service {
	if wiki == nil {
		wiki = &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	}
	return NewService(repo, wiki, nil, nil, nil, nil)
}

func answerMessage(refs ...*types.SearchResult) *types.Message {
	return &types.Message{
		ID: "msg-1", SessionID: "sess-1",
		KnowledgeReferences: types.References(refs),
	}
}

// TestRecordAnswerTouchesNoOpWhenGateClosed is the no-op proof the design
// contract demands: with the kill switch unset, nothing is read, nothing is
// written — the feature is indistinguishable from absent.
func TestRecordAnswerTouchesNoOpWhenGateClosed(t *testing.T) {
	repo := newStubRepo()
	svc := testService(repo, nil)
	msg := answerMessage(&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB})

	t.Setenv("LEARNING_ENABLE", "")
	svc.RecordAnswerTouches(collectorCtx(1, "alice"), msg)

	if repo.appends.Load() != 0 || repo.prefsHits.Load() != 0 {
		t.Fatalf("gate closed must touch nothing: appends=%d prefsReads=%d",
			repo.appends.Load(), repo.prefsHits.Load())
	}
	if len(repo.snapshotEvents()) != 0 {
		t.Fatal("gate closed must not append events")
	}
}

// TestRecordAnswerTouchesNoOpWithoutEvidence covers the empty-reference
// short-circuit even when the gate is open.
func TestRecordAnswerTouchesNoOpWithoutEvidence(t *testing.T) {
	repo := newStubRepo()
	svc := testService(repo, nil)
	t.Setenv("LEARNING_ENABLE", "true")

	svc.RecordAnswerTouches(collectorCtx(1, "alice"), answerMessage())
	svc.RecordAnswerTouches(collectorCtx(1, "alice"), nil)

	if repo.appends.Load() != 0 || repo.prefsHits.Load() != 0 {
		t.Fatalf("no evidence must touch nothing: appends=%d prefsReads=%d",
			repo.appends.Load(), repo.prefsHits.Load())
	}
}

func TestRecordAnswerTouchesCollectsAndFolds(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d9|Draft"}))
	wiki.addPage(testKB, testWikiPage("entity/weknora", nil, []string{"d1|Weknora Doc"}))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	// First answer: cites one chunk of concept/rag and doc d1 (entity/weknora).
	svc.RecordAnswerTouches(ctx, answerMessage(
		&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB},
	))
	events := repo.snapshotEvents()
	if len(events) != 2 {
		t.Fatalf("first answer events = %d, want 2: %+v", len(events), events)
	}
	for _, e := range events {
		if e.Type != types.LearningEventAnswerCite {
			t.Errorf("first touch type = %q, want answer_cite", e.Type)
		}
		if e.Weight != WeightAnswerCite {
			t.Errorf("weight = %v, want %v", e.Weight, WeightAnswerCite)
		}
		if e.SessionID != "sess-1" || e.MessageID != "msg-1" {
			t.Errorf("traceability lost: %+v", e)
		}
		if e.SubjectID != "web_user:alice" || e.TenantID != 1 || e.KnowledgeBaseID != testKB {
			t.Errorf("scope wrong: %+v", e)
		}
	}
	if row, _ := repo.GetMastery(ctx, learningScopeFor(1, "web_user:alice"), "concept/rag"); row == nil || row.EvidenceCount != 1 {
		t.Fatalf("mastery not folded once: %+v", row)
	}

	// Second answer minutes later (inside the re-ask window): same touch is negative.
	svc.RecordAnswerTouches(ctx, answerMessage(&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB}))
	events = repo.snapshotEvents()
	if len(events) != 4 {
		t.Fatalf("second answer events total = %d, want 4", len(events))
	}
	reasks := 0
	for _, e := range events[2:] {
		if e.Type == types.LearningEventReAsk {
			reasks++
		}
	}
	if reasks != 2 {
		t.Fatalf("in-window repeat classified as re_ask %d times, want 2", reasks)
	}
	if row, _ := repo.GetMastery(ctx, learningScopeFor(1, "web_user:alice"), "concept/rag"); row == nil || row.NegativeCount != 1 {
		t.Fatalf("re_ask not folded as negative: %+v", row)
	}
}

func TestRecordAnswerTouchesRespectsOptOut(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, nil))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")

	repo.prefs["web_user:bob"] = &types.LearningSubjectPrefs{CollectDisabled: true}
	svc.RecordAnswerTouches(collectorCtx(1, "bob"), answerMessage(
		&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB},
	))

	if repo.appends.Load() != 0 {
		t.Fatalf("opted-out subject must collect nothing, got %d events", repo.appends.Load())
	}
	if repo.prefsHits.Load() == 0 {
		t.Fatal("prefs must have been consulted")
	}
}

func TestRecordAnswerTouchesCachesPrefs(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, nil))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "carol")
	msg := answerMessage(&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB})

	svc.RecordAnswerTouches(ctx, msg)
	svc.RecordAnswerTouches(ctx, msg)
	svc.RecordAnswerTouches(ctx, msg)

	if repo.prefsHits.Load() != 1 {
		t.Fatalf("three answers in one cache window did %d prefs reads, want 1", repo.prefsHits.Load())
	}
}

func TestRecordAnswerTouchesOtherSubjectInvisible(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, nil))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")

	svc.RecordAnswerTouches(collectorCtx(1, "alice"), answerMessage(
		&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB},
	))
	if row, _ := repo.GetMastery(collectorCtx(1, "mallory"), learningScopeFor(1, "web_user:mallory"), "concept/rag"); row != nil {
		t.Fatal("another subject must not read alice's mastery")
	}
}

func learningScopeFor(tenant uint64, subject string) interfaces.LearningScope {
	return interfaces.LearningScope{TenantID: tenant, SubjectID: subject, KnowledgeBaseID: testKB}
}

func TestFoldOneIsIncremental(t *testing.T) {
	repo := newStubRepo()
	svc := testService(repo, nil)
	scope := learningScopeFor(1, "web_user:alice")
	ev := Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now()}

	svc.foldOne(collectorCtx(1, "alice"), scope, "concept/x", ev)
	svc.foldOne(collectorCtx(1, "alice"), scope, "concept/x", ev)

	row, _ := repo.GetMastery(collectorCtx(1, "alice"), scope, "concept/x")
	if row == nil || row.EvidenceCount != 2 || row.PositiveCount != 2 {
		t.Fatalf("incremental fold broken: %+v", row)
	}
	if row.Logit != WeightAnswerCite+WeightAnswerCite {
		t.Fatalf("logit = %v, want %v", row.Logit, 2*WeightAnswerCite)
	}
}

// readFixture wires the collector service with one concept page to read.
func wikiReadFixture(t *testing.T) (*Service, *stubLearningRepo, *stubWikiRepo) {
	t.Helper()
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, nil))
	return NewService(repo, wiki, nil, nil, nil, nil), repo, wiki
}

// TestRecordWikiReadHappyPath: opening a node page lands one low-trust
// touch at the topic-signal weight and folds it.
func TestRecordWikiReadHappyPath(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag"); err != nil {
		t.Fatal(err)
	}
	events := repo.snapshotEvents()
	if len(events) != 1 || events[0].Type != types.LearningEventWikiToolRead || events[0].Weight != WeightTopicSignal {
		t.Fatalf("events = %+v, want one wiki_tool_read at topic weight", events)
	}
	row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), "concept/rag")
	if row == nil || row.EvidenceCount != 1 || row.Logit != WeightTopicSignal {
		t.Fatalf("mastery = %+v, want folded read touch", row)
	}
}

// TestRecordWikiReadDedupWindow: the refresh shield swallows rapid
// re-opens, but a deliberate later re-open STILL lands in the timeline as a
// zero-weight trace (the learner's "我看了" must always be visible) without
// adding mastery — record the behaviour, pay the score once per 48h.
func TestRecordWikiReadDedupWindow(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	// Rapid re-opens inside the shield: one event total.
	for i := 0; i < 3; i++ {
		if err := svc.RecordWikiRead(ctx, testKB, "concept/rag"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(repo.snapshotEvents()); n != 1 {
		t.Fatalf("rapid re-opens: events = %d, want 1 (refresh shield)", n)
	}
}

// TestRecordWikiReadReopenTracesButNotScores: opening the same page again
// beyond the shield (a real re-visit, e.g. via a recommendation card)
// appends a zero-weight event so the timeline always shows the behaviour,
// and folds nothing so re-reading cannot farm mastery.
func TestRecordWikiReadReopenTracesButNotScores(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	// The earlier read: 10 minutes ago, beyond the shield, inside 48h.
	if err := repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventWikiToolRead,
		Weight: WeightTopicSignal, OccurredAt: time.Now().Add(-10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag"); err != nil {
		t.Fatal(err)
	}
	events := repo.snapshotEvents()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (trace kept)", len(events))
	}
	if events[1].Weight != 0 {
		t.Fatalf("re-open weight = %v, want 0 (score paid once per 48h)", events[1].Weight)
	}
	if row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), "concept/rag"); row != nil {
		t.Fatalf("mastery = %+v, want no fold from a zero-weight re-read", row)
	}
}

// TestRecordWikiReadSkipOptedOut: opted-out subjects keep their reads
// private — no event, no error.
func TestRecordWikiReadSkipOptedOut(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{
		TenantID: 1, SubjectID: "web_user:alice", CollectDisabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag"); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("events = %d, want 0 for opted-out subject", n)
	}
}

// TestRecordWikiReadRejectsNonNode: only entity/concept pages of this KB
// are knowledge nodes; anything else is not a touch.
func TestRecordWikiReadRejectsNonNode(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	if err := svc.RecordWikiRead(ctx, testKB, "concept/ghost"); err != ErrWikiReadTarget {
		t.Fatalf("err = %v, want ErrWikiReadTarget", err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("events = %d, want 0", n)
	}
}

// TestRecordWikiReadNoOpWhenGateClosed: the kill switch dominates.
func TestRecordWikiReadNoOpWhenGateClosed(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "")

	if err := svc.RecordWikiRead(collectorCtx(1, "alice"), testKB, "concept/rag"); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("events = %d, want 0 with gate closed", n)
	}
}
