package learning

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/types"
)

// maintenanceFixture assembles the five-dependency service with scripted
// model responses.
func maintenanceFixture(t *testing.T) (*Service, *stubLearningRepo, *stubWikiRepo, *stubKBRepo, *fakeChatModel) {
	t.Helper()
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/rag", PageType: "concept", Title: "RAG", KnowledgeBaseID: testKB,
		Content: "RAG combines retrieval and generation.", ChunkRefs: types.StringArray{"c1"},
		OutLinks: types.StringArray{"concept/decay"},
	})
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/decay", PageType: "concept", Title: "Decay", KnowledgeBaseID: testKB,
		ChunkRefs: types.StringArray{"c2"},
	})
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"c1": {ID: "c1", Content: "RAG evidence one"},
		"c2": {ID: "c2", Content: "Decay evidence"},
	}}
	fake := &fakeChatModel{}
	kbs := &stubKBRepo{kbs: map[string]*types.KnowledgeBase{
		testKB: {ID: testKB, TenantID: 1, WikiConfig: &types.WikiConfig{LearningFeatures: true, SynthesisModelID: "m"}},
	}}
	// The KB must appear in the maintenance universe: seed one mastery row.
	if err := repo.UpsertMastery(t.Context(), &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
	}); err != nil {
		t.Fatal(err)
	}
	svc, _ := maintenanceService(repo, wiki, chunks, fake, kbs)
	return svc, repo, wiki, kbs, fake
}

// TestMaintenanceKBGateClosedMeansZeroLLMCalls is the proof the design
// contract demands: with the per-KB switch off, the edge and quiz paths —
// the feature's only sustained model cost — never call the model at all.
func TestMaintenanceKBGateClosedMeansZeroLLMCalls(t *testing.T) {
	svc, _, wiki, kbs, fake := maintenanceFixture(t)
	kbs.kbs[testKB].WikiConfig.LearningFeatures = false
	// Wiki pages still exist and mastery still names the KB; only the gate differs.
	_ = wiki
	t.Setenv("LEARNING_ENABLE", "true")

	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() != 0 {
		t.Fatalf("KB gate closed must mean zero LLM calls, got %d", fake.calls.Load())
	}
}

// TestMaintenanceGlobalGateClosedMeansZeroLLMCalls: the kill switch
// dominates everything.
func TestMaintenanceGlobalGateClosedMeansZeroLLMCalls(t *testing.T) {
	svc, _, _, _, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "")
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() != 0 {
		t.Fatalf("global gate closed must mean zero LLM calls, got %d", fake.calls.Load())
	}
}

// TestMaintenanceEdgesAndQuizzesHappyPath drives both gated paths with a
// scripted model: one prerequisite edge is stored, one grounded question is
// stored, and one ungrounded question (out-of-page chunk ref) is dropped.
func TestMaintenanceEdgesAndQuizzesHappyPath(t *testing.T) {
	svc, repo, _, _, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")

	fake.responses = []*types.ChatResponse{
		// Edge adjudication for the wiki-link pair.
		{Content: `{"pairs":[{"from":"concept/rag","to":"concept/decay","relation":"prerequisite","confidence":0.9},{"from":"concept/rag","to":"concept/decay","relation":"related","confidence":0.99}]}`, FinishReason: "stop"},
		// Quiz for concept/rag: one grounded, one with an invented chunk ref.
		{Content: `{"questions":[{"question":"Q1?","options":{"A":"a","B":"b","C":"c","D":"d"},"correct_key":"A","explanation":"because","chunk_refs":["c1"]},{"question":"Q2?","options":{"A":"a","B":"b","C":"c","D":"d"},"correct_key":"B","explanation":"nope","chunk_refs":["c1","c-ghost"]}]}`, FinishReason: "stop"},
		// Quiz for concept/decay: one grounded.
		{Content: `{"questions":[{"question":"Q3?","options":{"A":"a","B":"b","C":"c","D":"d"},"correct_key":"C","explanation":"ok","chunk_refs":["c2"]}]}`, FinishReason: "stop"},
	}

	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (edges + 2 quiz pages)", fake.calls.Load())
	}

	edges, _ := repo.ListEdges(t.Context(), 1, testKB)
	if len(edges) != 1 || edges[0].FromSlug != "concept/rag" || edges[0].ToSlug != "concept/decay" {
		t.Fatalf("edges = %+v, want exactly rag→decay", edges)
	}
	if edges[0].Relation != types.LearningEdgePrerequisite || edges[0].Source != types.LearningEdgeSourceHeuristicLLM {
		t.Fatalf("edge fields wrong: %+v", edges[0])
	}

	ragItems, _ := repo.ListQuizItems(t.Context(), 1, testKB, "concept/rag")
	if len(ragItems) != 1 {
		t.Fatalf("rag items = %d, want 1 (ghost-ref question dropped)", len(ragItems))
	}
	decayItems, _ := repo.ListQuizItems(t.Context(), 1, testKB, "concept/decay")
	if len(decayItems) != 1 {
		t.Fatalf("decay items = %d, want 1", len(decayItems))
	}
}

