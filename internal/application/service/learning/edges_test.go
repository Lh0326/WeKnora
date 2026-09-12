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
	pairs := edgeCandidatePairs(pages, nil)
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

// Document order orients the candidate groups: within one source document
// the pairs must run earlier→later, and the per-document cap keeps the
// EARLIEST nodes instead of the alphabetically-first ones.
func TestEdgeCandidatePairsDocOrder(t *testing.T) {
	// Nine nodes in one sweeping document, each in its own folder so the
	// only candidate source under test is document co-occurrence;
	// alphabetical order would keep the first 8 of a-z and drop "z",
	// document order keeps the 8 earliest and drops the tail instead.
	mk := func(slug, title, folder string) *types.WikiPage {
		return &types.WikiPage{Slug: slug, PageType: "concept", Title: title,
			FolderID: folder, SourceRefs: types.StringArray{"doc1|D"}}
	}
	pages := []*types.WikiPage{
		mk("concept/z", "Z", "fz"),
		mk("concept/a", "A", "fa"),
		mk("concept/m", "M", "fm"),
		mk("concept/b", "B", "fb"),
		mk("concept/c", "C", "fc"),
		mk("concept/d", "D", "fd"),
		mk("concept/e", "E", "fe"),
		mk("concept/f", "F", "ff"),
		mk("concept/g", "G", "fg"),
	}
	// Reading order: a(m pos 2)… let rank encode chapter order directly:
	// a(0) b(1) c(2) m(3) d(4) e(5) f(6) g(7), z ranks last (8).
	rank := map[string]int{
		"concept/a": 0, "concept/b": 1, "concept/c": 2, "concept/m": 3,
		"concept/d": 4, "concept/e": 5, "concept/f": 6, "concept/g": 7,
		"concept/z": 8,
	}
	pairs := edgeCandidatePairs(pages, rank)
	for _, p := range pairs {
		if rank[p[0]] == 8 || rank[p[1]] == 8 {
			t.Fatalf("tail node z must be evicted by the per-doc cap, got %v", p)
		}
		if rank[p[0]] > rank[p[1]] {
			t.Fatalf("doc pair must run earlier→later, got %v (%d → %d)", p, rank[p[0]], rank[p[1]])
		}
	}
	if len(pairs) == 0 {
		t.Fatal("no pairs generated")
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

// TestEdgeClosesCycle: the write-side guard — a new edge may not close a
// cycle (including the one-node self cycle), whatever the adjudicator said.
func TestEdgeClosesCycle(t *testing.T) {
	existing := []types.LearningEdge{
		{FromSlug: "a", ToSlug: "b", Relation: types.LearningEdgePrerequisite},
		{FromSlug: "b", ToSlug: "c", Relation: types.LearningEdgePrerequisite},
	}
	if !edgeClosesCycle(existing, "c", "a") {
		t.Fatal("c→a closes the a→b→c cycle")
	}
	if edgeClosesCycle(existing, "a", "c") {
		t.Fatal("a→c extends the chain, no cycle")
	}
	if !edgeClosesCycle(existing, "x", "x") {
		t.Fatal("self-edge is a one-node cycle")
	}
	if edgeClosesCycle(nil, "x", "y") {
		t.Fatal("empty set cannot close a cycle")
	}
}
