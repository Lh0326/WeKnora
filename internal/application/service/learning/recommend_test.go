package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TestRecommendOrderingRespondsToLearning is the regression lock for the
// "static #1" bug: within a rank, untouched (blind-spot) nodes must lead,
// and a node the learner just studied must sink below untouched peers —
// the list has to acknowledge every quiz answer and page read.
func TestRecommendOrderingRespondsToLearning(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/logit-accumulation", PageType: "concept", Title: "Logit累加模型"},
		{Slug: "concept/alpha", PageType: "concept", Title: "甲概念"},
		{Slug: "concept/beta", PageType: "concept", Title: "乙概念"},
	}
	// The learner studied logit heavily (3 wrong quizzes + reads): 5 evidence.
	studied := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-48 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-2 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-1 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now},
	})
	recs := recommendNodes(recommendInput{
		Pages:  pages,
		States: map[string]FoldState{"concept/logit-accumulation": studied},
	}, now, nil, 5)

	if len(recs) != 3 {
		t.Fatalf("recs = %d, want 3", len(recs))
	}
	// Untouched nodes lead; the studied node must have sunk to the bottom —
	// under the old title sort "Logit..." would still be #1 (ASCII < CJK).
	if recs[2].Slug != "concept/logit-accumulation" {
		t.Fatalf("studied node should sink to last, got order %v", slugList(recs))
	}
	if recs[0].Slug == "concept/logit-accumulation" {
		t.Fatal("static #1 bug regressed: studied node still first")
	}
}

// TestRecommendOrderingRecentFirst: equal evidence counts break ties by
// freshest activity — the learner's most recent thread comes next.
func TestRecommendOrderingRecentFirst(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/a", PageType: "concept", Title: "甲"},
		{Slug: "concept/b", PageType: "concept", Title: "乙"},
	}
	older := FoldAll(FoldState{}, []Event{{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-72 * time.Hour)}})
	fresh := FoldAll(FoldState{}, []Event{{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-1 * time.Hour)}})
	recs := recommendNodes(recommendInput{
		Pages:  pages,
		States: map[string]FoldState{"concept/a": older, "concept/b": fresh},
	}, now, nil, 5)
	if len(recs) != 2 || recs[0].Slug != "concept/b" {
		t.Fatalf("fresher node should lead, got %v", slugList(recs))
	}
}

func slugList(recs []Recommendation) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Slug)
	}
	return out
}

// TestRecommendReviewDueLeads: a node whose earned (undecayed) tier sits
// above its decayed view is the most time-sensitive recommendation — it
// must lead the list with the "review" reason (the spaced-repetition due
// queue), ahead of untouched frontier nodes.
func TestRecommendReviewDueLeads(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/fading", PageType: "concept", Title: "消退中"},
		{Slug: "concept/fresh-blind", PageType: "concept", Title: "新盲区"},
	}
	// fading: 2 cites long ago — logit 2.0 (p 0.88, anchor mastered-tier
	// band) decayed over ~120 days far below the mastered gate. The two
	// distinct-item corrects straddle the session gap, so the direct gate
	// actually grants the mastered anchor the citations folded.
	fading := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-120 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-120 * 24 * time.Hour)},
	})
	recs := recommendNodes(recommendInput{
		Pages:  pages,
		States: map[string]FoldState{"concept/fading": fading},
		DirectFacts: map[string][]DirectQuizFact{"concept/fading": {
			{ItemID: "q1", FirstCorrectAt: now.Add(-120 * 24 * time.Hour)},
			{ItemID: "q2", FirstCorrectAt: now.Add(-120 * 24 * time.Hour).Add(MasteredSessionGap + time.Hour)},
		}},
	}, now, nil, 5)
	if len(recs) < 2 {
		t.Fatalf("recs = %v, want at least 2", slugList(recs))
	}
	if recs[0].Slug != "concept/fading" || recs[0].Reason != "review" {
		t.Fatalf("due node must lead with reason review, got %s (%s)", recs[0].Slug, recs[0].Reason)
	}
}

