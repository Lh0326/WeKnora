package learning

import (
	"context"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ZoneMap serves the module-partitioned recommendation surface. The wiki
// folder IS the module knowledge zone (zones-with-overlap cannot occur by
// construction: a page has exactly one FolderID); cross-zone relations are
// not hidden — they ride along as edges and render dashed. The per-zone
// "1"/"2" are simply the first two cards of that zone inside ONE global
// recommendNodes run, so every channel (review, continuity, remedial,
// foundation, shore-up) ranks zones exactly as it ranked the linear list —
// the zone view partitions that ranking instead of recomputing it.
func (s *Service) ZoneMap(ctx context.Context, kbID string) (*interfaces.ZoneMapResponse, error) {
	asm, err := s.assembleRecommend(ctx, kbID)
	if err != nil {
		return nil, err
	}
	now := asm.now

	// Full deterministic ranking (no ε dice): every pool and shore-up card
	// keeps its global position, which doubles as the zone ordering.
	// The limit must hold the pool AND every rotated shore-up dam: with
	// the cap at limit/2, a dam-heavy KB would otherwise drop its excess
	// dams from `ranked`, mislabelling their zones "complete".
	ranked := recommendNodes(asm.in, now, nil, 2*len(asm.in.Pages)+2)
	decorateRecommendations(asm, ranked)

	// Zone identity: FolderID of each page, with display names.
	zoneOf := map[string]string{}
	for _, p := range asm.pages {
		if p != nil && p.Slug != "" {
			zoneOf[p.Slug] = p.FolderID
		}
	}
	// Per-zone totals/lit counts and the ranked next/second slots.
	type zoneAcc struct {
		id           string
		total, lit   int
		next, second *Recommendation
		nextPos      int
	}
	acc := map[string]*zoneAcc{}
	get := func(id string) *zoneAcc {
		if z, ok := acc[id]; ok {
			return z
		}
		z := &zoneAcc{id: id, nextPos: -1}
		acc[id] = z
		return z
	}
	// Raw last activity powers the 48h "recent" twinkle (zero-weight
	// touches included). Soft read: an empty map dims nothing.
	activity := map[string]time.Time{}
	if m, err := s.repo.ListLastActivity(ctx, asm.scope); err != nil {
		logger.Warnf(ctx, "learning: zone-map last-activity read failed (kb %s): %v", kbID, err)
	} else {
		activity = m
	}
	recentCut := now.Add(-48 * time.Hour)
	// zoneLinkCap bounds the wiki-link edges per source page: a hub entity
	// linking to dozens of peers would paint an unreadable starburst.
	const zoneLinkCap = 12

	nodes := make([]interfaces.ZoneNode, 0, len(asm.pages))
	for _, p := range asm.pages {
		if p == nil || p.Slug == "" {
			continue
		}
		slug := p.Slug
		state := asm.in.States[slug]
		lv := gatedAnchoredLevel(state, asm.in.DirectFacts[slug], now)
		pEff := EffectiveP(state, now)
		if state.EvidenceCount == 0 && state.LastEvidenceAt.IsZero() {
			pEff = 0 // the "statement nobody earned" guard, same as /map
		}
		faded := lv.Level == LevelUnseen && state.EvidenceCount > 0
		_, skipped := asm.in.Skips[slug]
		// Coverage counts the person as past a node when they have lit it
		// OR retired it by declaration ("已掌握，移除推荐" is a coverage
		// claim): the counter reads (已点亮)/(总数), so a skip must raise
		// coverage, never erase it — the old !skipped exclusion made a
		// 1/1 zone read 0/1 the moment the user declared mastery.
		if skipped || (lv.Level != LevelUnseen && !faded) {
			get(zoneOf[slug]).lit++
		}
		get(zoneOf[slug]).total++
		lastActivity := activity[slug]
		if lastActivity.IsZero() || lastActivity.Before(state.LastEvidenceAt) {
			lastActivity = state.LastEvidenceAt
		}
		mat := asm.materials[slug]
		nodes = append(nodes, interfaces.ZoneNode{
			Slug: slug, Title: p.Title, Level: string(lv.Level),
			PEff: pEff, EvidenceCount: state.EvidenceCount,
			LowConfidence: lv.LowConfidence, LastActivityAt: lastActivity,
			Recent: lastActivity.After(recentCut), Skipped: skipped, Faded: faded,
			FolderID: zoneOf[slug], FolderName: asm.folderName[zoneOf[slug]],
			Section: mat.Section, DocTitle: mat.DocTitle,
		})
		if _, ok := asm.materials[slug]; ok {
			nodes[len(nodes)-1].DocRank = mat.Rank + 1
		}
	}
	// Ranked cards slot into their zone's next/second by global position.
	for pos, rec := range ranked {
		z := get(zoneOf[rec.Slug])
		switch {
		case z.next == nil:
			r := rec
			z.next, z.nextPos = &r, pos
		case z.second == nil:
			r := rec
			z.second = &r
		}
	}

	zones := make([]interfaces.ZoneSummary, 0, len(acc))
	for _, z := range acc {
		zones = append(zones, interfaces.ZoneSummary{
			FolderID: z.id, FolderName: asm.folderName[z.id],
			Total: z.total, Lit: z.lit, Next: z.next, Second: z.second,
		})
	}
	// Zones with a live "1" lead, best-first; completed zones trail. Root
	// ("" id) names itself on the client via the same rootUnit key the
	// progress units use.
	sort.SliceStable(zones, func(i, j int) bool {
		a, b := zones[i], zones[j]
		switch {
		case a.Next != nil && b.Next != nil:
			return acc[a.FolderID].nextPos < acc[b.FolderID].nextPos
		case a.Next != nil:
			return true
		case b.Next != nil:
			return false
		default:
			if a.FolderName != b.FolderName {
				return a.FolderName < b.FolderName
			}
			return a.FolderID < b.FolderID
		}
	})

	edges := make([]interfaces.ZoneEdge, 0, len(asm.edges)+16)
	seenPair := map[[2]string]bool{}
	addEdge := func(from, to, kind string) {
		if from == to {
			return
		}
		if _, ok := zoneOf[from]; !ok {
			return
		}
		if _, ok := zoneOf[to]; !ok {
			return
		}
		key := [2]string{from, to}
		if seenPair[key] {
			return
		}
		seenPair[key] = true
		edges = append(edges, interfaces.ZoneEdge{From: from, To: to, Kind: kind})
	}
	// Prerequisite edges (LLM-adjudicated): the pedagogical backbone.
	for _, e := range asm.edges {
		if e.Relation == types.LearningEdgePrerequisite {
			addEdge(e.FromSlug, e.ToSlug, "prereq")
		}
	}
	// Wiki-link edges (OutLinks between node pages): the structural relations
	// the graph tab renders. Without them a KB with zero prerequisite edges
	// would have no relation lines at all, and hover would highlight nothing.
	// Cap per source to keep a hub page from painting a starburst.
	for _, p := range asm.pages {
		if p == nil || len(p.OutLinks) == 0 {
			continue
		}
		links := p.OutLinks
		if len(links) > zoneLinkCap {
			links = links[:zoneLinkCap]
		}
		for _, to := range links {
			addEdge(p.Slug, to, "wikilink")
		}
	}
	return &interfaces.ZoneMapResponse{Zones: zones, Nodes: nodes, Edges: edges}, nil
}
