package learning

import (
	"fmt"
	"math/rand"
	"strings"
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
		HasQuiz: map[string]bool{"concept/fresher-claim": true},
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

// TestRecommendColdStartFollowsDocumentOrder: the 从浅入深 regression lock.
// A cold start (no states at all) must read in the order the source
// material introduces its nodes — chapter 1 before chapter 2 — not in the
// title-alphabetical order that used to decide every equal-score tie. The
// three earliest never-touched nodes additionally carry the "foundation"
// reason and its bonus, so the on-ramp leads even among equal-score peers.
func TestRecommendColdStartFollowsDocumentOrder(t *testing.T) {
	now := time.Now()
	// Titles deliberately invert the reading order: alphabetical sort
	// would put chapter 3's "A主题" first and chapter 1's "丙基础" last.
	pages := []*types.WikiPage{
		{Slug: "concept/ch3-topic", PageType: "concept", Title: "A主题"},  // chapter 3
		{Slug: "concept/ch2-topic", PageType: "concept", Title: "乙进阶"}, // chapter 2
		{Slug: "concept/ch1-basics", PageType: "concept", Title: "丙基础"}, // chapter 1
		{Slug: "concept/ch1-more", PageType: "concept", Title: "丁入门"},  // chapter 1, later
		{Slug: "concept/ch0-intro", PageType: "concept", Title: "戊导论"}, // chapter 0
	}
	docOrder := map[string]int{
		"concept/ch0-intro":   0,
		"concept/ch1-basics":  1,
		"concept/ch1-more":    2,
		"concept/ch2-topic":   3,
		"concept/ch3-topic":   4,
	}
	recs := recommendNodes(recommendInput{Pages: pages, DocOrder: docOrder}, now, nil, 5)
	got := slugList(recs)
	want := []string{
		"concept/ch0-intro", "concept/ch1-basics", "concept/ch1-more",
		"concept/ch2-topic", "concept/ch3-topic",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cold start must follow document order, got %v", got)
		}
	}
	// The five earliest never-touched nodes are the foundation window:
	// a cold start's whole default list reads in material order.
	for i := 0; i < 5; i++ {
		if recs[i].Reason != "foundation" {
			t.Fatalf("rec[%d] reason = %s, want foundation", i, recs[i].Reason)
		}
	}
}

// TestRecommendExplorationStaysOnTheOnRamp: ε-exploration draws only from
// the earliest ExploreWindow candidates — the diversity dice roll may not
// hand a day-one learner a chapter-20 card.
func TestRecommendExplorationStaysOnTheOnRamp(t *testing.T) {
	now := time.Now()
	const many = 30
	pages := make([]*types.WikiPage, 0, many)
	docOrder := map[string]int{}
	for i := 0; i < many; i++ {
		slug := "concept/ch" + fmt.Sprintf("%02d", i)
		pages = append(pages, &types.WikiPage{Slug: slug, PageType: "concept", Title: string(rune('A' + i%26)) + fmt.Sprint(i)})
		docOrder[slug] = i
	}
	// Sweep seeds until one triggers exploration, then bound the pick.
	for seed := int64(0); seed < 200; seed++ {
		recs := recommendNodes(recommendInput{Pages: pages, DocOrder: docOrder},
			now, rand.New(rand.NewSource(seed)), 5)
		explored := false
		for _, r := range recs {
			if r.Reason == "explore" {
				explored = true
				if rank := docOrder[r.Slug]; rank >= ExploreWindow {
					t.Fatalf("explore pick %s ranks %d, must stay within the earliest %d",
						r.Slug, rank, ExploreWindow)
				}
			}
		}
		if explored {
			return
		}
	}
	t.Fatal("no seed triggered exploration in 200 tries — check RecommendEpsilon wiring")
}

