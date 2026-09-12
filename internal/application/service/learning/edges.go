package learning

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Edge-pair batching and candidate caps. The doc-sharing source is capped
// per document so a single sweeping document cannot generate a quadratic
// candidate blob.
const (
	edgeBatchSize      = 40
	edgeMaxPerDocGroup = 8
	edgeMaxPairsPerKB  = 400
)

// edgeCandidatePairs generates the structural-heuristic candidate pairs
// for one KB's pages: (a) wiki-link direct pairs, (b) same-folder
// neighbours, (c) pages sharing a source document. docRank carries each
// node's position in the source material (the 从浅入深 channel): it orders
// the same-folder and same-document groups so the pairs submitted to the
// adjudicator run shallow→deep, and a nil map falls back to (title, slug)
// order — the pre-document-order behaviour.
func edgeCandidatePairs(pages []*types.WikiPage, docRank map[string]int) [][2]string {
	bySlug := map[string]*types.WikiPage{}
	sorted := append([]*types.WikiPage{}, pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	for _, p := range sorted {
		if p != nil && p.Slug != "" {
			bySlug[p.Slug] = p
		}
	}

	seen := map[[2]string]bool{}
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		if _, ok := bySlug[from]; !ok {
			return
		}
		if _, ok := bySlug[to]; !ok {
			return
		}
		key := [2]string{from, to}
		if seen[key] {
			return
		}
		seen[key] = true
	}

	// (a) wiki-link direct pairs, in sorted page order for determinism.
	for _, p := range sorted {
		for _, out := range p.OutLinks {
			add(p.Slug, out)
		}
	}

	// (b) same-folder neighbours: pages adjacent in (document order, title,
	// slug) order — reading order inside a folder, alphabetical only as the
	// fallback.
	rankOf := func(slug string) int {
		if r, ok := docRank[slug]; ok {
			return r
		}
		return math.MaxInt32
	}
	folders := map[string][]*types.WikiPage{}
	for _, p := range sorted {
		folders[p.FolderID] = append(folders[p.FolderID], p)
	}
	folderKeys := make([]string, 0, len(folders))
	for k := range folders {
		folderKeys = append(folderKeys, k)
	}
	sort.Strings(folderKeys)
	for _, fk := range folderKeys {
		group := folders[fk]
		sort.Slice(group, func(i, j int) bool {
			if ri, rj := rankOf(group[i].Slug), rankOf(group[j].Slug); ri != rj {
				return ri < rj
			}
			if group[i].Title != group[j].Title {
				return group[i].Title < group[j].Title
			}
			return group[i].Slug < group[j].Slug
		})
		for i := 0; i+1 < len(group); i++ {
			add(group[i].Slug, group[i+1].Slug)
		}
	}

	// (c) source-document co-occurrence, capped per document group. The
	// group is ordered by document position and the cap keeps the EARLIEST
	// nodes — the foundations a sweeping chapter-20 listing would otherwise
	// evict — and every pair runs earlier→later, the direction the source
	// material itself reads.
	byDoc := map[string][]string{}
	for _, p := range sorted {
		for _, ref := range p.SourceRefs {
			if docID := sourceRefDocID(ref); docID != "" {
				byDoc[docID] = append(byDoc[docID], p.Slug)
			}
		}
	}
	docKeys := make([]string, 0, len(byDoc))
	for k := range byDoc {
		docKeys = append(docKeys, k)
	}
	sort.Strings(docKeys)
	for _, dk := range docKeys {
		group := byDoc[dk]
		sort.Slice(group, func(i, j int) bool {
			if ri, rj := rankOf(group[i]), rankOf(group[j]); ri != rj {
				return ri < rj
			}
			return group[i] < group[j]
		})
		if len(group) > edgeMaxPerDocGroup {
			group = group[:edgeMaxPerDocGroup]
		}
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				add(group[i], group[j])
			}
		}
	}

	// Deterministic output: sort the collected keys.
	keys := make([][2]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	if len(keys) > edgeMaxPairsPerKB {
		keys = keys[:edgeMaxPairsPerKB]
	}
	return keys
}

// edgeVerdict is one adjudicated pair.
type edgeVerdict struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Relation   string  `json:"relation"`
	Confidence float64 `json:"confidence"`
}

