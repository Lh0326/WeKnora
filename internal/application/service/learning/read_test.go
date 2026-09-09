package learning

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// readFixture wires the real Service with the full stub set.
func readFixture(t *testing.T) (*Service, *stubLearningRepo, *stubWikiRepo) {
	t.Helper()
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Doc"}))
	wiki.addPage(testKB, testWikiPage("concept/decay", []string{"c2"}, nil))
	wiki.addPage(testKB, testWikiPage("entity/weknora", nil, nil))
	svc := NewService(repo, wiki, &stubChunkRepo{chunks: map[string]*types.Chunk{"c1": {ID: "c1", Content: "RAG evidence"}, "c2": {ID: "c2", Content: "Decay evidence"}}}, nil, nil, nil)
	return svc, repo, wiki
}

func TestGetProgressCountsTiersAndUnits(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	ctx := collectorCtx(1, "alice")

	// rag lives in a named folder; the other two stay in the wiki root.
	wiki.folders = map[string][]*types.WikiFolder{
		testKB: {{ID: "f1", Name: "RAG 基础"}},
	}
	for _, pages := range wiki.pages {
		for _, p := range pages {
			if p.Slug == "concept/rag" {
				p.FolderID = "f1"
			}
		}
	}

	// Touch rag twice (logit 2 → p≈0.88), leave the rest unseen. The two
	// citations are indirect evidence, so without direct quiz facts the
	// gate caps rag at touched; the distinct-item corrects below unlock
	// mastered (two items straddling the session gap).
	now := time.Now()
	for i := 0; i < 2; i++ {
		_ = repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now,
		})
		svc.foldOne(ctx, newReadScope(1, "web_user:alice"), "concept/rag",
			Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now})
	}
	for i, item := range []string{"q1", "q2"} {
		_ = repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			QuizItemID: item, Slug: "concept/rag", ChosenKey: "A", IsCorrect: true,
			AnsweredAt: now.Add(-time.Duration(i) * (MasteredSessionGap + time.Hour)),
		})
	}

	progress, err := svc.GetProgress(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if progress.TotalNodes != 3 {
		t.Fatalf("total = %d, want 3", progress.TotalNodes)
	}
	if progress.LitNodes != 1 {
		t.Fatalf("lit = %d, want 1 (rag mastered)", progress.LitNodes)
	}
	if progress.Levels["unseen"] != 2 || progress.Levels["mastered"] != 1 {
		t.Fatalf("levels = %v", progress.Levels)
	}
	if len(progress.Units) != 2 {
		t.Fatalf("units = %+v, want root + f1", progress.Units)
	}
	byID := map[string]LearningUnitProgress{}
	for _, u := range progress.Units {
		byID[u.FolderID] = u
	}
	if u := byID["f1"]; u.FolderName != "RAG 基础" || u.Total != 1 || u.Lit != 1 {
		t.Fatalf("named folder unit = %+v, want name RAG 基础 with 1/1", u)
	}
	if u := byID[""]; u.FolderName != "" || u.Total != 2 {
		t.Fatalf("root unit = %+v, want unnamed root with 2 pages", u)
	}
}

func TestRecommendFrontierAndQuizFlag(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	ctx := collectorCtx(1, "alice")

	// rag has one active quiz item.
	_ = repo.UpsertQuizItem(ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "Q?", Status: types.LearningQuizStatusActive,
	})
	// A prerequisite edge rag -> decay: rag unseen means decay is gated.
	_ = repo.UpsertEdge(ctx, &types.LearningEdge{
		TenantID: 1, KnowledgeBaseID: testKB, FromSlug: "concept/rag", ToSlug: "concept/decay",
		Relation: types.LearningEdgePrerequisite,
	})
	_ = wiki

	recs, err := svc.Recommend(ctx, testKB, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("frontier must be non-empty")
	}
	found := map[string]interfaces_Recommendation{}
	for _, r := range recs {
		found[r.Slug] = r
	}
	// rag is on the frontier with quiz material flagged.
	if r, ok := found["concept/rag"]; !ok || !r.HasQuiz {
		t.Fatalf("rag missing or quiz flag lost: %+v", found["concept/rag"])
	}
	// decay sits behind an unseen prerequisite: not on the frontier.
	if _, ok := found["concept/decay"]; ok {
		t.Fatal("gated node must not be recommended")
	}
}