// TestRecommendBypassNeighbor: a long-stuck prerequisite dams the node
// behind it. The spec's bypass: recommend the dam itself to shore up AND
// surface the blocked node's wiki-link neighbour (reason "bypass") instead
// of stalling — the neighbour must never be the dammed node itself.
func TestRecommendBypassNeighbor(t *testing.T) {
	now := time.Now()
	// The dam: 3 cites + 2 wrong quizzes 150 days ago — undecayed level
	// touched (p0 = 0.5) but decayed p_eff ≈ 0.27 < PrereqBlockedFloor,
	// with real evidence and last activity far outside the re-ask window.
	damEvents := []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
	}
	dam := FoldAll(FoldState{}, damEvents)
	pages := []*types.WikiPage{
		{Slug: "concept/dam", PageType: "concept", Title: "大坝"},
		// beyond's wiki-links: the dam (already shored) and the side path.
		{Slug: "concept/beyond", PageType: "concept", Title: "坝后", OutLinks: types.StringArray{"concept/dam", "concept/side"}},
		{Slug: "concept/side", PageType: "concept", Title: "邻道"},
	}
	edges := []types.LearningEdge{{
		TenantID: 1, KnowledgeBaseID: testKB,
		FromSlug: "concept/dam", ToSlug: "concept/beyond", Relation: types.LearningEdgePrerequisite,
	}}
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges,
		States: map[string]FoldState{"concept/dam": dam},
	}, now, nil, 5)

	found := map[string]string{}
	for _, r := range recs {
		found[r.Slug] = r.Reason
	}
	if found["concept/dam"] != "prerequisite-stuck" {
		t.Fatalf("dam must be shored up first, got %v", found)
	}
	if _, ok := found["concept/beyond"]; ok {
		t.Fatalf("dammed node must not enter the recommendation pool, got reason %q", found["concept/beyond"])
	}
	if found["concept/side"] != "bypass" {
		t.Fatalf("the dammed node's wiki-link neighbour must appear as bypass, got %v", found)
	}
}

// TestRecommendDamRequiresLongStuck: a prerequisite failing NOW (inside
// the re-ask window) is active learning, not a dam — the bypass must not
// fire and the blocked node simply stays gated.
func TestRecommendDamRequiresLongStuck(t *testing.T) {
	now := time.Now()
	active := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-1 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-30 * time.Minute)},
	})
	pages := []*types.WikiPage{
		{Slug: "concept/active", PageType: "concept", Title: "在学", OutLinks: types.StringArray{"concept/beyond"}},
		{Slug: "concept/beyond", PageType: "concept", Title: "坝后"},
		{Slug: "concept/side", PageType: "concept", Title: "邻道"},
	}
	edges := []types.LearningEdge{{
		TenantID: 1, KnowledgeBaseID: testKB,
		FromSlug: "concept/active", ToSlug: "concept/beyond", Relation: types.LearningEdgePrerequisite,
	}}
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges,
		States: map[string]FoldState{"concept/active": active},
	}, now, nil, 5)
	for _, r := range recs {
		if r.Reason == "prerequisite-stuck" || r.Reason == "bypass" {
			t.Fatalf("freshly-failing prerequisite is not a dam, got %s (%s)", r.Slug, r.Reason)
		}
	}
}

// TestRecommendRemedialMistakeNotebook: a node with a real, recoverable
// struggle history (quiz wrong + negative logit above the floor) leads the
// list as the mistake-notebook card — drill the direct failure before
// indirect struggle, and never surface a floor-clamped node as remedial
// (that one is a fresh start again).
func TestRecommendRemedialMistakeNotebook(t *testing.T) {
	now := time.Now()
	// quiz-failed node: cite + re_ask + quiz_wrong (3 evidence, logit
	// −1.3) — direct, recoverable struggle.
	quizFailed := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-27 * time.Hour)},
		{Type: types.LearningEventReAsk, Weight: WeightReAsk, OccurredAt: now.Add(-26 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-25 * time.Hour)},
	})
	// re_ask-struggled node: logit −0.6, indirect (3 evidence).
	reAsked := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-26 * time.Hour)},
		{Type: types.LearningEventReAsk, Weight: WeightReAsk, OccurredAt: now.Add(-25 * time.Hour)},
		{Type: types.LearningEventReAsk, Weight: WeightReAsk, OccurredAt: now.Add(-24 * time.Hour)},
	})
	// hopeless node: logit −3.5 (p ≈ 3%) — below the recoverable floor,
	// fresh start again, never a remedial card.
	hopeless := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-26 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-25 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-24 * time.Hour)},
	})
	pages := []*types.WikiPage{
		{Slug: "concept/quiz-failed", PageType: "concept", Title: "甲"},
		{Slug: "concept/re-asked", PageType: "concept", Title: "乙"},
		{Slug: "concept/hopeless", PageType: "concept", Title: "丙"},
		{Slug: "concept/fresh", PageType: "concept", Title: "丁"},
	}
	recs := recommendNodes(recommendInput{
		Pages: pages,
		States: map[string]FoldState{
			"concept/quiz-failed": quizFailed,
			"concept/re-asked":    reAsked,
			"concept/hopeless":    hopeless,
		},
		QuizStruggled: map[string]bool{"concept/quiz-failed": true},
	}, now, nil, 5)

	if len(recs) < 4 {
		t.Fatalf("recs = %v, want 4", slugList(recs))
	}
	// Direct failure drills first; indirect struggle second.
	if recs[0].Slug != "concept/quiz-failed" || recs[0].Reason != "remedial" {
		t.Fatalf("quiz-failed node must lead the notebook, got %s (%s)", recs[0].Slug, recs[0].Reason)
	}
	// Direct failure drills first (reason "remedial" — a real wrong
	// answer); indirect struggle second under its own honest label
	// "struggling" (re-asks only, no quiz failure ever happened).
	if recs[1].Slug != "concept/re-asked" || recs[1].Reason != "struggling" {
		t.Fatalf("indirect struggle comes second as struggling, got %s (%s)", recs[1].Slug, recs[1].Reason)
	}
	// The floor-clamped node is NOT part of the notebook.
	for _, r := range recs {
		if r.Slug == "concept/hopeless" && r.Reason == "remedial" {
			t.Fatal("floor-clamped node must not be remedial (it is a fresh start)")
		}
	}
}

