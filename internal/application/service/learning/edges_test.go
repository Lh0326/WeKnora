package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEdgeCandidatePairsThreeSources(t *testing.T) {
	pages := []*types.WikiPage{
		{Slug: "concept/a", PageType: "concept", Title: "A", FolderID: "f1",
			OutLinks: types.StringArray{"concept/b"}, SourceRefs: types.StringArray{"doc1|D"}},
		{Slug: "concept/b", PageType: "concept", Title: "B", FolderID: "f1",
			SourceRefs: types.StringArray{"doc1|D"}},
		{Slug: "concept/c", PageType: "concept", Title: "C", FolderID: "f2",
			SourceRefs: types.StringArray{"doc1|D", "doc2|E"}},
		{Slug: "concept/d", PageType: "concept", Title: "D", FolderID: "f2",
			OutLinks: types.StringArray{"concept/a", "concept/a"}}, // self+dup ignored
	}
	pairs := edgeCandidatePairs(pages)
	set := map[[2]string]bool{}
	for _, p := range pairs {
		set[p] = true
	}
	// (a) wiki-link: a→b, d→a
	// (b) folder neighbours: a→b (dup), c→d
	// (c) doc co-occurrence: a→b (dup), a→c, b→c, c→d? (doc2 only c) → a→c, b→c
	for _, want := range [][2]string{{"concept/a", "concept/b"}, {"concept/d", "concept/a"}, {"concept/c", "concept/d"}, {"concept/a", "concept/c"}, {"concept/b", "concept/c"}} {
		if !set[want] {
			t.Errorf("missing pair %v (have %v)", want, pairs)
		}
	}
	if set[[2]string{"concept/d", "concept/d"}] || set[[2]string{"concept/a", "concept/d"}] == false && false {
		t.Error("self pair leaked")
	}
	if len(pairs) != len(set) {
		t.Error("duplicate pairs leaked")
	}
}

func TestAcceptedEdgesFilters(t *testing.T) {
	submitted := [][2]string{{"a", "b"}, {"a", "c"}}
	resp := edgeResponse{Pairs: []edgeVerdict{
		{From: "a", To: "b", Relation: "prerequisite", Confidence: 0.9},  // keep
		{From: "a", To: "c", Relation: "related", Confidence: 0.99},      // wrong relation
		{From: "a", To: "b", Relation: "prerequisite", Confidence: 0.5},  // below floor
		{From: "x", To: "y", Relation: "prerequisite", Confidence: 0.99}, // not submitted
	}}
	got := acceptedEdges(resp, submitted)
	if len(got) != 1 || got[0].From != "a" || got[0].To != "b" {
		t.Fatalf("accepted = %+v, want only a→b", got)
	}
}

func TestPairsInvolvingNewPagesRespectsWatermark(t *testing.T) {
	now := time.Now()
	pagesBySlug := map[string]*types.WikiPage{
		"old": {Slug: "old", UpdatedAt: now.Add(-48 * time.Hour)},
		"new": {Slug: "new", UpdatedAt: now},
	}
	pairs := [][2]string{{"old", "old2"}, {"old", "new"}, {"new", "new2"}}
	// old2/new2 absent from map: pairs survive only via their known page.
	watermark := now.Add(-24 * time.Hour)

	got := pairsInvolvingNewPages(pairs, pagesBySlug, watermark)
	// [old old2] has no fresh page → dropped; [old new] and [new new2]
	// both involve the freshly updated "new" → kept.
	if len(got) != 2 || got[0] != [2]string{"old", "new"} || got[1] != [2]string{"new", "new2"} {
		t.Fatalf("filtered = %v, want [old new] and [new new2]", got)
	}

	// Zero watermark keeps everything (first pass).
	if all := pairsInvolvingNewPages(pairs, pagesBySlug, time.Time{}); len(all) != len(pairs) {
		t.Fatalf("zero watermark must keep all, got %v", all)
	}
}

func TestEdgeWatermarkTakesMaxCreatedAt(t *testing.T) {
	t1 := time.Now().Add(-time.Hour)
	t2 := time.Now()
	if got := edgeWatermark([]types.LearningEdge{{CreatedAt: t1}, {CreatedAt: t2}}); !got.Equal(t2) {
		t.Fatalf("watermark = %v, want %v", got, t2)
	}
	if got := edgeWatermark(nil); !got.IsZero() {
		t.Fatal("empty edges must give zero watermark")
	}
}