type interfaces_Recommendation = Recommendation

func newReadScope(tenant uint64, subject string) scopeType {
	return scopeType{TenantID: tenant, SubjectID: subject, KnowledgeBaseID: testKB}
}

func TestTakeQuizStripsAnswerAndPrefersFresh(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")

	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-fresh",
		Question: "Fresh?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "A", Explanation: "secret", Status: types.LearningQuizStatusActive,
	})
	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-used",
		Question: "Used?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "B", Explanation: "secret2", Status: types.LearningQuizStatusActive,
	})
	_ = repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		QuizItemID: "q-used", Slug: "concept/rag", ChosenKey: "B", IsCorrect: true, AnsweredAt: time.Now(),
	})

	questions, err := svc.TakeQuiz(ctx, testKB, "concept/rag")
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 2 {
		t.Fatalf("questions = %d, want 2", len(questions))
	}
	if questions[0].ID != "q-fresh" {
		t.Fatalf("fresh question must come first, got %s", questions[0].ID)
	}
	for _, q := range questions {
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "secret") || strings.Contains(string(b), "correct_key") {
			t.Fatalf("answer material leaked into served question: %s", b)
		}
	}
}

func TestSubmitAnswerGradesAndFolds(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")

	item := &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-1",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "B", Explanation: "because", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusActive,
	}
	_ = storeGroundedQuiz(t, svc, repo, ctx, item)

	// Wrong answer.
	res, err := svc.SubmitAnswer(ctx, testKB, "q-1", "A")
	if err != nil {
		t.Fatal(err)
	}
	if res.Correct || res.CorrectKey != "B" {
		t.Fatalf("wrong answer graded %+v", res)
	}
	events := repo.snapshotEvents()
	if len(events) != 1 || events[0].Type != types.LearningEventQuizWrong || events[0].Weight != WeightQuizWrong {
		t.Fatalf("quiz_wrong event missing: %+v", events)
	}

	// Correct answer after feedback: verdict is correct, evidence weight zero.
	res, err = svc.SubmitAnswer(ctx, testKB, "q-1", "B")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Correct {
		t.Fatal("correct answer graded wrong")
	}
	events = repo.snapshotEvents()
	if len(events) != 2 || events[1].Type != types.LearningEventQuizCorrect {
		t.Fatalf("quiz_correct event missing: %+v", events)
	}
	if events[1].Weight != 0 {
		t.Fatalf("feedback-exposed retry weight = %v, want 0", events[1].Weight)
	}
	// Fold landed on the mastery row.
	row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), "concept/rag")
	if row == nil || row.EvidenceCount != 1 {
		t.Fatalf("quiz events not folded: %+v", row)
	}
}

func TestSubmitAnswerOptedOutServesVerdictButStoresNothing(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "bob")
	repo.prefs["web_user:bob"] = &types.LearningSubjectPrefs{CollectDisabled: true}

	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-9",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "A", Explanation: "why", Status: types.LearningQuizStatusActive,
	})

	res, err := svc.SubmitAnswer(ctx, testKB, "q-9", "A")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Correct || res.Explanation != "why" {
		t.Fatalf("opted-out subject must still get the verdict: %+v", res)
	}
	if repo.appends.Load() != 0 {
		t.Fatalf("opted-out subject must store nothing, events = %d", repo.appends.Load())
	}
	if row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:bob"), "concept/rag"); row != nil {
		t.Fatal("opted-out subject must not gain mastery rows")
	}
}

