package learning

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// quizFixtureEvidence is the fixture page's evidence (must match
// maintenanceFixture's concept/rag page and chunk c1).
func quizFixtureEvidence() (*types.WikiPage, map[string]string) {
	return &types.WikiPage{
			Slug: "concept/rag", PageType: "concept", Title: "RAG", KnowledgeBaseID: testKB,
			Content: "RAG combines retrieval and generation.",
		}, map[string]string{
			"c1": "RAG evidence one",
		}
}

// TestQuizEvidenceStaleOutAndRegeneration（评审 P2-D 内容版本回归）：证据
// 变更后，绑定旧版本的题目必须立即停止出题与计分（stale），同一轮维护内
// 重新生成绑定新版本的新题；证据未变时不误伤。
func TestQuizEvidenceStaleOutAndRegeneration(t *testing.T) {
	svc, repo, _, _, fake := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	page, evidence := quizFixtureEvidence()
	currentHash := quizEvidenceHash(page, evidence)

	// 1) A bank bound to the CURRENT evidence survives untouched. Seed to
	// the cap so this page consumes no quiz model call of its own.
	seeded := &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "What does RAG combine?", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "Retrieval and generation", "B": "x", "C": "y", "D": "z"},
		Explanation: "From the page.", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusActive, EvidenceHash: currentHash,
	}
	for i := 0; i < QuizItemsPerSlug; i++ {
		clone := *seeded
		clone.ID = fmt.Sprintf("seeded-%d", i)
		if err := repo.UpsertQuizItem(t.Context(), &clone); err != nil {
			t.Fatal(err)
		}
	}
	fake.responses = []*types.ChatResponse{
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: `{"questions":[]}`, FinishReason: "stop"}, // decay only: rag is at cap
	}
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	for _, it := range repo.quizItems {
		if it.Slug == "concept/rag" && it.Status != types.LearningQuizStatusActive {
			t.Fatalf("item bound to current evidence must stay active, got %q", it.Status)
		}
	}
	repo.mu.Unlock()

	// 2) The evidence changes (the cited chunk is rewritten). The next pass
	// must stale the drifted item and regenerate from the new material.
	svc.chunkRepo.(*stubChunkRepo).chunks["c1"] = &types.Chunk{ID: "c1", Content: "RAG evidence REVISED"}
	newHash := quizEvidenceHash(page, map[string]string{"c1": "RAG evidence REVISED"})
	if newHash == currentHash {
		t.Fatal("test setup: revised evidence must change the hash")
	}
	// Page iteration order from the stub is map-random, so every quiz-slot
	// response carries a question for EACH page; only the matching page's
	// question survives its grounding validation.
	qBoth := `{"questions":[` +
		`{"question":"What does revised RAG cite?","options":{"A":"New material","B":"x","C":"y","D":"z"},"correct_key":"A","explanation":"From the revised chunk.","chunk_refs":["c1"]},` +
		`{"question":"What is decay about?","options":{"A":"Forgetting","B":"x","C":"y","D":"z"},"correct_key":"A","explanation":"From the decay chunk.","chunk_refs":["c2"]}]}`
	fake.responses = []*types.ChatResponse{
		{Content: `{"pairs":[]}`, FinishReason: "stop"},
		{Content: qBoth, FinishReason: "stop"},
		{Content: qBoth, FinishReason: "stop"},
	}
	fake.calls.Store(0) // the fake indexes responses by global call count
	if err := svc.RunMaintenance(t.Context()); err != nil {
		t.Fatal(err)
	}

	var staledIDs []string
	var freshItems []types.LearningQuizItem
	repo.mu.Lock()
	for _, it := range repo.quizItems {
		if it.Slug != "concept/rag" {
			continue
		}
		switch it.Status {
		case types.LearningQuizStatusStale:
			staledIDs = append(staledIDs, it.ID)
		case types.LearningQuizStatusDraft:
			// Stage 2: regenerated items enter as LLM drafts.
			freshItems = append(freshItems, it)
		}
	}
	repo.mu.Unlock()
	if len(staledIDs) != QuizItemsPerSlug {
		t.Fatalf("drifted bank must be staled whole, got %d", len(staledIDs))
	}
	if len(freshItems) == 0 {
		t.Fatal("staled bank must regenerate in the same pass")
	}
	for _, it := range freshItems {
		if it.EvidenceHash != newHash {
			t.Fatalf("regenerated item must bind the new evidence hash, got %q", it.EvidenceHash)
		}
	}
}

// TestQuizLegacyUnversionedItemsGoStale：迁移前生成的题目（空哈希=版本未知）
// 在第一个能计算摘要的维护轮全部失效——宁可再生，不可继续按未知版本计分。
func TestQuizLegacyUnversionedItemsGoStale(t *testing.T) {
	repo := newStubRepo()
	t.Setenv("LEARNING_ENABLE", "true")
	if err := repo.UpsertQuizItem(t.Context(), &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "legacy", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		Explanation: "old", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusActive, // EvidenceHash: "" (legacy)
	}); err != nil {
		t.Fatal(err)
	}
	// A human-disabled item must never be touched by the stale-out.
	disabled := &types.LearningQuizItem{
		TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "kept disabled", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		Explanation: "old", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusDisabled,
	}
	if err := repo.UpsertQuizItem(t.Context(), disabled); err != nil {
		t.Fatal(err)
	}

	staled, err := repo.StaleQuizItemsByEvidence(t.Context(), 1, testKB, "concept/rag", "somehash")
	if err != nil {
		t.Fatal(err)
	}
	if staled != 1 {
		t.Fatalf("legacy unversioned item must go stale, got %d staled", staled)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, it := range repo.quizItems {
		if it.Question == "legacy" && it.Status != types.LearningQuizStatusStale {
			t.Fatalf("legacy item must be stale, got %q", it.Status)
		}
		if it.Question == "kept disabled" && it.Status != types.LearningQuizStatusDisabled {
			t.Fatalf("disabled item must never be touched, got %q", it.Status)
		}
	}
}

// TestStaleQuizNotServedOrScored：stale 题目既不进入 TakeQuiz 的可见题面，
// 也不能通过 SubmitAnswer 计分——失效必须同时停掉出题和计分两侧。
func TestStaleQuizNotServedOrScored(t *testing.T) {
	svc, repo, _, _, _ := maintenanceFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	item := &types.LearningQuizItem{
		ID: "quiz-stale-1", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "stale question", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		Explanation: "old", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusStale,
	}
	if err := repo.UpsertQuizItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}

	ctx := collectorCtx(1, "alice")
	questions, err := svc.TakeQuiz(ctx, testKB, "concept/rag")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range questions {
		if q.ID == item.ID {
			t.Fatal("stale item must not be served by TakeQuiz")
		}
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, item.ID, "A", ""); err == nil {
		t.Fatal("stale item must not be scoreable via SubmitAnswer")
	}
}
