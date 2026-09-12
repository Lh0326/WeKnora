package learning

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// The document-order channel (从浅入深). Wiki pages carry no chapter field —
// a document is parsed into chunks and the LLM distills entity/concept pages
// from the whole text, so "chapter 1 before chapter 2" survives only as WHERE
// in the source material each node's evidence first appears. This module
// reconstructs that position: a node's document position is the earliest
// (document creation time, chunk index) among all the chunks its page cites,
// with the page's SourceRefs as a document-level fallback. The recommender
// turns that into a foundation-first ordering for cold starts, a direction
// prior for prerequisite-edge candidates, and — via NodeMaterial — the
// chapter/document context the recommendation narratives speak in.
const (
	// docOrderTTL matches the page-index cache: the ranks are derived from
	// the same wiki tables, so the two caches go stale together.
	docOrderTTL = 5 * time.Minute
	// docOrderMaxChunkLookups bounds the chunk-metadata fetch per KB rebuild.
	// The MINIMUM position is what the rank needs, and the citation pass
	// records refs in reading order, so the earliest-looking slice per page
	// suffices in practice; the cap keeps a pathological wiki from turning
	// the rebuild into a table scan.
	docOrderMaxChunkLookups = 4000
	// docOrderChunkBatchSize keeps the IN-list per query sane.
	docOrderChunkBatchSize = 500
	// docNoChunkIndex orders pages with document-level evidence only (no
	// chunk citations) after every chunked page of the same document.
	docNoChunkIndex = 1 << 30
)

// NodeMaterial is one node's resolved place in the source material: its
// rank (0 = earliest), its home document (id + title), the chapter-scale
// section label, and the median chunk index inside that document. The
// narrative channel composes these into the "承上启下" reasons; the
// recommender consumes Rank for ordering and Section/DocID for the
// continuity bonus.
type NodeMaterial struct {
	Rank     int
	DocID    string
	DocTitle string
	Section  string
	ChunkIx  int
}

// docOrderCache caches slug → NodeMaterial per (tenant, KB). Pattern follows
// pageIndexCache: sync.Map of key → {value, expiry}; a stale rebuild is
// idempotent.
type docOrderCache struct {
	m sync.Map // "tenant|kb" → docOrderEntry
}

type docOrderEntry struct {
	material  map[string]NodeMaterial
	expiresAt time.Time
}

// docPos is one node's comparable position in the source material. The ref
// field rides along for section-label extraction and is deliberately not
// part of the before() ordering.
type docPos struct {
	docAt   time.Time
	chunkIx int
	startAt int
	slug    string
	ref     string
}

func (p docPos) before(o docPos) bool {
	if !p.docAt.Equal(o.docAt) {
		return p.docAt.Before(o.docAt)
	}
	if p.chunkIx != o.chunkIx {
		return p.chunkIx < o.chunkIx
	}
	if p.startAt != o.startAt {
		return p.startAt < o.startAt
	}
	return p.slug < o.slug
}

// nodeMaterials returns the cached slug → material map. Rebuilds from the
// caller's page set on expiry; pages missing from the cached map simply
// read as "no material" (rank last, no narrative), which callers handle.
func (s *Service) nodeMaterials(ctx context.Context, tenantID uint64, kbID string, pages []*types.WikiPage) map[string]NodeMaterial {
	key := fmt.Sprintf("%d|%s", tenantID, kbID)
	now := time.Now()
	if v, ok := s.docOrder.m.Load(key); ok {
		entry := v.(docOrderEntry)
		if now.Before(entry.expiresAt) {
			return entry.material
		}
		s.docOrder.m.Delete(key)
	}
	material := s.buildDocOrder(ctx, tenantID, pages)
	s.docOrder.m.Store(key, docOrderEntry{material: material, expiresAt: now.Add(docOrderTTL)})
	return material
}

// docRankMap derives the rank-only view the pure recommender consumes.
func docRankMap(material map[string]NodeMaterial) map[string]int {
	ranks := make(map[string]int, len(material))
	for slug, m := range material {
		ranks[slug] = m.Rank
	}
	return ranks
}