func TestTimelinePagesNewestFirst(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	ctx := collectorCtx(1, "alice")
	// Human titles ride along so the client never has to show a slug.
	for _, pages := range wiki.pages {
		for _, p := range pages {
			switch p.Slug {
			case "entity/weknora":
				p.Title = "WeKnora 知识库"
			case "concept/rag":
				p.Title = "检索增强生成"
			case "concept/decay":
				p.Title = "惰性衰减"
			}
		}
	}
	base := time.Now().Add(-time.Hour)
	for i, slug := range []string{"concept/rag", "concept/decay", "entity/weknora"} {
		_ = repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: slug, Type: types.LearningEventAnswerCite, OccurredAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	items, total, err := svc.Timeline(ctx, testKB, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 2 {
		t.Fatalf("timeline page = %d/%d, want 2 of 3", len(items), total)
	}
	if items[0].Slug != "entity/weknora" {
		t.Fatalf("newest first broken: %+v", items[0])
	}
	if items[0].Title != "WeKnora 知识库" || items[0].PageType != "entity" {
		t.Fatalf("timeline title missing: %+v", items[0])
	}
	if items[1].Title != "惰性衰减" || items[1].PageType != "concept" {
		t.Fatalf("timeline title missing: %+v", items[1])
	}
}

func TestExportAndDeleteProfile(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")

	_ = repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: time.Now(),
	})
	_ = repo.UpsertTopicMap(ctx, &types.MemoryWikiMap{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		NormalizedTopicKey: "rag", Slug: "concept/rag",
	})
	// Another subject's data must never appear.
	_ = repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:mallory", KnowledgeBaseID: testKB,
		Slug: "concept/decay", Type: types.LearningEventAnswerCite, OccurredAt: time.Now(),
	})

	payload, err := svc.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 || payload.Events[0].SubjectID != "web_user:alice" {
		t.Fatalf("export leaked or lost rows: %+v", payload.Events)
	}
	if len(payload.TopicMaps) != 1 {
		t.Fatalf("topic maps = %d, want 1", len(payload.TopicMaps))
	}

	// Delete with opt-out.
	if err := svc.DeleteProfile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if again, _ := svc.ExportProfile(ctx); len(again.Events) != 0 {
		t.Fatalf("events survived delete: %d", len(again.Events))
	}
	prefs, _ := repo.GetSubjectPrefs(ctx, "web_user:alice")
	if prefs == nil || !prefs.CollectDisabled {
		t.Fatal("opt_out must persist the collection opt-out")
	}
	// Mallory untouched.
	if leaked, _ := repo.ListEventsBySubject(ctx, "web_user:mallory"); len(leaked) != 1 {
		t.Fatal("delete crossed subject boundaries")
	}
}

// TestExportKBSummaryGroupsAndMarksOrphans: the export's top-level
// kb_summary rolls up per-KB counts and marks deleted KBs Exists=false so
// their group labels itself in the downloaded JSON.
func TestExportKBSummaryGroupsAndMarksOrphans(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	kbs := &stubKBRepo{kbs: map[string]*types.KnowledgeBase{
		testKB: {ID: testKB, TenantID: 1, Name: "课题四"},
		// kb-gone is absent: the deleted-KB case.
	}}
	svc := NewService(repo, wiki, nil, nil, kbs, nil)
	ctx := collectorCtx(1, "alice")
	now := time.Now()

	for _, kb := range []string{testKB, "kb-gone"} {
		_ = repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: kb,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: now,
		})
		_ = repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: kb,
			Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: now,
		})
	}
	_ = repo.UpsertMastery(ctx, &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
	})

	payload, err := svc.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.KBSummary) != 2 {
		t.Fatalf("kb_summary = %+v, want 2 groups", payload.KBSummary)
	}
	byID := map[string]ExportKBSummary{}
	for _, s := range payload.KBSummary {
		byID[s.KbID] = s
	}
	if s := byID[testKB]; !s.Exists || s.KbName != "课题四" || s.Events != 2 || s.MasteryNodes != 1 {
		t.Fatalf("live KB summary = %+v", s)
	}
	if s := byID["kb-gone"]; s.Exists || s.Events != 2 {
		t.Fatalf("orphan KB summary = %+v, want Exists=false with 2 events", s)
	}
}