// TestRecommendSelfVerifyOutranksInferredLabels is the round-40 regression
// lock for the user-reported mismatch: a node self-assessed down
// ("题目太简单") minutes ago must NOT wear the 错题重练 label built from
// five-day-old wrong answers — the wrongs are exactly what the user just
// re-contextualised. The freshest explicit claim is the reason, and the
// self-verify channel leads the list.
func TestRecommendSelfVerifyOutranksInferredLabels(t *testing.T) {
	now := time.Now()
	// claimed: textbook remedial shape — 2 correct + 3 wrong ≈ logit −0.1
	// (recoverable struggle, 5 evidence) with real wrong answers on record.
	claimed := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-124 * time.Hour)},
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-123 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-122 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-121 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-120 * time.Hour)},
	})
	struggler := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-26 * time.Hour)},
		{Type: types.LearningEventReAsk, Weight: WeightReAsk, OccurredAt: now.Add(-25 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-24 * time.Hour)},
	})
	pages := []*types.WikiPage{
		{Slug: "concept/claimed", PageType: "concept", Title: "甲"},
		{Slug: "concept/struggler", PageType: "concept", Title: "乙"},
	}
	recs := recommendNodes(recommendInput{
		Pages:    pages,
		States:   map[string]FoldState{"concept/claimed": claimed, "concept/struggler": struggler},
		QuizStruggled: map[string]bool{"concept/claimed": true, "concept/struggler": true},
		SelfAssess: map[string]interfaces.SelfAssessMark{
			"concept/claimed": {Direction: "down", EventType: "self_assess_down_quiz_easy", At: now.Add(-40 * time.Minute)},
		},
	}, now, nil, 5)

	if len(recs) < 2 {
		t.Fatalf("recs = %v, want 2", slugList(recs))
	}
	// The claimed node leads with the honest label — not 错题重练.
	if recs[0].Slug != "concept/claimed" || recs[0].Reason != "self-verify" {
		t.Fatalf("claimed node must lead as self-verify, got %s (%s)", recs[0].Slug, recs[0].Reason)
	}
	// The unclaimed struggler keeps the ordinary notebook label.
	if recs[1].Slug != "concept/struggler" || recs[1].Reason != "remedial" {
		t.Fatalf("struggler stays remedial, got %s (%s)", recs[1].Slug, recs[1].Reason)
	}
}

// TestRecommendSelfVerifyFreshestFirst: two pending claims — the more
// recent conversation leads (an up-claim and a down-claim both await proof;
// recency is the only fair tiebreak between them).
func TestRecommendSelfVerifyFreshestFirst(t *testing.T) {
	now := time.Now()
	touched := func(hoursAgo float64) FoldState {
		return FoldAll(FoldState{}, []Event{
			{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-time.Duration(hoursAgo) * time.Hour)},
		})
	}
	pages := []*types.WikiPage{
		{Slug: "concept/older-claim", PageType: "concept", Title: "甲"},
		{Slug: "concept/fresher-claim", PageType: "concept", Title: "乙"},
	}
	recs := recommendNodes(recommendInput{
		Pages:  pages,
		States: map[string]FoldState{"concept/older-claim": touched(30), "concept/fresher-claim": touched(30)},
		SelfAssess: map[string]interfaces.SelfAssessMark{
			"concept/older-claim":   {Direction: "down", EventType: "self_assess_down_all", At: now.Add(-6 * time.Hour)},
			"concept/fresher-claim": {Direction: "up", EventType: "self_assess_up", At: now.Add(-20 * time.Minute)},
		},
	}, now, nil, 5)
	if len(recs) != 2 || recs[0].Slug != "concept/fresher-claim" || recs[0].Reason != "self-verify" {
		t.Fatalf("fresher claim must lead as self-verify, got %v", slugList(recs))
	}
	if recs[1].Slug != "concept/older-claim" || recs[1].Reason != "self-verify" {
		t.Fatalf("older claim second as self-verify, got %s (%s)", recs[1].Slug, recs[1].Reason)
	}
}
