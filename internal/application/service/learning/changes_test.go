package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Fold three citations into a state whose anchor tier is mastered, then
// let it idle far past its stability — forgetting eats the earned tier.
func demotableState(now time.Time, idle time.Duration) FoldState {
	base := now.Add(-idle)
	return FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base.Add(time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: base.Add(2 * time.Hour)},
	})
}

// masteredFacts unlocks the top tier for a node whose evidence started
// `idle` ago: two distinct items answered across the session gap. The
// passive channel's fixtures need this because the anchor tier is
// direct-gate capped — indirect citations alone can no longer "earn"
// familiar/mastered, by design.
func masteredFacts(now time.Time, idle time.Duration) []DirectQuizFact {
	base := now.Add(-idle)
	return []DirectQuizFact{
		{ItemID: "quiz-a", FirstCorrectAt: base},
		{ItemID: "quiz-b", FirstCorrectAt: base.Add(MasteredSessionGap + time.Hour)},
	}
}

// familiarFacts unlocks the familiar tier (one distinct correct item).
func familiarFacts(now time.Time, idle time.Duration) []DirectQuizFact {
	return []DirectQuizFact{{ItemID: "quiz-a", FirstCorrectAt: now.Add(-idle)}}
}

// A node holding its tier today but decaying toward the gate is due-soon,
// not demoted; nothing is due when the horizon is short. With the shipped
// constants forgetting is deliberately slow (weeks), so the classification
// test sweeps the horizon var instead of hand-tuning a knife-edge state —
// exactly what the sweepable-consts design is for.
func holdingState(now time.Time) FoldState {
	return FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-24 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-23 * time.Hour)},
		{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: now.Add(-22 * time.Hour)},
	})
}

func TestDerivePassiveChangesDemoted(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/gone", PageType: "concept", Title: "遗忘节点"},
		{Slug: "concept/fresh", PageType: "concept", Title: "新鲜节点"},
		{Slug: "concept/untouched", PageType: "concept", Title: "未接触节点"},
	}
	states := map[string]FoldState{
		"concept/gone":      demotableState(now, 200*24*time.Hour),
		"concept/fresh":     demotableState(now, time.Hour),
		"concept/untouched": {},
	}
	summary := derivePassiveChanges(pages, states, map[string][]DirectQuizFact{
		"concept/gone":  masteredFacts(now, 200*24*time.Hour),
		"concept/fresh": masteredFacts(now, time.Hour),
	}, nil, now, 20)

	if summary.DemotedCount != 1 || summary.DueSoonCount != 0 {
		t.Fatalf("counts = %d/%d, want 1/0", summary.DemotedCount, summary.DueSoonCount)
	}
	if len(summary.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(summary.Items))
	}
	it := summary.Items[0]
	if it.Slug != "concept/gone" || it.Title != "遗忘节点" {
		t.Fatalf("item = %+v, want concept/gone", it)
	}
	if !it.Demoted || it.AnchorLevel != "mastered" || it.ViewLevel != "familiar" {
		t.Fatalf("tiers = %s→%s demoted=%v, want mastered→familiar demoted", it.AnchorLevel, it.ViewLevel, it.Demoted)
	}
	if it.NextReviewDays != nil {
		t.Fatalf("demoted node must carry no next_review_days, got %v", *it.NextReviewDays)
	}
	if it.BaseP <= it.PEff {
		t.Fatalf("base_p %.3f must exceed decayed p_eff %.3f", it.BaseP, it.PEff)
	}
	if it.DaysIdle < 199 {
		t.Fatalf("days_idle = %.1f, want ≈200", it.DaysIdle)
	}
}

func TestDerivePassiveChangesDueSoon(t *testing.T) {
	orig := PassiveDueSoonDays
	// The holding node demotes in ~100+ days with shipped constants; a
	// 400-day horizon makes it due-soon without touching the state.
	PassiveDueSoonDays = 400
	t.Cleanup(func() { PassiveDueSoonDays = orig })

	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/holding", PageType: "concept", Title: "持有节点"},
	}
	summary := derivePassiveChanges(pages, map[string]FoldState{
		"concept/holding": holdingState(now),
	}, map[string][]DirectQuizFact{"concept/holding": familiarFacts(now, 24*time.Hour)}, nil, now, 20)
	if summary.DemotedCount != 0 || summary.DueSoonCount != 1 {
		t.Fatalf("counts = %d/%d, want 0/1", summary.DemotedCount, summary.DueSoonCount)
	}
	it := summary.Items[0]
	if it.Demoted || it.AnchorLevel != it.ViewLevel {
		t.Fatalf("holding node must not be demoted: %+v", it)
	}
	if it.NextReviewDays == nil || *it.NextReviewDays <= 0 {
		t.Fatalf("due-soon node needs a positive next_review_days, got %v", it.NextReviewDays)
	}

	// Short horizon: the same slow decay is not due — nothing surfaces.
	PassiveDueSoonDays = 7
	summary = derivePassiveChanges(pages, map[string]FoldState{
		"concept/holding": holdingState(now),
	}, nil, nil, now, 20)
	if summary.DemotedCount != 0 || summary.DueSoonCount != 0 || len(summary.Items) != 0 {
		t.Fatalf("short horizon must surface nothing, got %+v", summary)
	}
}

func TestDerivePassiveChangesOrderAndLimit(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/b", PageType: "concept", Title: "乙"},
		{Slug: "concept/a", PageType: "concept", Title: "甲"},
		{Slug: "concept/c", PageType: "concept", Title: "丙"},
	}
	// Three demoted nodes, low-confidence variant for 甲 (2 events only):
	// deeper drop ranks first, slug breaks ties, limit trims items but
	// never the counts.
	deep := demotableState(now, 400*24*time.Hour)
	shallow := demotableState(now, 200*24*time.Hour)
	states := map[string]FoldState{
		"concept/a": FoldAll(FoldState{}, []Event{
			{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-200 * 24 * time.Hour)},
			{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-199 * 24 * time.Hour)},
		}),
		"concept/b": shallow,
		"concept/c": deep,
	}
	summary := derivePassiveChanges(pages, states, map[string][]DirectQuizFact{
		"concept/a": masteredFacts(now, 200*24*time.Hour),
		"concept/b": masteredFacts(now, 200*24*time.Hour),
		"concept/c": masteredFacts(now, 400*24*time.Hour),
	}, nil, now, 2)

	if summary.DemotedCount != 3 {
		t.Fatalf("demoted_count = %d, want 3 (counts ignore the limit)", summary.DemotedCount)
	}
	if len(summary.Items) != 2 {
		t.Fatalf("items = %d, want 2 (limit)", len(summary.Items))
	}
	if summary.Items[0].Slug != "concept/c" {
		t.Fatalf("deepest drop must lead, got %s", summary.Items[0].Slug)
	}
	// Same-depth pair orders by slug: 甲(a) before 乙(b).
	if summary.Items[1].Slug != "concept/a" && summary.Items[1].Slug != "concept/b" {
		t.Fatalf("second item = %s, want a or b", summary.Items[1].Slug)
	}
	if summary.Items[1].Slug == "concept/a" && !summary.Items[1].LowConfidence {
		t.Fatal("2-evidence node must carry low_confidence")
	}
}