// TestRecommendFoundationWindowSlides: once the earliest nodes are touched,
// the foundation window slides to the next never-touched nodes in reading
// order — the on-ramp follows the learner, it does not pin the book's head.
func TestRecommendFoundationWindowSlides(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/ch1-a", PageType: "concept", Title: "一"},
		{Slug: "concept/ch1-b", PageType: "concept", Title: "二"},
		{Slug: "concept/ch2-a", PageType: "concept", Title: "三"},
		{Slug: "concept/ch3-a", PageType: "concept", Title: "四"},
	}
	docOrder := map[string]int{
		"concept/ch1-a": 0, "concept/ch1-b": 1, "concept/ch2-a": 2, "concept/ch3-a": 3,
	}
	studied := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-24 * time.Hour)},
	})
	// Slides can also come from skips: retire the two earliest and the
	// window must hand its slots to the next never-touched nodes.
	skippedRecs := recommendNodes(recommendInput{
		Pages: pages, DocOrder: docOrder,
		Skips: map[string]bool{"concept/ch1-a": true, "concept/ch1-b": true},
	}, now, nil, 4)
	if skippedRecs[0].Slug != "concept/ch2-a" || skippedRecs[0].Reason != "foundation" {
		t.Fatalf("skip must slide the window to ch2-a, got %s (%s)", skippedRecs[0].Slug, skippedRecs[0].Reason)
	}
	recs := recommendNodes(recommendInput{
		Pages: pages, DocOrder: docOrder,
		States: map[string]FoldState{"concept/ch1-a": studied, "concept/ch1-b": studied},
	}, now, nil, 4)
	// The touched chapter-1 nodes sink below every never-touched peer; the
	// foundation window now covers ch2-a (and ch3-a).
	if recs[0].Slug != "concept/ch2-a" || recs[0].Reason != "foundation" {
		t.Fatalf("window must slide to ch2-a (foundation), got %s (%s)", recs[0].Slug, recs[0].Reason)
	}
	if recs[1].Slug != "concept/ch3-a" || recs[1].Reason != "foundation" {
		t.Fatalf("window must cover ch3-a (foundation), got %s (%s)", recs[1].Slug, recs[1].Reason)
	}
}

// TestRecommendDocOrderTiebreakWithoutWindow: two seen nodes with equal
// signals order by document position — the reading order survives past the
// foundation window as the tiebreak before title alphabet.
func TestRecommendDocOrderTiebreakWithoutWindow(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/late-doc", PageType: "concept", Title: "乙"}, // later in the book
		{Slug: "concept/early-doc", PageType: "concept", Title: "甲"}, // earlier in the book
		{Slug: "concept/untouched", PageType: "concept", Title: "丙"},
	}
	docOrder := map[string]int{"concept/early-doc": 0, "concept/late-doc": 7, "concept/untouched": 9}
	seen := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-48 * time.Hour)},
	})
	recs := recommendNodes(recommendInput{
		Pages: pages, DocOrder: docOrder,
		States: map[string]FoldState{"concept/early-doc": seen, "concept/late-doc": seen},
	}, now, nil, 3)
	// The untouched node is the only never-touched page, so the foundation
	// window is exactly it and it leads; the two equally-seen nodes then
	// break their tie by document position (early before late) rather than
	// by title alphabet.
	if recs[0].Slug != "concept/untouched" {
		t.Fatalf("the sole never-touched node leads, got %v", slugList(recs))
	}
	if recs[1].Slug != "concept/early-doc" || recs[2].Slug != "concept/late-doc" {
		t.Fatalf("equal-signal seen nodes must order by document position, got %v", slugList(recs))
	}
}

// ---- skip list (已掌握，不再推荐) ----

// TestRecommendSkipsSuppressEveryChannel: a skipped node never appears —
// not as a frontier candidate, not as an exploration pick — while its
// neighbours move up. The queue the user sees after skipping is exactly
// the queue they asked for.
func TestRecommendSkipsSuppressEveryChannel(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/known", PageType: "concept", Title: "已掌握"},
		{Slug: "concept/next", PageType: "concept", Title: "下一站"},
	}
	recs := recommendNodes(recommendInput{
		Pages: pages, Skips: map[string]bool{"concept/known": true},
	}, now, nil, 2)
	if len(recs) != 1 || recs[0].Slug != "concept/next" {
		t.Fatalf("skipped node must vanish from every channel, got %v", slugList(recs))
	}
}

