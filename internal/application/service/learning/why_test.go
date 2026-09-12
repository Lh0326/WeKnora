package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TestRecentAnchorsFiltersPassiveAndWindows: only strong behaviour anchors
// the mainline, per-slug newest wins, the continuity window bounds, and at
// most three anchors survive (newest first).
func TestRecentAnchorsFiltersPassiveAndWindows(t *testing.T) {
	now := time.Now()
	history := []types.LearningEvent{
		// Passive projections must never anchor.
		{Slug: "concept/noise-topic", Type: types.LearningEventTopicSignal, OccurredAt: now.Add(-time.Hour)},
		{Slug: "concept/noise-backfill", Type: types.LearningEventBackfillCite, OccurredAt: now.Add(-time.Hour)},
		{Slug: "concept/noise-reask", Type: types.LearningEventReAsk, OccurredAt: now.Add(-time.Hour)},
		{Slug: "concept/noise-unsure", Type: types.LearningEventQuizUnsure, OccurredAt: now.Add(-time.Hour)},
		// Strong behaviour, but outside the 72h window.
		{Slug: "concept/stale", Type: types.LearningEventQuizCorrect, OccurredAt: now.Add(-100 * time.Hour)},
		// Strong behaviour inside the window; per-slug newest wins.
		{Slug: "concept/main", Type: types.LearningEventWikiToolRead, OccurredAt: now.Add(-30 * time.Hour)},
		{Slug: "concept/main", Type: types.LearningEventQuizCorrect, OccurredAt: now.Add(-2 * time.Hour)},
		{Slug: "concept/second", Type: types.LearningEventAnswerCite, OccurredAt: now.Add(-10 * time.Hour)},
		{Slug: "concept/third", Type: types.LearningEventCrossRef, OccurredAt: now.Add(-20 * time.Hour)},
		{Slug: "concept/fourth", Type: types.LearningEventQuizWrong, OccurredAt: now.Add(-40 * time.Hour)}, // 4th → dropped
	}
	got := recentAnchors(history, now)
	if len(got) != 3 {
		t.Fatalf("anchors = %v, want 3", got)
	}
	wantOrder := []string{"concept/main", "concept/second", "concept/third"}
	for i, slug := range wantOrder {
		if got[i].Slug != slug {
			t.Fatalf("anchor[%d] = %s, want %s (full %v)", i, got[i].Slug, slug, got)
		}
	}
}

// TestRecommendContinuityChannel: the candidate that directly extends the
// just-learned node (prerequisite successor, or same-document same-chapter
// neighbour) must lead the list with the continue reason and the matching
// narrative key.
func TestRecommendContinuityChannel(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/just-learned", PageType: "concept", Title: "刚学"},
		{Slug: "concept/successor", PageType: "concept", Title: "后继"},
		{Slug: "concept/same-chapter", PageType: "concept", Title: "同章"},
		{Slug: "concept/other-doc-same-chapter", PageType: "concept", Title: "他书同章"},
		{Slug: "concept/unrelated", PageType: "concept", Title: "无关"},
	}
	edges := []types.LearningEdge{{
		FromSlug: "concept/just-learned", ToSlug: "concept/successor",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}}
	material := map[string]NodeMaterial{
		"concept/just-learned":           {Rank: 3, DocID: "dA", Section: "第2章"},
		"concept/same-chapter":           {Rank: 4, DocID: "dA", Section: "第2章"},
		"concept/other-doc-same-chapter": {Rank: 5, DocID: "dB", Section: "第2章"}, // same label, other doc
		"concept/successor":              {Rank: 6, DocID: "dA", Section: "第9章"}, // worst tiebreak, must still lead
	}
	recent := []RecentNode{{Slug: "concept/just-learned", At: now.Add(-time.Hour)}}
	// The edge just-learned→successor GATES successor until the anchor is
	// familiar — exactly the real flow this channel serves: the learner
	// just worked the anchor through.
	familiar := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-2 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-time.Hour)},
	})

	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, Material: material, Recent: recent,
		States: map[string]FoldState{"concept/just-learned": familiar},
		DirectFacts: map[string][]DirectQuizFact{
			"concept/just-learned": {{ItemID: "q1", FirstCorrectAt: now.Add(-2 * time.Hour)}},
		},
	}, now, nil, 5)

	// The successor and the same-doc same-chapter neighbour lead, ahead of
	// every unrelated peer; the other document's same-named chapter does
	// NOT qualify (document identity disambiguates the label).
	if recs[0].Slug != "concept/successor" || recs[0].Reason != "continue" ||
		recs[0].Why != "continues_prereq" || recs[0].WhyRef != "concept/just-learned" {
		t.Fatalf("successor must lead as continue/continues_prereq, got %+v", recs[0])
	}
	foundSameSection := false
	for _, r := range recs {
		if r.Slug == "concept/same-chapter" {
			foundSameSection = true
			if r.Why != "same_section" {
				t.Fatalf("same-doc same-chapter neighbour why = %q, want same_section", r.Why)
			}
		}
		if r.Slug == "concept/other-doc-same-chapter" && r.Reason == "continue" {
			t.Fatal("same chapter label from ANOTHER document must not earn the continuity bonus")
		}
	}
	if !foundSameSection {
		t.Fatalf("same-chapter neighbour missing from %v", slugList(recs))
	}
	// Freshness weighting: a 30h-old anchor halves the bonus — the
	// successor still qualifies and leads, just by a smaller margin.
	stale := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, Material: material,
		Recent: []RecentNode{{Slug: "concept/just-learned", At: now.Add(-30 * time.Hour)}},
		States: map[string]FoldState{"concept/just-learned": familiar},
		DirectFacts: map[string][]DirectQuizFact{
			"concept/just-learned": {{ItemID: "q1", FirstCorrectAt: now.Add(-30 * time.Hour)}},
		},
	}, now, nil, 5)
	if stale[0].Slug != "concept/successor" {
		t.Fatalf("stale anchor successor must still lead, got %v", slugList(stale))
	}
}