// TestListMasteryViewCoversUnseenNodes: the mastery map must describe the
// whole node universe, never-touched pages included — the tier cards and
// the graph hover card must never meet a node the map cannot place, and
// the unseen count can no longer disagree with the map rows.
func TestListMasteryViewCoversUnseenNodes(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	ctx := collectorCtx(1, "alice")
	for _, pages := range wiki.pages {
		for _, p := range pages {
			switch p.Slug {
			case "entity/weknora":
				p.Title = "WeKnora 知识库"
			case "concept/rag":
				p.Title = "检索增强生成"
			}
		}
	}
	now := time.Now()
	_ = repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now,
	})
	svc.foldOne(ctx, newReadScope(1, "web_user:alice"), "concept/rag",
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now})

	views, err := svc.ListMasteryView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 3 {
		t.Fatalf("views = %d, want all 3 fixture nodes (touched + unseen)", len(views))
	}
	bySlug := map[string]MasteryView{}
	for _, v := range views {
		bySlug[v.Slug] = v
	}
	if v := bySlug["concept/rag"]; v.Level != string(LevelTouched) || v.EvidenceCount != 1 {
		t.Fatalf("cited node = %+v, want touched (indirect evidence is gate-capped) with 1 evidence", v)
	}
	if v := bySlug["concept/rag"]; v.NextTierHint != HintQuizUnlockFamiliar {
		t.Fatalf("cited node hint = %q, want %q", v.NextTierHint, HintQuizUnlockFamiliar)
	}
	for _, slug := range []string{"concept/decay", "entity/weknora"} {
		v := bySlug[slug]
		if v.Level != string(LevelUnseen) || v.EvidenceCount != 0 || !v.LowConfidence {
			t.Fatalf("unseen entry %s = %+v, want unseen/0-evidence/low-confidence", slug, v)
		}
	}
	if bySlug["entity/weknora"].Title != "WeKnora 知识库" {
		t.Fatalf("unseen entry title = %q, want page title", bySlug["entity/weknora"].Title)
	}
}

// TestListMasteryViewLastActivitySeesZeroWeightTouches: the twinkle marker
// must reflect raw activity, not the fold. A deduped re-read or an unsure
// answer carries weight 0 by design and never folds — yet the learner just
// touched the node, so LastActivityAt (not LastEvidenceAt) must move.
func TestListMasteryViewLastActivitySeesZeroWeightTouches(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")
	now := time.Now()
	stale := now.Add(-72 * time.Hour)
	scope := newReadScope(1, "web_user:alice")
	// Old scored touch: folds, sets last_evidence_at three days back.
	_ = repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: stale,
	})
	svc.foldOne(ctx, scope, "concept/rag", Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: stale})
	// Fresh zero-weight touch (an unsure answer): recorded, never folded.
	_ = repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventQuizUnsure, Weight: 0, OccurredAt: now,
	})

	views, err := svc.ListMasteryView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	bySlug := map[string]MasteryView{}
	for _, v := range views {
		bySlug[v.Slug] = v
	}
	v := bySlug["concept/rag"]
	if v.LastActivityAt.Before(now.Add(-time.Minute)) {
		t.Fatalf("LastActivityAt = %v, want the fresh zero-weight touch (now-ish)", v.LastActivityAt)
	}
	if !v.LastEvidenceAt.Before(now.Add(-time.Hour)) {
		t.Fatalf("LastEvidenceAt = %v, want the stale folded timestamp (zero-weight must not fold)", v.LastEvidenceAt)
	}
	// Nodes with no events at all fall back to the zero time — the client
	// renders them non-recent either way.
	if bySlug["concept/decay"].LastActivityAt.IsZero() {
		// acceptable: never touched → zero activity
		_ = bySlug["concept/decay"]
	}
}

// TestTakeQuizResolvesSourceDocs: every served question carries the source
// documents its evidence chunks live in, titled so the client never shows a
// raw UUID — titled page refs resolve directly, bare-id refs (real wiki data
// carries them) fall back to the knowledge repository.
func TestTakeQuizResolvesSourceDocs(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	// d2 arrives as a bare id ref (no "|title"): the fallback path.
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1", "c2"}, []string{"d1|文档一", "d2"}))
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"c1": {ID: "c1", KnowledgeID: "d1", Content: "RAG evidence"},
		"c2": {ID: "c2", KnowledgeID: "d2", Content: "Decay evidence"},
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"d2": {ID: "d2", Title: "文档二（知识库回退）"},
	}}
	svc := NewService(repo, wiki, chunks, nil, nil, docs)
	ctx := collectorCtx(1, "alice")

	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-src",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "A", Explanation: "why", ChunkRefs: types.RefList{"c1", "c2"},
		Status: types.LearningQuizStatusActive,
	})

	questions, err := svc.TakeQuiz(ctx, testKB, "concept/rag")
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 1 || len(questions[0].SourceDocs) != 2 {
		t.Fatalf("source docs = %+v", questions)
	}
	byID := map[string]QuizSourceDoc{}
	for _, d := range questions[0].SourceDocs {
		byID[d.KnowledgeID] = d
	}
	if d := byID["d1"]; d.Title != "文档一" || d.ChunkCount != 1 {
		t.Fatalf("doc d1 = %+v, want titled 文档一 with 1 chunk", d)
	}
	if d := byID["d2"]; d.Title != "文档二（知识库回退）" || d.ChunkCount != 1 {
		t.Fatalf("doc d2 = %+v, want knowledge-repo fallback title with 1 chunk", d)
	}
}