// TestMaintenanceQuizCapRespected: a page already at the cap generates
// nothing — no model call is even made for it.
func TestMaintenanceQuizCapRespected(t *testing.T) {
	svc, repo, _, _, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")

	// Fill concept/rag to the cap.
	for i := 0; i < QuizItemsPerSlug; i++ {
		if err := repo.UpsertQuizItem(t.Context(), &types.LearningQuizItem{
			TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
			Question: "filled", Status: types.LearningQuizStatusActive,
		}); err != nil {
			t.Fatal(err)
		}
	}

	fake.responses = []*types.ChatResponse{
		{Content: `{"pairs":[]}`, FinishReason: "stop"},     // edges: nothing
		{Content: `{"questions":[]}`, FinishReason: "stop"}, // decay quiz
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 (rag capped, not asked)", fake.calls.Load())
	}
}

// TestMaintenanceTopicMappingStoresAndSignals drives channel B end to end:
// a topic with a matching page is adjudicated, mapped, and projected as a
// topic_signal event; the same mapping run twice does not farm events.
func TestMaintenanceTopicMappingStoresAndSignals(t *testing.T) {
	svc, repo, _, kbs, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	kbs.kbs[testKB].WikiConfig = &types.WikiConfig{SynthesisModelID: "m"} // topic path needs a model

	repo.topicStats = []types.MemoryTopicStat{
		{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "rag", Topic: "RAG"},
		{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "lunch", Topic: "午饭吃什么"},
	}
	repo.affinity = []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1"},
	}

	fake.responses = []*types.ChatResponse{
		{Content: `{"maps":{"rag":{"slug":"concept/rag","confidence":0.9},"lunch":"reject"}}`, FinishReason: "stop"},
		// Subsequent passes: edges + quizzes get their scripted turns.
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}

	if m := repo.maps["rag→concept/rag"]; m == nil || m.Confidence != 0.9 || m.DecidedBy != types.LearningMapDecidedByLLM {
		t.Fatalf("map = %+v, want stored llm verdict", repo.maps["rag→concept/rag"])
	}
	if repo.maps["lunch→"] != nil && len(repo.maps) != 1 {
		t.Fatalf("rejected topic stored: %+v", repo.maps)
	}
	signals := 0
	for _, e := range repo.snapshotEvents() {
		if e.Type == types.LearningEventTopicSignal {
			signals++
			if e.Slug != "concept/rag" || e.Weight != WeightTopicSignal {
				t.Fatalf("signal = %+v", e)
			}
		}
	}
	if signals != 1 {
		t.Fatalf("topic_signal count = %d, want 1", signals)
	}

	// Second run within the window: mapping upserted again is fine, but no
	// second signal event may be emitted.
	fake.responses = []*types.ChatResponse{
		{Content: `{"maps":{"rag":{"slug":"concept/rag","confidence":0.9}}}`, FinishReason: "stop"},
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	signals = 0
	for _, e := range repo.snapshotEvents() {
		if e.Type == types.LearningEventTopicSignal {
			signals++
		}
	}
	if signals != 1 {
		t.Fatalf("signal count after re-run = %d, want still 1 (window dedup)", signals)
	}
}

// TestMaintenanceTopicMappingIgnoresNonNodePages: channel B projects onto
// entity/concept nodes only. A topic whose only name match is a summary page
// must never enter adjudication — the node layer excludes document
// projections — while a genuinely nodal match still maps normally.
func TestMaintenanceTopicMappingIgnoresNonNodePages(t *testing.T) {
	svc, repo, wiki, kbs, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	kbs.kbs[testKB].WikiConfig = &types.WikiConfig{SynthesisModelID: "m"} // topic path needs a model

	// A summary page whose title collides with the "deploy" topic.
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "summary/deploy", PageType: "summary", Title: "Deploy", KnowledgeBaseID: testKB,
	})

	repo.topicStats = []types.MemoryTopicStat{
		{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "rag", Topic: "RAG"},
		{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "deploy", Topic: "Deploy"},
	}
	repo.affinity = []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1"},
	}

	fake.responses = []*types.ChatResponse{
		// Only the "rag" topic carries candidates; the adjudication input
		// must not contain any summary page.
		{Content: `{"maps":{"rag":{"slug":"concept/rag","confidence":0.9}}}`, FinishReason: "stop"},
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}

	if repo.maps["rag→concept/rag"] == nil {
		t.Fatalf("nodal topic must map: %+v", repo.maps)
	}
	for key, m := range repo.maps {
		if m.Slug == "summary/deploy" {
			t.Fatalf("topic mapped onto non-node page: %s", key)
		}
	}
	for _, e := range repo.snapshotEvents() {
		if e.Slug == "summary/deploy" {
			t.Fatalf("event landed on non-node page: %+v", e)
		}
	}
	// The topic prompt itself must carry node pages only.
	prompt, ok := fake.lastUserUnder(agent.LearningTopicMapPrompt)
	if !ok {
		t.Fatal("topic-mapping prompt never sent")
	}
	if strings.Contains(prompt, "summary/deploy") {
		t.Fatal("summary page leaked into the topic-mapping prompt")
	}
}

