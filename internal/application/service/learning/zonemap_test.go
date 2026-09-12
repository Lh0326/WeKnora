package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TestZoneMapPartitionsRankingPerFolder: two folders (two module zones).
// The global ranking is partitioned per zone — each zone gets its own
// "1"/"2" from the SAME ordering, zones with a live next lead best-first,
// and edges (including a cross-zone one) ride along with dangling ones
// filtered.
func TestZoneMapPartitionsRankingPerFolder(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	// Zone A: two nodes, both unseen. Zone B: one unseen node plus one the
	// learner just touched. All in one KB; A gets a cross-zone edge into B
	// and a dangling edge whose target page does not exist.
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/a1", PageType: "concept", Title: "甲一", FolderID: "fa"})
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/a2", PageType: "concept", Title: "甲二", FolderID: "fa"})
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/b1", PageType: "concept", Title: "乙一", FolderID: "fb"})
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/b2", PageType: "concept", Title: "乙二", FolderID: "fb"})
	wiki.folders = map[string][]*types.WikiFolder{
		testKB: {{ID: "fa", Name: "模块甲"}, {ID: "fb", Name: "模块乙"}},
	}
	_ = repo.UpsertEdge(ctx, &types.LearningEdge{
		TenantID: 1, KnowledgeBaseID: testKB,
		FromSlug: "concept/a1", ToSlug: "concept/b1",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	})
	_ = repo.UpsertEdge(ctx, &types.LearningEdge{
		TenantID: 1, KnowledgeBaseID: testKB,
		FromSlug: "concept/a1", ToSlug: "concept/ghost",
		Relation: types.LearningEdgePrerequisite, Confidence: 0.9,
	})
	// b2 carries fresh evidence: the continuity/consolidate channels push
	// zone B's next above zone A's untouched pair.
	seedFold(t, svc, repo, ctx, "concept/b2",
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-time.Hour)})

	zm, err := svc.ZoneMap(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	// Three zones: the fixture's three root-folder pages plus fa and fb.
	if len(zm.Zones) != 3 {
		t.Fatalf("zones = %+v, want 3 (root, fa, fb)", zm.Zones)
	}
	byID := map[string]interfaces.ZoneSummary{}
	for _, z := range zm.Zones {
		byID[z.FolderID] = z
	}
	// Zone A: both nodes unseen → next is the doc-order/title-first one,
	// second the other; totals 2/2, lit 0/2.
	za := byID["fa"]
	if za.Total != 2 || za.Lit != 0 || za.Next == nil || za.Second == nil {
		t.Fatalf("zone fa = %+v", za)
	}
	if za.FolderName != "模块甲" {
		t.Fatalf("zone fa name = %q", za.FolderName)
	}
	// Zone B: b2 (fresh, consolidate) must be its "1". b1 sits behind the
	// a1→b1 prerequisite edge with a1 unseen, so it never pools and zone
	// fb legitimately has NO second — the gate, not the zone, decides.
	zb := byID["fb"]
	if zb.Next == nil || zb.Next.Slug != "concept/b2" || zb.Next.Level != "touched" {
		t.Fatalf("zone fb next = %+v, want concept/b2 at touched", zb.Next)
	}
	if zb.Second != nil {
		t.Fatalf("gated b1 must not appear as zone fb second: %+v", zb.Second)
	}
	// Untouched zones lead studied ones (the established ordering), so fa
	// precedes fb in the zone list (root's ASCII-titled nodes may lead both).
	posOf := map[string]int{}
	for i, z := range zm.Zones {
		posOf[z.FolderID] = i
	}
	if posOf["fa"] > posOf["fb"] {
		t.Fatalf("zone order has fa(%d) after fb(%d)", posOf["fa"], posOf["fb"])
	}
	// Nodes: 2+2+3 fixture nodes = the map covers every page with zone id.
	nodeZones := map[string]string{}
	for _, n := range zm.Nodes {
		nodeZones[n.Slug] = n.FolderID
	}
	if nodeZones["concept/a1"] != "fa" || nodeZones["concept/b2"] != "fb" {
		t.Fatalf("node zones wrong: %v", nodeZones)
	}
	for _, n := range zm.Nodes {
		if n.Slug == "concept/b2" && !n.Recent {
			t.Fatal("b2's fresh evidence must flag recent (48h twinkle)")
		}
	}
	// Edges: the cross-zone edge ships, the dangling one is filtered.
	if len(zm.Edges) != 1 || zm.Edges[0].From != "concept/a1" || zm.Edges[0].To != "concept/b1" {
		t.Fatalf("edges = %+v, want only a1→b1", zm.Edges)
	}
}

// TestZoneMapSkippedNodesNeverBecomeNext: a user-retired node leaves its
// zone's ranking slots to the live peers.
func TestZoneMapSkippedNodesNeverBecomeNext(t *testing.T) {
	svc, _, wiki := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/s1", PageType: "concept", Title: "跳过", FolderID: "f"})
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/s2", PageType: "concept", Title: "留下", FolderID: "f"})
	if err := svc.RecordSkip(ctx, testKB, "concept/s1", true); err != nil {
		t.Fatal(err)
	}
	zm, err := svc.ZoneMap(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	for _, z := range zm.Zones {
		if z.FolderID != "f" {
			continue
		}
		if z.Next == nil || z.Next.Slug == "concept/s1" {
			t.Fatalf("skipped node must not be a zone next: %+v", z.Next)
		}
		if z.Second != nil && z.Second.Slug == "concept/s1" {
			t.Fatalf("skipped node must not be a zone second: %+v", z.Second)
		}
	}
	for _, n := range zm.Nodes {
		if n.Slug == "concept/s1" && !n.Skipped {
			t.Fatal("s1 must carry the skipped flag in the node payload")
		}
	}
}