// TestRecommendSkippedPrereqSatisfiesGate: the user skipped the chapter-1
// node ("看一眼就会了"), so the frontier gate must treat it as satisfied —
// the chapter-2 node behind it becomes reachable instead of bricked.
func TestRecommendSkippedPrereqSatisfiesGate(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/ch1", PageType: "concept", Title: "一"},
		{Slug: "concept/ch2", PageType: "concept", Title: "二"},
	}
	edges := []types.LearningEdge{{
		FromSlug: "concept/ch1", ToSlug: "concept/ch2",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}}
	// Without the skip the gate holds ch2 back (ch1 unseen).
	plain := recommendNodes(recommendInput{Pages: pages, Edges: edges}, now, nil, 2)
	if len(plain) != 1 || plain[0].Slug != "concept/ch1" {
		t.Fatalf("unseen prereq must gate ch2, got %v", slugList(plain))
	}
	// With the skip ch1 is gone AND ch2 is reachable.
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, Skips: map[string]bool{"concept/ch1": true},
	}, now, nil, 2)
	if len(recs) != 1 || recs[0].Slug != "concept/ch2" {
		t.Fatalf("skipped prereq must satisfy the gate, got %v", slugList(recs))
	}
}

// TestRecommendSkippedDamDoesNotShoreUp: a prerequisite the user skipped
// must not come back as a shore-up card — that would be the exact nag the
// skip forbids; the blocked node behind it becomes an ordinary candidate.
func TestRecommendSkippedDamDoesNotShoreUp(t *testing.T) {
	now := time.Now()
	// The dam: 3 cites + 2 wrongs 150 days ago — touched anchor, decayed
	// p_eff below the blocked floor, last activity far outside the window.
	dam := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
	})
	pages := []*types.WikiPage{
		{Slug: "concept/dam", PageType: "concept", Title: "水坝"},
		{Slug: "concept/behind", PageType: "concept", Title: "坝后"},
	}
	edges := []types.LearningEdge{{
		FromSlug: "concept/dam", ToSlug: "concept/behind",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}}
	// Sanity: without the skip the dam shores up.
	plain := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, States: map[string]FoldState{"concept/dam": dam},
	}, now, nil, 2)
	foundStuck := false
	for _, r := range plain {
		if r.Reason == "prerequisite-stuck" {
			foundStuck = true
		}
	}
	if !foundStuck {
		t.Fatalf("unskipped dam must surface a shore-up card, got %v", plain)
	}
	// Skipped: no shore-up, no dam card at all.
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, States: map[string]FoldState{"concept/dam": dam},
		Skips: map[string]bool{"concept/dam": true},
	}, now, nil, 2)
	if len(recs) != 1 || recs[0].Slug != "concept/behind" {
		t.Fatalf("skipped dam must not nag, got %v", recs)
	}
}

// TestRecommendSelfVerifyNeedsProvingGround (task-3 lock): an up-claim
// leads the list ONLY while quiz items exist to verify it against. A
// pinned "please verify" card with no way to verify is noise — the node
// falls back to its ordinary channel until the quiz pass generates items.
func TestRecommendSelfVerifyNeedsProvingGround(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/claimed", PageType: "concept", Title: "甲"},
		{Slug: "concept/other", PageType: "concept", Title: "乙"},
	}
	mark := map[string]interfaces.SelfAssessMark{
		"concept/claimed": {Direction: "up", EventType: "self_assess_up", At: now.Add(-time.Hour)},
	}
	// No quiz anywhere: the claim must NOT pin the node.
	noQuiz := recommendNodes(recommendInput{
		Pages: pages, SelfAssess: mark,
	}, now, nil, 2)
	if noQuiz[0].Reason == "self-verify" {
		t.Fatalf("up-claim without quiz must not lead, got %v", noQuiz)
	}
	// Quiz exists: the claim leads as self-verify.
	withQuiz := recommendNodes(recommendInput{
		Pages: pages, SelfAssess: mark, HasQuiz: map[string]bool{"concept/claimed": true},
	}, now, nil, 2)
	if withQuiz[0].Slug != "concept/claimed" || withQuiz[0].Reason != "self-verify" {
		t.Fatalf("up-claim with a proving ground must lead as self-verify, got %v", withQuiz)
	}
	// A down-claim always leads: its follow-up (re-learning) needs no quiz.
	down := recommendNodes(recommendInput{
		Pages: pages,
		SelfAssess: map[string]interfaces.SelfAssessMark{
			"concept/claimed": {Direction: "down", EventType: "self_assess_down_all", At: now.Add(-time.Hour)},
		},
	}, now, nil, 2)
	if down[0].Slug != "concept/claimed" || down[0].Reason != "self-verify" {
		t.Fatalf("down-claim must lead as self-verify, got %v", down)
	}
}