// TestSubmitAnswerReturnsReviewSchedule: a correct answer on a fresh node
// must return a positive deterministic review horizon (the number of days
// the folded state holds its tier), and a node sunk below every gate by a
// wrong answer returns no horizon (review now).
func TestSubmitAnswerReturnsReviewSchedule(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")
	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-sched",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "B", Explanation: "why", Status: types.LearningQuizStatusActive,
	})

	res, err := svc.SubmitAnswer(ctx, testKB, "q-sched", "B")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Correct || res.NextReviewDays == nil || *res.NextReviewDays <= 0 {
		t.Fatalf("correct answer must schedule a future review, got %+v", res)
	}

	// A wrong answer on a fresh node sinks it to unseen: no tier to hold,
	// so no future horizon — the schedule says "review now" (nil).
	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/decay", ID: "q-sink",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "B", Explanation: "why", Status: types.LearningQuizStatusActive,
	})
	res, err = svc.SubmitAnswer(ctx, testKB, "q-sink", "A")
	if err != nil {
		t.Fatal(err)
	}
	if res.Correct {
		t.Fatal("wrong answer graded correct")
	}
	if res.NextReviewDays != nil {
		t.Fatalf("sunk node has no future horizon, got %v", *res.NextReviewDays)
	}
}

// TestSubmitAnswerRepeatDecayWindowed: the anti-farm decay applies to rapid
// re-answers on the same item, but attempts spaced beyond the re-ask window
// stop counting toward the decay — the spacing effect, so returning to
// practice on another day always moves the needle at full strength.
func TestSubmitAnswerRepeatDecayWindowed(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")
	_ = storeGroundedQuiz(t, svc, repo, ctx, &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-decay",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "B", Explanation: "why", Status: types.LearningQuizStatusActive,
	})

	// Part 1: an item answered many times DAYS ago — a fresh answer today
	// earns the FULL weight (old attempts must not starve new practice).
	for i := 0; i < 4; i++ {
		_ = repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			QuizItemID: "q-decay", Slug: "concept/rag", ChosenKey: "B", IsCorrect: true,
			AnsweredAt: time.Now().Add(-ReAskWindowHours*time.Hour - time.Duration(i+1)*time.Hour),
		})
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q-decay", "B"); err != nil {
		t.Fatal(err)
	}
	events := repo.snapshotEvents()
	if events[len(events)-1].Weight != WeightQuizCorrect {
		t.Fatalf("spaced attempt weight = %v, want full %v (old attempts must not starve new practice)", events[len(events)-1].Weight, WeightQuizCorrect)
	}

	// Part 2: same-session repeats still decay (the anti-farm guarantee).
	if _, err := svc.SubmitAnswer(ctx, testKB, "q-decay", "B"); err != nil {
		t.Fatal(err)
	}
	events = repo.snapshotEvents()
	if events[len(events)-1].Weight != 0 {
		t.Fatalf("same-window repeat weight = %v, want 0", events[len(events)-1].Weight)
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q-decay", "B"); err != nil {
		t.Fatal(err)
	}
	events = repo.snapshotEvents()
	// Further feedback-exposed retries remain zero evidence.
	if events[len(events)-1].Weight != 0 {
		t.Fatalf("third same-window repeat weight = %v, want 0", events[len(events)-1].Weight)
	}
}

// TestSubmitAnswerConcurrentDoubleSubmit is the anti-farm regression for the
// answer cycle's critical section: two simultaneous submissions of the same
// item must serialize, the second seeing the first's attempt and earning the
// decayed weight — not both reading prior=0 and both banking the full score.
func TestSubmitAnswerConcurrentDoubleSubmit(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")

	item := &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", ID: "q-race",
		Question: "Q?", Options: types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		CorrectKey: "B", Explanation: "because", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusActive,
	}
	if err := storeGroundedQuiz(t, svc, repo, ctx, item); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.SubmitAnswer(ctx, testKB, "q-race", "B"); err != nil {
				t.Errorf("concurrent submit failed: %v", err)
			}
		}()
	}
	wg.Wait()

	row, err := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), "concept/rag")
	if err != nil || row == nil {
		t.Fatalf("mastery row missing after concurrent submits: %v, %v", row, err)
	}
	want := WeightQuizCorrect
	if math.Abs(row.Logit-want) > 1e-9 {
		t.Fatalf("concurrent double-submit logit = %v, want %v (second submission must be decayed)", row.Logit, want)
	}
}