// buildDocOrder derives the material map from scratch. Any failure degrades
// to an empty map (callers fall back to the title ordering), never to an
// error: document order is a ranking signal, not a correctness invariant. A
// service wired without chunk repo (read-path tests) simply produces no
// ranks. Only entity/concept pages are ranked: the two callers pass
// different page sets (nodes-only vs ListAll) but must share one cache, and
// the rank channel is node-scoped by definition.
func (s *Service) buildDocOrder(ctx context.Context, tenantID uint64, pages []*types.WikiPage) map[string]NodeMaterial {
	// A service wired without chunk repo (read-path tests) simply produces
	// no ranks — document order is a ranking signal, never a hard dep.
	if s.chunkRepo == nil {
		return map[string]NodeMaterial{}
	}
	// Only entity/concept pages are ranked: the two callers pass different
	// page sets (nodes-only vs ListAll) but must share one cache, and the
	// rank channel is node-scoped by definition.
	nodes := make([]*types.WikiPage, 0, len(pages))
	for _, p := range pages {
		if p != nil && p.Slug != "" && (p.PageType == "entity" || p.PageType == "concept") {
			nodes = append(nodes, p)
		}
	}
	// Slug-sort before the cap slices: the two callers pass differently
	// ordered page sets, and the cap must cut the same deterministic prefix
	// regardless of which caller rebuilt the shared cache.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Slug < nodes[j].Slug })
	pages = nodes
	// Collect the chunk ids to resolve, capped per KB rebuild.
	chunkIDs := make([]string, 0, len(pages)*4)
	perPage := make(map[string][]string, len(pages))
	for _, p := range pages {
		if p == nil || p.Slug == "" || len(p.ChunkRefs) == 0 {
			continue
		}
		refs := p.ChunkRefs
		if room := docOrderMaxChunkLookups - len(chunkIDs); room <= 0 {
			break
		} else if len(refs) > room {
			refs = refs[:room]
		}
		perPage[p.Slug] = refs
		chunkIDs = append(chunkIDs, refs...)
	}

	// Resolve chunk → (document id, chunk index, start offset, heading
	// breadcrumb). ContextHeader rides on the same rows for free.
	chunkDoc := make(map[string]string, len(chunkIDs))
	chunkIx := make(map[string]int, len(chunkIDs))
	chunkStart := make(map[string]int, len(chunkIDs))
	chunkHeader := make(map[string]string, len(chunkIDs))
	docIDs := make(map[string]bool)
	for i := 0; i < len(chunkIDs); i += docOrderChunkBatchSize {
		end := i + docOrderChunkBatchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		chunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs[i:end])
		if err != nil {
			logger.Warnf(ctx, "learning: doc-order chunk read failed: %v", err)
			continue // partial positions beat none
		}
		for _, ch := range chunks {
			if ch == nil || ch.ID == "" {
				continue
			}
			chunkDoc[ch.ID] = ch.KnowledgeID
			chunkIx[ch.ID] = ch.ChunkIndex
			chunkStart[ch.ID] = ch.StartAt
			chunkHeader[ch.ID] = ch.ContextHeader
			if ch.KnowledgeID != "" {
				docIDs[ch.KnowledgeID] = true
			}
		}
	}

	// Resolve document creation times (documents uploaded in reading order
	// — chapter 1 before chapter 2 — were created in that order, unlike
	// chunk rows whose timestamps follow async parse completion) and titles
	// (the narrative's document reference). A nil knowledge repo
	// (maintenance tests) degrades to chunk-index-only ordering.
	docAt := make(map[string]time.Time, len(docIDs))
	docTitle := make(map[string]string, len(docIDs))
	if s.knowledgeRepo != nil {
		ids := make([]string, 0, len(docIDs))
		for id := range docIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for i := 0; i < len(ids); i += docOrderChunkBatchSize {
			end := i + docOrderChunkBatchSize
			if end > len(ids) {
				end = len(ids)
			}
			docs, err := s.knowledgeRepo.GetKnowledgeBatch(ctx, tenantID, ids[i:end])
			if err != nil {
				logger.Warnf(ctx, "learning: doc-order knowledge read failed: %v", err)
				continue
			}
			for _, k := range docs {
				if k == nil {
					continue
				}
				if !k.CreatedAt.IsZero() {
					docAt[k.ID] = k.CreatedAt
				}
				if k.Title != "" {
					docTitle[k.ID] = k.Title
				}
			}
		}
	}

	// Node position = its SUBSTANTIVE home in the material, not its first
	// mention. The earliest-appearance rule (a plain min over citations)
	// had a forward-reference failure: a chapter-5 concept name-dropped in
	// the chapter-1 overview — or in an earlier-uploaded document — ranked
	// as chapter-1 material and got served on day one. The robust estimate:
	// the home document is the one citing the node most, and the position
	// inside it is the upper median of its cited chunks — a single early
	// mention can no longer drag a node forward, while a genuine early
	// concept with a late back-reference keeps most of its citations (and
	// its median) up front. SourceRefs demote to document granularity (no
	// chunk position, orders after that document's chunked pages); a chunk
	// whose document has no creation time (deleted doc, partial read
	// failure) is skipped — zero time would sort EARLIEST, handing
	// unresolvable nodes the foundation slots.
	type resolved struct {
		pos      docPos
		docID    string
		docTitle string
		section  string
	}
	out := make([]resolved, 0, len(pages))
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		// docID → cited chunk positions inside that document.
		byDoc := map[string][]docPos{}
		for _, ref := range perPage[p.Slug] {
			docID, ok := chunkDoc[ref]
			if !ok {
				continue
			}
			at, ok := docAt[docID]
			if !ok {
				continue // unresolvable document: no position from this ref
			}
			byDoc[docID] = append(byDoc[docID], docPos{
				docAt: at, chunkIx: chunkIx[ref], startAt: chunkStart[ref], slug: p.Slug, ref: ref,
			})
		}
		var best *docPos
		homeDoc, homeTitle, section := "", "", ""
		if len(byDoc) > 0 {
			// Home document: most citations, ties to the earliest document.
			homeRefs := 0
			var homeAt time.Time
			for docID, refs := range byDoc {
				better := false
				if len(refs) > homeRefs {
					better = true
				} else if len(refs) == homeRefs {
					switch {
					case homeDoc == "":
						better = true
					case byDoc[docID][0].docAt.Before(homeAt):
						better = true
					case byDoc[docID][0].docAt.Equal(homeAt) && docID < homeDoc:
						better = true // same-second uploads: docID keeps the pick deterministic
					}
				}
				if better {
					homeDoc, homeRefs, homeAt = docID, len(refs), byDoc[docID][0].docAt
				}
			}
			refs := byDoc[homeDoc]
			sort.Slice(refs, func(i, j int) bool { return refs[i].before(refs[j]) })
			best = &refs[len(refs)/2] // upper median: biased away from lone early mentions
			homeTitle = docTitle[homeDoc]
			section = sectionLabel(refs, len(refs)/2, chunkHeader)
		} else {
			// No chunk-level evidence: document-granularity fallback.
			var fallback *docPos
			for _, ref := range p.SourceRefs {
				docID := sourceRefDocID(ref)
				if docID == "" {
					continue
				}
				if at, ok := docAt[docID]; ok {
					pos := docPos{docAt: at, chunkIx: docNoChunkIndex, slug: p.Slug}
					if fallback == nil || pos.before(*fallback) {
						fallback = &pos
					}
					if homeDoc == "" || at.Before(docAt[homeDoc]) {
						homeDoc = docID
					}
				}
			}
			if fallback == nil {
				continue // no resolvable position: the slug keeps "rank last"
			}
			best = fallback
			homeTitle = docTitle[homeDoc]
		}
		out = append(out, resolved{pos: *best, docID: homeDoc, docTitle: homeTitle, section: section})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos.before(out[j].pos) })
	material := make(map[string]NodeMaterial, len(out))
	for i, r := range out {
		material[r.pos.slug] = NodeMaterial{
			Rank: i, DocID: r.docID, DocTitle: r.docTitle,
			Section: r.section, ChunkIx: r.pos.chunkIx,
		}
	}
	return material
}

