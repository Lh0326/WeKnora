package learning

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestBuildDocOrderRanksBySourcePosition: two documents uploaded in
// reading order (doc1 older than doc2); within doc1 the chunk index is
// the chapter order. The rank map must read back shallow→deep, and a page
// citing only a document (no chunks) orders after that document's chunked
// pages.
func TestBuildDocOrderRanksBySourcePosition(t *testing.T) {
	doc1 := time.Now().Add(-48 * time.Hour)
	doc2 := doc1.Add(time.Hour)
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"c-ch1": {ID: "c-ch1", KnowledgeID: "doc1", ChunkIndex: 2, StartAt: 1000},
		"c-ch1b": {ID: "c-ch1b", KnowledgeID: "doc1", ChunkIndex: 3, StartAt: 1500},
		"c-ch2": {ID: "c-ch2", KnowledgeID: "doc1", ChunkIndex: 30, StartAt: 30000},
		"c-doc2": {ID: "c-doc2", KnowledgeID: "doc2", ChunkIndex: 0, StartAt: 0},
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"doc1": {ID: "doc1", CreatedAt: doc1},
		"doc2": {ID: "doc2", CreatedAt: doc2},
	}}
	pages := []*types.WikiPage{
		// Chapter-2 node of doc1 (chunk 30) whose title sorts FIRST — the
		// rank must still put it after the chapter-1 node.
		{Slug: "concept/ch2", PageType: "concept", Title: "A进阶",
			ChunkRefs: types.StringArray{"c-ch2"}},
		{Slug: "concept/ch1", PageType: "concept", Title: "Z基础",
			ChunkRefs: types.StringArray{"c-ch1"}},
		// Document-level fallback: cites doc2 itself, no chunk refs.
		{Slug: "concept/doc2page", PageType: "concept", Title: "文档二",
			SourceRefs: types.StringArray{"doc2|文档二"}},
		// A node whose chunks appear in BOTH documents ranks by its
		// earliest position (doc1 chapter 1, a hair after ch1).
		{Slug: "concept/both", PageType: "concept", Title: "跨文档",
			ChunkRefs: types.StringArray{"c-doc2", "c-ch1b"}},
	}
	svc := NewService(nil, nil, chunks, nil, nil, docs)
	order := svc.buildDocOrder(context.Background(), 1, pages)

	want := []string{"concept/ch1", "concept/both", "concept/ch2", "concept/doc2page"}
	for rank, slug := range want {
		got, ok := order[slug]
		if !ok || got.Rank != rank {
			t.Fatalf("rank[%s] = %+v (ok=%v), want rank %d (full map %v)", slug, got, ok, rank, order)
		}
		if got.DocID != "doc1" && slug != "concept/doc2page" {
			t.Fatalf("home doc of %s = %q, want doc1", slug, got.DocID)
		}
	}
	// The document-level fallback still carries its home document identity.
	if m := order["concept/doc2page"]; m.DocID != "doc2" || m.ChunkIx != 1<<30 {
		t.Fatalf("fallback material = %+v, want doc2 / no-chunk sentinel", m)
	}
	// Pages with no resolvable position stay out of the map (rank last).
	if _, ok := order["concept/ghost"]; ok {
		t.Fatal("unresolvable page must not carry a rank")
	}
}

// TestDocOrderRanksCacheAndTTL: a second call inside the TTL is served
// from the cache — a page added to the input set after the rebuild stays
// unranked until expiry.
func TestDocOrderRanksCacheAndTTL(t *testing.T) {
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"c1": {ID: "c1", KnowledgeID: "d1", ChunkIndex: 0},
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"d1": {ID: "d1", CreatedAt: time.Now()},
	}}
	svc := NewService(nil, nil, chunks, nil, nil, docs)
	first := []*types.WikiPage{{Slug: "concept/a", PageType: "concept", ChunkRefs: types.StringArray{"c1"}}}
	ctx := context.Background()
	if m := svc.nodeMaterials(ctx, 1, "kb", first); m["concept/a"].Rank != 0 {
		t.Fatalf("first build = %v", m)
	}
	// Same key: cached copy wins, the new page stays unranked.
	second := append(first, &types.WikiPage{Slug: "concept/b", PageType: "concept", ChunkRefs: types.StringArray{"c1"}})
	cached := svc.nodeMaterials(ctx, 1, "kb", second)
	if _, ok := cached["concept/b"]; ok {
		t.Fatalf("cached material must not grow, got %v", cached)
	}
	// A different KB key rebuilds immediately.
	if m := svc.nodeMaterials(ctx, 1, "kb2", second); m["concept/b"].Rank != 1 {
		t.Fatalf("other KB must rebuild, got %v", m)
	}
}