// TestMaintenanceInjectsTenantIntoModelCalls is the regression for the
// first live learning_features=true run crashing the server: the background
// maintenance context carries no tenant, and GetChatModel panics without
// one — every pass must inject the KB's tenant before touching the model
// channel.
func TestMaintenanceInjectsTenantIntoModelCalls(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, &types.WikiPage{
		Slug: "concept/rag", PageType: "concept", Title: "RAG", KnowledgeBaseID: testKB,
		ChunkRefs: types.StringArray{"c1"},
	})
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"c1": {ID: "c1", Content: "RAG evidence"},
	}}
	fake := &fakeChatModel{}
	kbs := &stubKBRepo{kbs: map[string]*types.KnowledgeBase{
		testKB: {ID: testKB, TenantID: 1, WikiConfig: &types.WikiConfig{LearningFeatures: true, SynthesisModelID: "m"}},
	}}
	if err := repo.UpsertMastery(t.Context(), &types.MasteryState{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag",
	}); err != nil {
		t.Fatal(err)
	}
	svc, models := maintenanceService(repo, wiki, chunks, fake, kbs)
	t.Setenv("LEARNING_ENABLE", "true")

	fake.responses = []*types.ChatResponse{
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
	}
	// The empty context is the point: it must not reach the model channel.
	if err := svc.RunMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() == 0 {
		t.Fatal("edge/quiz pass must have run model calls")
	}
	if !models.tenantOK {
		t.Fatal("model channel saw a tenant-less context")
	}
}

// TestMaintenanceModelGapSkipsQuietly: a KB with the gate on but no model
// configured skips the pass without touching the model.
func TestMaintenanceModelGapSkipsQuietly(t *testing.T) {
	svc, _, _, kbs, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	kbs.kbs[testKB].WikiConfig = &types.WikiConfig{LearningFeatures: true} // no model ids

	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() != 0 {
		t.Fatalf("model gap must skip silently, calls = %d", fake.calls.Load())
	}
}

// TestMaintenanceTopicMappingWatermarkSkipsSettled: a topic already mapped
// onto a live node of the KB is settled — the daily pass must not spend a
// model call re-adjudicating it — while a mapping onto a dead slug (page
// renamed/merged since) is NOT settled and is re-adjudicated.
func TestMaintenanceTopicMappingWatermarkSkipsSettled(t *testing.T) {
	svc, repo, _, kbs, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	kbs.kbs[testKB].WikiConfig = &types.WikiConfig{SynthesisModelID: "m"}

	repo.topicStats = []types.MemoryTopicStat{
		{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "rag", Topic: "RAG"},
		{TenantID: 1, SubjectID: "web_user:alice", NormalizedKey: "decay", Topic: "Decay"},
	}
	repo.affinity = []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, KnowledgeID: "d1"},
	}
	// Settled: maps onto a live node.
	_ = repo.UpsertTopicMap(t.Context(), &types.MemoryWikiMap{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		NormalizedTopicKey: "rag", Slug: "concept/rag", Confidence: 0.9, DecidedBy: types.LearningMapDecidedByLLM,
	})
	// Stale: maps onto a slug that is no longer a node of this KB.
	_ = repo.UpsertTopicMap(t.Context(), &types.MemoryWikiMap{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		NormalizedTopicKey: "decay", Slug: "concept/gone", Confidence: 0.9, DecidedBy: types.LearningMapDecidedByLLM,
	})

	fake.responses = []*types.ChatResponse{
		{Content: `{"maps":{"decay":{"slug":"concept/decay","confidence":0.9}}}`, FinishReason: "stop"},
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"},
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}

	prompt, ok := fake.lastUserUnder(agent.LearningTopicMapPrompt)
	if !ok {
		t.Fatal("topic adjudication never ran — stale mapping must be re-adjudicated")
	}
	if strings.Contains(prompt, `"rag"`) || strings.Contains(prompt, "RAG") {
		t.Fatalf("settled topic re-adjudicated, prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "decay") && !strings.Contains(prompt, "Decay") {
		t.Fatalf("stale topic missing from adjudication, prompt: %s", prompt)
	}
	if m := repo.maps["decay→concept/decay"]; m == nil || m.Confidence != 0.9 {
		t.Fatalf("re-adjudicated mapping not stored: %+v", repo.maps["decay→concept/decay"])
	}
}