// sectionLabel derives the chapter-scale label from the node's sorted home
// document references: the FIRST top-level heading ("# ...") wins — chapter
// granularity by construction, immune to the fine-grained "## x.y"
// subsections a median chunk might sit under. With no level-1 heading
// anywhere (unheaded documents, or headings only deeper levels), it falls
// back to the median reference's first heading line, then to any first
// heading line; "" when the material carries no headings at all.
func sectionLabel(sortedRefs []docPos, medianIx int, chunkHeader map[string]string) string {
	if nearest := nearestSectionLine(sortedRefs, medianIx, chunkHeader); nearest != "" {
		// Prefer a level-1 chapter heading anywhere in the citations; the
		// nearest-to-median line is the subsection-scale fallback.
		for _, r := range sortedRefs {
			if isH1(chunkHeader[r.ref]) {
				return headerFirstLine(chunkHeader[r.ref])
			}
		}
		return nearest
	}
	return ""
}

// nearestSectionLine walks outward from the median reference and returns
// the first non-empty heading line — the median's own vote survives even
// when its chunk carries no breadcrumb.
func nearestSectionLine(sortedRefs []docPos, medianIx int, chunkHeader map[string]string) string {
	for dist := 0; dist < len(sortedRefs); dist++ {
		for _, i := range []int{medianIx - dist, medianIx + dist} {
			if i < 0 || i >= len(sortedRefs) {
				continue
			}
			if line := headerFirstLine(chunkHeader[sortedRefs[i].ref]); line != "" {
				return line
			}
		}
	}
	return ""
}

// headerFirstLine returns the breadcrumb's first non-empty line with the
// leading #'s and surrounding space trimmed ("# 第2章\n## 2.3" → "第2章").
func headerFirstLine(header string) string {
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.TrimLeft(strings.TrimSpace(strings.TrimLeft(line, "#")), " ")
	}
	return ""
}

// isH1 reports whether the breadcrumb's first non-empty line is a level-1
// markdown heading: exactly one '#' followed by a space. ("## x" must NOT
// match — TrimLeft would strip every '#' and mistake subsections for
// chapters.)
func isH1(header string) bool {
	first := ""
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		first = line
		break
	}
	return strings.HasPrefix(first, "# ") || first == "#"
}