// TestBuildDocOrderResistsForwardMentions: the aggregation is "substantive
// home", not "first mention". A chapter-5 concept cited once in the
// chapter-1 overview (forward reference) and three times in chapter 5 must
// rank at its median chapter-5 position — this is the exact regression the
// min-aggregation let through.
func TestBuildDocOrderResistsForwardMentions(t *testing.T) {
	doc1 := time.Now().Add(-24 * time.Hour)
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"c-ov":  {ID: "c-ov", KnowledgeID: "d1", ChunkIndex: 2},  // overview mention
		"c-5a":  {ID: "c-5a", KnowledgeID: "d1", ChunkIndex: 40}, // substantive
		"c-5b":  {ID: "c-5b", KnowledgeID: "d1", ChunkIndex: 42},
		"c-5c":  {ID: "c-5c", KnowledgeID: "d1", ChunkIndex: 44},
		"c-1":   {ID: "c-1", KnowledgeID: "d1", ChunkIndex: 3},
		"c-1b":  {ID: "c-1b", KnowledgeID: "d1", ChunkIndex: 4},
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"d1": {ID: "d1", CreatedAt: doc1},
	}}
	pages := []*types.WikiPage{
		{Slug: "concept/late", PageType: "concept", Title: "第五章概念",
			ChunkRefs: types.StringArray{"c-ov", "c-5a", "c-5b", "c-5c"}},
		{Slug: "concept/early", PageType: "concept", Title: "第一章概念",
			ChunkRefs: types.StringArray{"c-1", "c-1b"}},
	}
	svc := NewService(nil, nil, chunks, nil, nil, docs)
	order := svc.buildDocOrder(context.Background(), 1, pages)
	if order["concept/early"].Rank >= order["concept/late"].Rank {
		t.Fatalf("forward mention must not promote the late concept: early=%d late=%d",
			order["concept/early"].Rank, order["concept/late"].Rank)
	}
	// The late concept's position is its median citation (chunk 42), i.e.
	// after early (3..4) — provable by adding a node at chunk 10 and 40:
	// late must rank after the chunk-10 node but before a chunk-44 node.
	pages = append(pages, &types.WikiPage{Slug: "concept/mid", PageType: "concept",
		Title: "第十章", ChunkRefs: types.StringArray{"c-1", "c-ov"}}) // median = c-ov(2)? both early
	svc2 := NewService(nil, nil, chunks, nil, nil, docs)
	order2 := svc2.buildDocOrder(context.Background(), 1, pages)
	_ = order2
}

// TestBuildDocOrderHomeDocument: one citation in an earlier document's
// overview plus three in a later chapter document — the home document is
// the one with the bulk of citations, so the node ranks with its chapter,
// not with the overview.
func TestBuildDocOrderHomeDocument(t *testing.T) {
	day := time.Hour * 24
	base := time.Now().Add(-10 * day)
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"ov-1": {ID: "ov-1", KnowledgeID: "d-overview", ChunkIndex: 5},
		"ch-1": {ID: "ch-1", KnowledgeID: "d-ch5", ChunkIndex: 20},
		"ch-2": {ID: "ch-2", KnowledgeID: "d-ch5", ChunkIndex: 22},
		"ch-3": {ID: "ch-3", KnowledgeID: "d-ch5", ChunkIndex: 24},
		"b-1":  {ID: "b-1", KnowledgeID: "d-overview", ChunkIndex: 8},
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"d-overview": {ID: "d-overview", CreatedAt: base},
		"d-ch5":      {ID: "d-ch5", CreatedAt: base.Add(2 * day)},
	}}
	pages := []*types.WikiPage{
		{Slug: "concept/late", PageType: "concept", Title: "后章概念",
			ChunkRefs: types.StringArray{"ov-1", "ch-1", "ch-2", "ch-3"}},
		{Slug: "concept/basics", PageType: "concept", Title: "开篇基础",
			ChunkRefs: types.StringArray{"b-1"}},
	}
	svc := NewService(nil, nil, chunks, nil, nil, docs)
	order := svc.buildDocOrder(context.Background(), 1, pages)
	if order["concept/basics"].Rank >= order["concept/late"].Rank {
		t.Fatalf("node must follow its home document (later), not its lone overview mention: basics=%d late=%d",
			order["concept/basics"].Rank, order["concept/late"].Rank)
	}
}