// ---- round-2 review locks: edge-table anomalies must never brick nodes ----

// TestRecommendPrereqCycleDegradesToReady: an adjudicated prerequisite cycle
// (a↔b) is unsatisfiable — every member waits on another member. The queue
// must degrade honestly (serve the members) instead of silently emptying
// every channel for that component.
func TestRecommendPrereqCycleDegradesToReady(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/a", PageType: "concept", Title: "甲"},
		{Slug: "concept/b", PageType: "concept", Title: "乙"},
		{Slug: "concept/free", PageType: "concept", Title: "丙"},
	}
	edges := []types.LearningEdge{
		{FromSlug: "concept/a", ToSlug: "concept/b", Relation: types.LearningEdgePrerequisite, Confidence: 0.9},
		{FromSlug: "concept/b", ToSlug: "concept/a", Relation: types.LearningEdgePrerequisite, Confidence: 0.9},
	}
	recs := recommendNodes(recommendInput{Pages: pages, Edges: edges}, now, nil, 5)
	got := map[string]bool{}
	for _, r := range recs {
		got[r.Slug] = true
	}
	if !got["concept/a"] || !got["concept/b"] {
		t.Fatalf("cycle members must surface as ready, got %v", slugList(recs))
	}
	if !got["concept/free"] {
		t.Fatalf("unrelated node still served, got %v", slugList(recs))
	}
}

// TestRecommendDanglingPrereqIgnored: an edge whose prerequisite page no
// longer exists can never be satisfied by learning — it must not gate the
// target, and the dead slug must not become a shore-up card either.
func TestRecommendDanglingPrereqIgnored(t *testing.T) {
	now := time.Now()
	// The dead prerequisite carries dam-shaped state: real evidence, weak,
	// stale. Only its missing page saves the queue from a phantom dam.
	dam := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
	})
	pages := []*types.WikiPage{
		{Slug: "concept/target", PageType: "concept", Title: "目标"}, // the only page: prereq page is gone
	}
	edges := []types.LearningEdge{
		{FromSlug: "concept/ghost", ToSlug: "concept/target", Relation: types.LearningEdgePrerequisite, Confidence: 0.9},
	}
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, States: map[string]FoldState{"concept/ghost": dam},
	}, now, nil, 5)
	if len(recs) != 1 || recs[0].Slug != "concept/target" {
		t.Fatalf("dangling prereq must not gate the target, got %v", recs)
	}
	if recs[0].Reason == "prerequisite-stuck" {
		t.Fatal("a page-less dam must not surface as a shore-up card")
	}
}

