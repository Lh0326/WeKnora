package learning

import (
	"math"
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

	// First answer cites one chunk of concept/rag；该引用同时携带 doc d1，
	// 但精确归因（评审 P2-B）下 chunk 身份存在即不触发文档级回退——
	// doc-only 页 entity/weknora 不应被连带点亮。
	svc.RecordAnswerTouches(ctx, answerMessage(
		&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB},
	))
	events := repo.snapshotEvents()
	if len(events) != 1 || events[0].Slug != "concept/rag" {
		t.Fatalf("precise citation lights only its chunk's page, got %+v", events)
	}
	// 对照：引用没有任何 chunk 身份（纯 doc d1）时，doc-only 页经文档级
	// 回退正常点亮。
	svc.RecordAnswerTouches(collectorCtx(1, "carol"), answerMessage(
		&types.SearchResult{KnowledgeID: "d1", KnowledgeBaseID: testKB},
	))
	if carol, _ := repo.GetMastery(ctx, learningScopeFor(1, "web_user:carol"), "entity/weknora"); carol == nil || carol.EvidenceCount != 1 {
		t.Fatalf("doc-grained citation must still reach the doc-only page: %+v", carol)
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

	// A different answer minutes later is a re-ask; retrying the first callback is not.
	second := answerMessage(&types.SearchResult{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: testKB})
	second.ID = "msg-2"
	svc.RecordAnswerTouches(ctx, second)
	events = repo.snapshotEvents()
	aliceEvents := 0
	reasks := 0
	for _, e := range events {
		if e.SubjectID != "web_user:alice" {
			continue
		}
		aliceEvents++
		if e.Type == types.LearningEventReAsk {
			reasks++
		}
	}
	if aliceEvents != 2 || reasks != 1 {
		t.Fatalf("alice's in-window repeat must classify as one re_ask: events=%d reasks=%d", aliceEvents, reasks)
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

func TestRecordAnswerTouchesRechecksDurableConsent(t *testing.T) {
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

	if repo.prefsHits.Load() != 3 {
		t.Fatalf("each answer must recheck durable consent, got %d reads, want 3", repo.prefsHits.Load())
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

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", ""); err != nil {
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
		if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", ""); err != nil {
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

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", ""); err != nil {
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
// private — no event, and an explicit disabled outcome for UI feedback.
func TestRecordWikiReadSkipOptedOut(t *testing.T) {
	svc, repo, _ := wikiReadFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{
		TenantID: 1, SubjectID: "web_user:alice", CollectDisabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", ""); err != interfaces.ErrLearningCollectionDisabled {
		t.Fatalf("want disabled collection outcome, got %v", err)
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

	if err := svc.RecordWikiRead(ctx, testKB, "concept/ghost", ""); err != ErrWikiReadTarget {
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

	if err := svc.RecordWikiRead(collectorCtx(1, "alice"), testKB, "concept/rag", ""); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("events = %d, want 0 with gate closed", n)
	}
}

// TestRecordWikiReadDeepTier: the "deep" tier lands its own event type with
// the tier weight on top of a normal read, and repeats inside the re-ask
// window are silent no-ops — one deep credit per node per window.
func TestRecordWikiReadDeepTier(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Doc"}))
	svc := testService(repo, wiki)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	// Normal glance first (the delayed 5s fire), then the deep leave signal.
	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "deep"); err != nil {
		t.Fatal(err)
	}
	events := repo.snapshotEvents()
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (glance + deep)", len(events))
	}
	if events[0].Type != types.LearningEventWikiToolRead || events[0].Weight != WeightTopicSignal {
		t.Fatalf("glance event = %+v", events[0])
	}
	if events[1].Type != types.LearningEventWikiDeepRead || events[1].Weight != WeightWikiDeepRead {
		t.Fatalf("deep event = %+v, want wiki_deep_read w/ %.1f", events[1], WeightWikiDeepRead)
	}
	row, err := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), "concept/rag")
	if err != nil || row == nil {
		t.Fatalf("mastery row missing: %v %v", row, err)
	}
	wantLogit := WeightTopicSignal + WeightWikiDeepRead
	if math.Abs(row.Logit-wantLogit) > 1e-9 {
		t.Fatalf("folded logit = %v, want %v (glance + deep)", row.Logit, wantLogit)
	}

	// Second deep inside the window: no-op.
	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "deep"); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 2 {
		t.Fatalf("repeat deep appended %d events, want 0 (window cap)", n-2)
	}
	// A later difficulty correction permits one observed revisit. It may
	// release the pending review, but cannot farm additional reading credit.
	time.Sleep(time.Millisecond) // separate user actions on Windows' coarse clock
	if err := svc.SetNodeState(ctx, testKB, "concept/rag", "review"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "deep"); err != nil {
		t.Fatal(err)
	}
	view, err := svc.ObjectiveView(ctx, testKB)
	if err != nil || len(view.Nodes) != 1 || view.Nodes[0].Estimate.Level != "developing" || view.Nodes[0].Estimate.Opportunities != 1 || view.Nodes[0].Estimate.Corrections != 1 {
		t.Fatalf("revisit did not close difficulty without another declaration: %+v %v", view, err)
	}
	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "deep"); err != nil {
		t.Fatal(err)
	}
	events = repo.snapshotEvents()
	if len(events) != 4 || events[len(events)-1].Weight != 0 {
		t.Fatal("revisit refresh bypassed event/weight caps", events)
	}
}

// TestRecordWikiReadDeepTierRespectsOptOut: opted-out subjects get nothing
// stored, deep or not.
func TestRecordWikiReadDeepTierRespectsOptOut(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Doc"}))
	svc := testService(repo, wiki)
	repo.prefs["web_user:alice"] = &types.LearningSubjectPrefs{CollectDisabled: true}
	ctx := collectorCtx(1, "alice")

	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "deep"); err != interfaces.ErrLearningCollectionDisabled {
		t.Fatalf("want disabled collection outcome, got %v", err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("opted-out deep read stored %d events, want 0", n)
	}
}