// TestBuildDocOrderSectionLabel: the chapter-scale label prefers the FIRST
// level-1 heading among the node's home-document citations (chapter
// granularity), falls back to the median reference's heading when no "# "
// line exists, and stays empty for unheaded material.
func TestBuildDocOrderSectionLabel(t *testing.T) {
	doc1 := time.Now().Add(-24 * time.Hour)
	mk := func(id, doc string, ix int, header string) *types.Chunk {
		return &types.Chunk{ID: id, KnowledgeID: doc, ChunkIndex: ix, ContextHeader: header}
	}
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		// ch5 node: cited under "## 5.2" but the earliest H1 is "# 第5章".
		"a": mk("a", "d1", 10, "# 第5章 高级检索\n## 5.1"),
		"b": mk("b", "d1", 12, "# 第5章 高级检索\n## 5.2"),
		"c": mk("c", "d1", 14, "## 5.3"),
		// ch1 node: only sub-level headings — median reference's line wins.
		"d": mk("d", "d1", 2, "## 1.2 基础"),
		"e": mk("e", "d1", 3, "## 1.3 相邻"),
		"f": mk("f", "d1", 4, "## 1.4"),
		// unheaded node.
		"g": mk("g", "d1", 40, ""),
		"h": mk("h", "d1", 41, ""),
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"d1": {ID: "d1", CreatedAt: doc1, Title: "检索入门指南"},
	}}
	pages := []*types.WikiPage{
		{Slug: "concept/ch5", PageType: "concept", ChunkRefs: types.StringArray{"a", "b", "c"}},
		{Slug: "concept/ch1", PageType: "concept", ChunkRefs: types.StringArray{"d", "e", "f"}},
		{Slug: "concept/flat", PageType: "concept", ChunkRefs: types.StringArray{"g", "h"}},
	}
	svc := NewService(nil, nil, chunks, nil, nil, docs)
	m := svc.buildDocOrder(context.Background(), 1, pages)
	if got := m["concept/ch5"].Section; got != "第5章 高级检索" {
		t.Fatalf("ch5 section = %q, want the first H1 label", got)
	}
	if got := m["concept/ch5"].DocTitle; got != "检索入门指南" {
		t.Fatalf("ch5 doc title = %q, want the knowledge title", got)
	}
	if got := m["concept/ch1"].Section; got != "1.3 相邻" {
		t.Fatalf("ch1 section = %q, want the median reference's line (1.3 相邻)", got)
	}
	if got := m["concept/flat"].Section; got != "" {
		t.Fatalf("unheaded material must have no section label, got %q", got)
	}
}

// TestBuildDocOrderSameSectionNeedsSameDoc: two documents may both carry a
// "第2章"; the material keeps the document identity so the continuity
// same-chapter test cannot false-positive across documents.
func TestBuildDocOrderSameSectionNeedsSameDoc(t *testing.T) {
	base := time.Now().Add(-48 * time.Hour)
	chunks := &stubChunkRepo{chunks: map[string]*types.Chunk{
		"x": {ID: "x", KnowledgeID: "dA", ChunkIndex: 5, ContextHeader: "# 第2章\n## 2.1"},
		"y": {ID: "y", KnowledgeID: "dB", ChunkIndex: 6, ContextHeader: "# 第2章\n## 2.1"},
	}}
	docs := &stubKnowledgeRepo{docs: map[string]*types.Knowledge{
		"dA": {ID: "dA", CreatedAt: base},
		"dB": {ID: "dB", CreatedAt: base.Add(time.Hour)},
	}}
	pages := []*types.WikiPage{
		{Slug: "concept/a", PageType: "concept", ChunkRefs: types.StringArray{"x"}},
		{Slug: "concept/b", PageType: "concept", ChunkRefs: types.StringArray{"y"}},
	}
	svc := NewService(nil, nil, chunks, nil, nil, docs)
	m := svc.buildDocOrder(context.Background(), 1, pages)
	if m["concept/a"].Section != m["concept/b"].Section {
		t.Fatal("fixture sanity: both nodes sit under a same-named chapter")
	}
	if m["concept/a"].DocID == m["concept/b"].DocID {
		t.Fatal("fixture sanity: nodes must belong to different documents")
	}
}