// TestRecommendSelfVerifyLeadsOverShoreUp: the assembly order must follow
// the documented channel priority — the user's pending claim leads even
// when shore-up cards would otherwise fill every slot.
func TestRecommendSelfVerifyLeadsOverShoreUp(t *testing.T) {
	now := time.Now()
	damEvents := func() FoldState {
		return FoldAll(FoldState{}, []Event{
			{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
			{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
			{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-150 * 24 * time.Hour)},
			{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
			{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-149 * 24 * time.Hour)},
		})
	}
	states := map[string]FoldState{}
	edges := []types.LearningEdge{}
	pages := []*types.WikiPage{
		{Slug: "concept/claimed", PageType: "concept", Title: "我的认定"},
	}
	// Five dams, each gating its own target: the old assembly would fill
	// all 5 slots with prerequisite-stuck and never show the claim.
	for _, name := range []string{"dam-a", "dam-b", "dam-c", "dam-d", "dam-e"} {
		damSlug := "concept/" + name
		tgtSlug := "concept/behind-" + name
		states[damSlug] = damEvents()
		edges = append(edges, types.LearningEdge{
			FromSlug: damSlug, ToSlug: tgtSlug, Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
		})
		pages = append(pages,
			&types.WikiPage{Slug: damSlug, PageType: "concept", Title: name},
			&types.WikiPage{Slug: tgtSlug, PageType: "concept", Title: "behind " + name},
		)
	}
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, States: states,
		HasQuiz: map[string]bool{"concept/claimed": true},
		SelfAssess: map[string]interfaces.SelfAssessMark{
			"concept/claimed": {Direction: "up", EventType: "self_assess_up", At: now.Add(-time.Hour)},
		},
	}, now, nil, 5)
	if len(recs) == 0 || recs[0].Slug != "concept/claimed" || recs[0].Reason != "self-verify" {
		t.Fatalf("the pending claim must lead the list, got %v", recs)
	}
	// Shore-up cards still appear, but capped at half the slots.
	stuck := 0
	for _, r := range recs {
		if r.Reason == "prerequisite-stuck" {
			stuck++
		}
	}
	if stuck > 2 {
		t.Fatalf("shore-up cards must cap at limit/2, got %d of %d", stuck, len(recs))
	}
	if stuck == 0 {
		t.Fatal("dams must still surface (capped), got none")
	}
}

// TestRecommendSelfEdgeIgnored: a stored self-edge is a tautology that
// would gate the node on itself; the reader must ignore it.
func TestRecommendSelfEdgeIgnored(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{{Slug: "concept/self", PageType: "concept", Title: "自指"}}
	edges := []types.LearningEdge{
		{FromSlug: "concept/self", ToSlug: "concept/self", Relation: types.LearningEdgePrerequisite, Confidence: 0.9},
	}
	recs := recommendNodes(recommendInput{Pages: pages, Edges: edges}, now, nil, 2)
	if len(recs) != 1 || recs[0].Slug != "concept/self" {
		t.Fatalf("self-edge must not gate its own node, got %v", recs)
	}
}

// ---- 首访让位 (attended-release): pressure pins step aside after a real
// visit, quick flips never disturb the path, and pure-browse decay leaves
// recommendations entirely (verified-only review channel). ----

func TestRecommendSelfVerifyReleasedByVisit(t *testing.T) {
	now := time.Now()
	claimed := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-124 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-120 * time.Hour)},
	})
	other := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-24 * time.Hour)},
	})
	pages := []*types.WikiPage{
		{Slug: "concept/claimed", PageType: "concept", Title: "甲"},
		{Slug: "concept/other", PageType: "concept", Title: "乙"},
	}
	base := recommendInput{
		Pages:         pages,
		States:        map[string]FoldState{"concept/claimed": claimed, "concept/other": other},
		QuizStruggled: map[string]bool{"concept/claimed": true, "concept/other": true},
		SelfAssess: map[string]interfaces.SelfAssessMark{
			"concept/claimed": {Direction: "down", EventType: "self_assess_down_quiz_easy", At: now.Add(-2 * time.Hour)},
		},
		HasQuiz: map[string]bool{"concept/claimed": true, "concept/other": true},
	}
	// No visit data: the pin holds (round-40 semantics preserved).
	recs := recommendNodes(base, now, nil, 5)
	if len(recs) == 0 || recs[0].Slug != "concept/claimed" || recs[0].Reason != "self-verify" {
		t.Fatalf("unvisited claim must pin as self-verify, got %v", slugReasonList(recs))
	}
	// A deliberate read AFTER the claim releases the pin: the node returns
	// to ordinary ranking (its recent visit also makes it ineligible for
	// the remedial pin this pass).
	relaxed := base
	relaxed.LastVisit = map[string]time.Time{"concept/claimed": now.Add(-1 * time.Hour)}
	recs = recommendNodes(relaxed, now, nil, 5)
	for _, r := range recs {
		if r.Slug == "concept/claimed" && r.Reason == "self-verify" {
			t.Fatalf("visited-after-claim must release the self-verify pin, got %v", slugReasonList(recs))
		}
	}
	// A visit BEFORE the claim does not release: the claim is the newer
	// conversation the system must still answer.
	stale := base
	stale.LastVisit = map[string]time.Time{"concept/claimed": now.Add(-3 * time.Hour)}
	recs = recommendNodes(stale, now, nil, 5)
	if len(recs) == 0 || recs[0].Slug != "concept/claimed" || recs[0].Reason != "self-verify" {
		t.Fatalf("visit older than the claim must not release, got %v", slugReasonList(recs))
	}
}