// TestRecommendChainAssembly: with no history anchors, the visible list
// itself chains — a prerequisite successor of the previously picked card
// steps forward over a higher-scored unrelated peer, and its narrative
// says which card it continues.
func TestRecommendChainAssembly(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/a", PageType: "concept", Title: "甲"},
		{Slug: "concept/b", PageType: "concept", Title: "乙"},
		{Slug: "concept/c", PageType: "concept", Title: "丙"},
		{Slug: "concept/d", PageType: "concept", Title: "丁"},
	}
	// b extends a (edge a→b) — the edge also GATES b, so the fixture gives
	// 'a' a familiar state (logit + one direct fact) exactly like a card
	// the learner just worked through; c and d are plain peers.
	edges := []types.LearningEdge{{
		FromSlug: "concept/a", ToSlug: "concept/b",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	}}
	material := map[string]NodeMaterial{
		"concept/a": {Rank: 0, DocID: "d", Section: "第1章"},
		"concept/b": {Rank: 1, DocID: "d", Section: "第1章"},
		"concept/c": {Rank: 2, DocID: "d", Section: "第2章"},
		"concept/d": {Rank: 3, DocID: "d", Section: "第2章"},
	}
	familiar := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-2 * time.Hour)},
		{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-time.Hour)},
	})
	// Disable the foundation window for this test: never-touched peers
	// would otherwise blind-spot-boost over the familiar anchor, and the
	// assertion is about chaining, not cold start.
	origWindow := FoundationWindow
	FoundationWindow = 0
	t.Cleanup(func() { FoundationWindow = origWindow })
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, Material: material,
		States: map[string]FoldState{"concept/a": familiar},
		DirectFacts: map[string][]DirectQuizFact{
			"concept/a": {{ItemID: "q1", FirstCorrectAt: now.Add(-2 * time.Hour)}},
		},
	}, now, nil, 4)
	if len(recs) != 4 {
		t.Fatalf("recs = %v", slugList(recs))
	}
	if recs[0].Slug != "concept/a" || recs[1].Slug != "concept/b" {
		t.Fatalf("chain must keep the successor adjacent to its prerequisite, got %v", slugList(recs))
	}
	if recs[1].Why != "continues_prev" || recs[1].WhyRef != "concept/a" {
		t.Fatalf("second card narrative = %q/%q, want continues_prev/concept/a", recs[1].Why, recs[1].WhyRef)
	}
}

// TestBuildWhyLadder: narrative priority — pass-through keys untouched,
// specialized channels exempt, then first_stop > material_start >
// chapter_of > in_doc, and nothing without material.
func TestBuildWhyLadder(t *testing.T) {
	mat := NodeMaterial{Rank: 3, DocID: "d", DocTitle: "指南", Section: "第2章"}

	// Pass-through: the pure recommender's history-based keys win.
	rec := interfaces.Recommendation{Why: "continues_prereq", WhyRef: "x", Reason: "continue", DocRank: 1}
	buildWhy(&rec, mat)
	if rec.Why != "continues_prereq" {
		t.Fatalf("existing why must pass through, got %q", rec.Why)
	}
	// Specialized channels stay exempt (their own explanations suffice).
	for _, reason := range []string{"self-verify", "review", "remedial", "struggling", "prerequisite-stuck", "bypass"} {
		rec = interfaces.Recommendation{Reason: reason, DocRank: 1}
		buildWhy(&rec, mat)
		if rec.Why != "" {
			t.Fatalf("reason %q must stay narrative-free, got %q", reason, rec.Why)
		}
	}
	// Rank 1 = the opening card.
	rec = interfaces.Recommendation{Reason: "foundation", DocRank: 1}
	buildWhy(&rec, mat)
	if rec.Why != "first_stop" {
		t.Fatalf("rank-1 why = %q, want first_stop", rec.Why)
	}
	// Foundation cards announce their material position.
	rec = interfaces.Recommendation{Reason: "foundation", DocRank: 2}
	buildWhy(&rec, mat)
	if rec.Why != "material_start" {
		t.Fatalf("foundation why = %q, want material_start", rec.Why)
	}
	// Generic frontier falls to the chapter label.
	rec = interfaces.Recommendation{Reason: "frontier", DocRank: 4}
	buildWhy(&rec, mat)
	if rec.Why != "chapter_of" || rec.WhyRef != "第2章" {
		t.Fatalf("frontier why = %q/%q, want chapter_of/第2章", rec.Why, rec.WhyRef)
	}
	// No section → the document speaks.
	rec = interfaces.Recommendation{Reason: "frontier", DocRank: 4}
	buildWhy(&rec, NodeMaterial{Rank: 4, DocID: "d", DocTitle: "指南"})
	if rec.Why != "in_doc" || rec.WhyRef != "指南" {
		t.Fatalf("doc why = %q/%q, want in_doc/指南", rec.Why, rec.WhyRef)
	}
	// No material at all → no line.
	rec = interfaces.Recommendation{Reason: "frontier"}
	buildWhy(&rec, NodeMaterial{})
	if rec.Why != "" {
		t.Fatalf("no material must mean no why, got %q", rec.Why)
	}
}