// edgeResponse is the LLM response envelope.
type edgeResponse struct {
	Pairs []edgeVerdict `json:"pairs"`
}

// acceptedEdges filters adjudication results: only prerequisite relations
// at or above the confidence floor, and only for pairs actually submitted
// (the deterministic anti-hallucination guard).
func acceptedEdges(resp edgeResponse, submitted [][2]string) []edgeVerdict {
	submittedSet := map[[2]string]bool{}
	for _, p := range submitted {
		submittedSet[p] = true
	}
	var out []edgeVerdict
	for _, v := range resp.Pairs {
		if v.Relation != "prerequisite" || v.Confidence < EdgeConfidenceMin {
			continue
		}
		if !submittedSet[[2]string{v.From, v.To}] {
			continue
		}
		out = append(out, v)
	}
	return out
}

// edgeUserPrompt renders the pair list with titles, first-paragraph
// summaries, and each page's position in the source material (the 从浅入深
// channel: smaller pos = introduced earlier), the wiki-dedup nested style
// adapted to pairs. Only pages the submitted pairs actually involve are
// described — the adjudicator judges pairs, and an unrelated page in the
// answer space is prompt bloat, so the input stays proportional to the
// candidate set rather than the KB size.
func edgeUserPrompt(pagesBySlug map[string]*types.WikiPage, pairs [][2]string, docRank map[string]int) string {
	involved := map[string]bool{}
	for _, p := range pairs {
		if _, ok := pagesBySlug[p[0]]; ok {
			involved[p[0]] = true
		}
		if _, ok := pagesBySlug[p[1]]; ok {
			involved[p[1]] = true
		}
	}
	slugs := make([]string, 0, len(involved))
	for slug := range involved {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	var b strings.Builder
	describe := func(slug string) {
		p := pagesBySlug[slug]
		if p == nil {
			return
		}
		b.WriteString("  <page slug=\"" + slug + "\" type=\"" + p.PageType + "\" title=\"" + p.Title + "\"")
		if r, ok := docRank[slug]; ok {
			b.WriteString(" pos=\"" + strconv.Itoa(r) + "\"")
		}
		b.WriteString(">\n")
		if p.Summary != "" {
			b.WriteString("    <summary>" + firstSentences(p.Summary, 2) + "</summary>\n")
		}
		b.WriteString("  </page>\n")
	}
	b.WriteString("<pages>\n")
	for _, slug := range slugs {
		describe(slug)
	}
	b.WriteString("</pages>\n\n<pairs>\n")
	for _, p := range pairs {
		b.WriteString("  <pair from=\"" + p[0] + "\" to=\"" + p[1] + "\" />\n")
	}
	b.WriteString("</pairs>\n")
	return b.String()
}

// edgeSchema is the response format for the edge adjudication call.
var edgeSchema = jsonRaw(`{
  "type": "object",
  "properties": {
    "pairs": {"type": "array", "items": {"type": "object"}}
  },
  "required": ["pairs"]
}`)

// firstSentences returns up to n sentences of s as a compact summary.
func firstSentences(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" || n <= 0 {
		return ""
	}
	count := 0
	for i, r := range s {
		if strings.ContainsRune(".!?。！？", r) {
			count++
			if count == n {
				return strings.TrimSpace(s[:i+len(string(r))])
			}
		}
	}
	return s
}

// edgeWatermark returns the newest edge creation time for a KB — pages
// updated after it are the incremental candidates for the next pass.
func edgeWatermark(edges []types.LearningEdge) time.Time {
	var max time.Time
	for _, e := range edges {
		if e.CreatedAt.After(max) {
			max = e.CreatedAt
		}
	}
	return max
}

// pairsInvolvingNewPages keeps only pairs with at least one page updated
// after the watermark. An empty watermark (never processed) keeps all.
func pairsInvolvingNewPages(pairs [][2]string, pagesBySlug map[string]*types.WikiPage, watermark time.Time) [][2]string {
	if watermark.IsZero() {
		return pairs
	}
	var out [][2]string
	for _, p := range pairs {
		fresh := false
		for _, slug := range p {
			if page := pagesBySlug[slug]; page != nil && page.UpdatedAt.After(watermark) {
				fresh = true
				break
			}
		}
		if fresh {
			out = append(out, p)
		}
	}
	return out
}