func TestRecommendRemedialReleasedByRecentVisit(t *testing.T) {
	now := time.Now()
	struggler := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-26 * time.Hour)},
		{Type: types.LearningEventReAsk, Weight: WeightReAsk, OccurredAt: now.Add(-25 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-24 * time.Hour)},
	})
	pages := []*types.WikiPage{{Slug: "concept/s", PageType: "concept", Title: "乙"}}
	base := recommendInput{
		Pages:         pages,
		States:        map[string]FoldState{"concept/s": struggler},
		QuizStruggled: map[string]bool{"concept/s": true},
	}
	// Old visit (outside the re-ask window): the drill pin keeps its turn.
	old := base
	old.LastVisit = map[string]time.Time{"concept/s": now.Add(-72 * time.Hour)}
	recs := recommendNodes(old, now, nil, 5)
	if len(recs) == 0 || recs[0].Reason != "remedial" {
		t.Fatalf("stale struggle keeps the remedial pin, got %v", slugReasonList(recs))
	}
	// Fresh visit inside the window: the user just faced this node — the
	// pin steps aside (card may still appear via ordinary ranking, but
	// never as the remedial leader).
	fresh := base
	fresh.LastVisit = map[string]time.Time{"concept/s": now.Add(-1 * time.Hour)}
	recs = recommendNodes(fresh, now, nil, 5)
	for _, r := range recs {
		if r.Slug == "concept/s" && (r.Reason == "remedial" || r.Reason == "struggling") {
			t.Fatalf("recent visit must release the remedial pin, got %v", slugReasonList(recs))
		}
	}
}

func TestRecommendReviewVerifiedOnly(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/browsed", PageType: "concept", Title: "看过"},
		{Slug: "concept/quizzed", PageType: "concept", Title: "练过"},
	}
	// Both decayed from the touched tier far below its demotion gate; the
	// only difference is whether the user ever engaged with verification.
	st := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-800 * 24 * time.Hour)},
	})
	recs := recommendNodes(recommendInput{
		Pages:  pages,
		States: map[string]FoldState{"concept/browsed": st, "concept/quizzed": st},
		// quzzed carries a wrong answer on record (direct evidence);
		// browsed was only ever… browsed.
		QuizStruggled: map[string]bool{"concept/quizzed": true},
	}, now, nil, 5)
	sawReview := map[string]bool{}
	for _, r := range recs {
		sawReview[r.Slug] = r.Reason == "review"
	}
	if sawReview["concept/browsed"] {
		t.Fatalf("pure-browse decay must leave recommendations (digest only), got %v", slugReasonList(recs))
	}
	if !sawReview["concept/quizzed"] {
		t.Fatalf("verified decay must stay in the review channel, got %v", slugReasonList(recs))
	}
}

func slugReasonList(recs []Recommendation) string {
	parts := make([]string, 0, len(recs))
	for _, r := range recs {
		parts = append(parts, r.Slug+"("+r.Reason+")")
	}
	return strings.Join(parts, " ")
}