// TestExportDeleteCoverSharedTenantRows: learning rows collected through a
// shared KB are filed under the KB owner's effective tenant — the export
// must surface them and the profile delete must remove them, or "delete my
// profile" silently leaves data in every workspace a shared KB belongs to.
func TestExportDeleteCoverSharedTenantRows(t *testing.T) {
	svc, repo, _ := readFixture(t)
	// The caller lives in tenant 1; the shared KB's owner is tenant 7.
	ctx := collectorCtx(1, "alice")

	// Home-tenant row…
	_ = repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventAnswerCite, OccurredAt: time.Now(),
	})
	// …and the same person's rows filed under the shared KB's effective
	// tenant (what the KB access guard's tenant rewrite produces).
	shared := &types.LearningEvent{
		TenantID: 7, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/decay", Type: types.LearningEventAnswerCite, OccurredAt: time.Now(),
	}
	_ = repo.AppendEvent(ctx, shared)
	_ = repo.UpsertMastery(ctx, &types.MasteryState{
		TenantID: 7, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/decay", Logit: 1.5, EvidenceCount: 1, PositiveCount: 1,
	})

	payload, err := svc.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 {
		t.Fatalf("export events = %d, want 2 (home + shared-tenant rows): %+v", len(payload.Events), payload.Events)
	}
	if len(payload.Mastery) != 1 || payload.Mastery[0].TenantID != 7 {
		t.Fatalf("export mastery = %+v, want the shared-tenant row", payload.Mastery)
	}
	if len(payload.KBSummary) != 1 || payload.KBSummary[0].Events != 2 {
		t.Fatalf("kb summary = %+v, want one KB rolling up both rows", payload.KBSummary)
	}

	// Delete (no opt-out needed here) removes BOTH tenants' rows.
	if err := svc.DeleteProfile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if left, _ := repo.ListEventsBySubject(ctx, "web_user:alice"); len(left) != 0 {
		t.Fatalf("shared-tenant rows survived delete: %+v", left)
	}
	if m, _ := repo.ListMasteryBySubject(ctx, "web_user:alice"); len(m) != 0 {
		t.Fatalf("shared-tenant mastery survived delete: %+v", m)
	}
}

// TestOptOutIsSubjectGlobalForSharedKBs: the collection opt-out is a
// property of the person — a home-tenant opt-out row must stop collection
// even when the request runs under a shared KB's effective tenant.
func TestOptOutIsSubjectGlobalForSharedKBs(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")

	// Opted out at home (tenant 1)…
	repo.prefs["web_user:alice"] = &types.LearningSubjectPrefs{CollectDisabled: true}

	// …but the read arrives under the shared KB's effective tenant 7.
	sharedCtx := collectorCtx(7, "alice")
	_ = wiki
	if err := svc.RecordWikiRead(sharedCtx, testKB, "concept/rag", ""); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("collection continued under effective tenant despite opt-out: %d events", n)
	}

	// And GetSettings (called from the home tenant) reports the opt-out.
	disabled, err := svc.GetSettings(collectorCtx(1, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	if !disabled.CollectDisabled {
		t.Fatal("GetSettings must reflect the subject-scoped opt-out row")
	}
}

// Existing endpoint tests seed actual versioned evidence instead of relying on
// legacy empty hashes, which the production endpoint must reject.
func storeGroundedQuiz(t *testing.T, svc *Service, repo *stubLearningRepo, ctx context.Context, item *types.LearningQuizItem) error {
	t.Helper()
	_, _, hash, err := svc.currentQuizEvidence(ctx, item.TenantID, item.KnowledgeBaseID, item.Slug)
	if err != nil || hash == "" {
		t.Fatalf("invalid quiz fixture evidence: hash=%q err=%v", hash, err)
	}
	item.EvidenceHash = hash
	return repo.UpsertQuizItem(ctx, item)
}
