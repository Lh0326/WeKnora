package main

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestOnlineReplayUniverseGrowsAfterTouch（评审 P4 泄漏回归）：后缀里首次
// 出现的节点在它被触达之前不存在于任何 ranker 的候选宇宙——本导出只含被
// 触达过的节点，首触是所有 ranker 一致的必失；触达之后节点入宇宙，后续
// 重访可以被预测。三个 ranker 每步同池抽取。
func TestOnlineReplayUniverseGrowsAfterTouch(t *testing.T) {
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	mk := func(id, slug string, mins int) types.LearningEvent {
		return types.LearningEvent{
			ID: id, TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: "kb",
			Slug: slug, Type: types.LearningEventAnswerCite, Weight: 1,
			OccurredAt: base.Add(time.Duration(mins) * time.Minute),
		}
	}
	// Prefix: A and B touched. Suffix: C first-ever touch, then A revisit.
	events := []types.LearningEvent{
		mk("e1", "concept/a", 0),
		mk("e2", "concept/b", 10),
		mk("e3", "concept/a", 20),
		mk("e4", "concept/c", 40), // suffix, first touch of C
		mk("e5", "concept/a", 50), // suffix, revisit of A
	}
	splitIdx := 3

	recM, randM, popM := runOnlineReplay(events, splitIdx, []int{5}, nil)

	// Two suffix predictions, one per event.
	if recM.Predictions != 2 || randM.Predictions != 2 || popM.Predictions != 2 {
		t.Fatalf("predictions = %d/%d/%d, want 2/2/2", recM.Predictions, randM.Predictions, popM.Predictions)
	}
	// The first-touch of C is a guaranteed miss for every ranker: C was not
	// in the universe before its own touch, so no list could contain it.
	if recM.HitsByK[5] > 1 || randM.HitsByK[5] > 1 || popM.HitsByK[5] > 1 {
		t.Fatalf("a ranker hit a first-touch node: rec=%d rand=%d pop=%d (max 1, from the A revisit only)",
			recM.HitsByK[5], randM.HitsByK[5], popM.HitsByK[5])
	}
	// The A revisit IS predictable: A has been in the universe since the
	// prefix. Popularity must hit it (A is the most touched slug).
	if popM.HitsByK[5] != 1 {
		t.Fatalf("popularity must hit the A revisit, got hits=%d", popM.HitsByK[5])
	}
}

// A first quiz on an unknown node is unreachable. Quiz eligibility itself is
// independently tested with a known-prefix node in replay_protocol_test.go.
func TestOnlineReplayFirstQuizOnUnseenNodeIsUnreachable(t *testing.T) {
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	events := []types.LearningEvent{
		{ID: "e1", TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb",
			Slug: "concept/a", Type: types.LearningEventAnswerCite, Weight: 1, OccurredAt: base},
		{ID: "e2", TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb",
			Slug: "concept/a", Type: types.LearningEventAnswerCite, Weight: 1, OccurredAt: base.Add(10 * time.Minute)},
		// Suffix: the FIRST quiz event in the whole history lands on B,
		// which has never been touched before — a guaranteed miss for every
		// ranker. If HasQuiz were built from the full timeline (leak), the
		// recommendation input would still not rank B before its touch
		// (universe rule), which is exactly the defense this test pins.
		{ID: "e3", TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb",
			Slug: "concept/b", Type: types.LearningEventQuizCorrect, Weight: 2, OccurredAt: base.Add(40 * time.Minute)},
	}
	recM, randM, popM := runOnlineReplay(events, 2, []int{5}, nil)
	if recM.HitsByK[5] != 0 || randM.HitsByK[5] != 0 || popM.HitsByK[5] != 0 {
		t.Fatalf("first-touch quiz node must be a miss for all rankers: rec=%d rand=%d pop=%d",
			recM.HitsByK[5], randM.HitsByK[5], popM.HitsByK[5])
	}
}
