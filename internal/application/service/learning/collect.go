package learning

import (
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// touchLookback bounds how far back the collector looks for a previous
// touch when classifying a new one. The re-ask window itself stays 48h
// (ReAskWindowHours); the lookback exists only so "re-touched after the
// window" (cross_ref) can be told apart from "first touch" without scanning
// a subject's entire event history on every answer.
const touchLookback = 7 * 24 * time.Hour

// kbCitations is one answer's evidence, grouped per knowledge base:
// the chunk ids and document ids the answer actually cited.
type kbCitations struct {
	chunkIDs    map[string]bool
	docIDs      map[string]bool
	preciseDocs map[string]bool // a cited chunk identifies this document; no coarse fallback
}

// citationsByKB groups an answer's references by knowledge base and drops
// everything without a usable identity. Chunk-level ids drive the precise
// (ChunkRefs) half of the evidence channel; document-level ids drive the
// coarser (SourceRefs prefix) half, mirroring what the memory subsystem's
// affinity write consumes from the very same rows.
func citationsByKB(refs types.References) map[string]kbCitations {
	byKB := map[string]kbCitations{}
	for _, ref := range refs {
		if ref == nil || ref.KnowledgeBaseID == "" {
			continue
		}
		c, ok := byKB[ref.KnowledgeBaseID]
		if !ok {
			c = kbCitations{chunkIDs: map[string]bool{}, docIDs: map[string]bool{}, preciseDocs: map[string]bool{}}
			byKB[ref.KnowledgeBaseID] = c
		}
		if ref.ID != "" {
			c.chunkIDs[ref.ID] = true
			if ref.KnowledgeID != "" {
				c.preciseDocs[ref.KnowledgeID] = true
			}
		}
		if ref.KnowledgeID != "" {
			c.docIDs[ref.KnowledgeID] = true
		}
	}
	return byKB
}

// pageRefs is the reference side of one wiki page: the chunk ids in its
// ChunkRefs and the document ids extracted from its SourceRefs
// ("<knowledge_id>|<title>" prefix).
type pageRefs struct {
	chunkIDs map[string]bool
	docIDs   map[string]bool
}

// pageRefIndex is the KB-side half of the evidence channel: every
// entity/concept page's reference sets, keyed by slug. Intersection against
// an answer's citations is then pure set membership — deterministic,
// hallucination-free and cost-free of any model call.
type pageRefIndex struct {
	pages map[string]pageRefs
	slugs []string // sorted, so every derived list is deterministic
}

// sourceRefDocID splits a SourceRefs entry's "<knowledge_id>|<title>" form.
// Entries without the separator carry no doc identity and are ignored.
func sourceRefDocID(ref string) string {
	if i := strings.IndexByte(ref, '|'); i > 0 {
		return ref[:i]
	}
	return ""
}

// sourceRefParts is the two-value variant for callers that also want the
// human-readable title half of the "<knowledge_id>|<title>" form.
func sourceRefParts(ref string) (id, title string) {
	if i := strings.IndexByte(ref, '|'); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ""
}

// buildPageRefIndex folds wiki pages into the lookup structure. Page types
// outside entity/concept are the caller's filtering decision; the index
// itself is type-agnostic.
func buildPageRefIndex(pages []*types.WikiPage) *pageRefIndex {
	idx := &pageRefIndex{pages: map[string]pageRefs{}}
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		pr := pageRefs{chunkIDs: map[string]bool{}, docIDs: map[string]bool{}}
		for _, c := range p.ChunkRefs {
			if c != "" {
				pr.chunkIDs[c] = true
			}
		}
		for _, s := range p.SourceRefs {
			if docID := sourceRefDocID(s); docID != "" {
				pr.docIDs[docID] = true
			}
		}
		idx.pages[p.Slug] = pr
		idx.slugs = append(idx.slugs, p.Slug)
	}
	sort.Strings(idx.slugs)
	return idx
}

func (idx *pageRefIndex) empty() bool {
	return idx == nil || len(idx.slugs) == 0
}

// touchedSlugs intersects one answer's citations with the page index. A
// page is touched when any cited chunk appears in its ChunkRefs or any
// cited document matches one of its SourceRefs. One answer touches each
// slug at most once regardless of how many citations support it.
func (idx *pageRefIndex) touchedSlugs(c kbCitations) []string {
	var touched []string
	for _, slug := range idx.slugs {
		pr := idx.pages[slug]
		hit := false
		for chunkID := range c.chunkIDs {
			if pr.chunkIDs[chunkID] {
				hit = true
				break
			}
		}
		if !hit {
			for docID := range c.docIDs {
				if pr.docIDs[docID] && !c.preciseDocs[docID] {
					hit = true
					break
				}
			}
		}
		if hit {
			touched = append(touched, slug)
		}
	}
	return touched
}

// classifyTouch decides which event type a new touch of slug represents,
// given the subject's prior touch events (already scope-filtered and
// lookback-bounded by the caller):
//
//   - no prior touch (or none of touch semantics)  → answer_cite
//   - prior touch inside the re-ask window, and the
//     window does not carry a re_ask yet           → re_ask (negative)
//   - prior touch inside the window, a re_ask already
//     recorded in it                               → skip ("")
//   - prior touch outside the window               → cross_ref (stronger)
//
// quiz_* and backfill events are deliberately not "touches": they either
// are direct evidence in their own right or replayed history, and counting
// them here would let a quiz answer suppress the next citation's class.
//
// The one-re_ask-per-window cap is the semantic fix for "multi-angle
// learning punished as repeated failure": the spec's re_ask means "the
// previous answer did not land" (反复基础追问), and that signal is fully
// expressed by the first occurrence. Further same-window touches are
// typically the person exploring DIFFERENT facets of the node from new
// sessions — exploring a topic deeply for a day must not sink its mastery
// without bound, which would betray the honest "活跃触达与巩固程度"
// semantics of the measurement. Those touches carry no new information,
// so they are skipped rather than re-scored.
func classifyTouch(now time.Time, slug string, prior []types.LearningEvent) string {
	latest := time.Time{}
	windowHasReAsk := false
	for _, ev := range prior {
		if ev.Slug != slug {
			continue
		}
		switch ev.Type {
		case types.LearningEventAnswerCite, types.LearningEventCrossRef, types.LearningEventReAsk:
		default:
			continue
		}
		if ev.OccurredAt.After(latest) {
			latest = ev.OccurredAt
		}
		if ev.Type == types.LearningEventReAsk &&
			now.Sub(ev.OccurredAt) <= ReAskWindowHours*time.Hour {
			windowHasReAsk = true
		}
	}
	if latest.IsZero() {
		return types.LearningEventAnswerCite
	}
	if now.Sub(latest) <= ReAskWindowHours*time.Hour {
		if windowHasReAsk {
			return "" // capped: the window already voiced "did not land" once
		}
		return types.LearningEventReAsk
	}
	return types.LearningEventCrossRef
}

// weightForType maps an event type to its fold weight. The weight is frozen
// on the event row at append time; the mapping lives here so there is
// exactly one place where type becomes number.
func weightForType(eventType string) float64 {
	switch eventType {
	case types.LearningEventAnswerCite:
		return WeightAnswerCite
	case types.LearningEventCrossRef:
		return WeightCrossRef
	case types.LearningEventReAsk:
		return WeightReAsk
	case types.LearningEventTopicSignal:
		return WeightTopicSignal
	case types.LearningEventWikiToolRead:
		// Opportunistic agent wiki reads ride the topic-signal weight: the
		// §3.3.6 spec reuses WeightTopicSignal for this low-trust touch.
		return WeightTopicSignal
	case types.LearningEventQuizCorrect:
		return WeightQuizCorrect
	case types.LearningEventQuizWrong:
		return WeightQuizWrong
	default:
		return 0
	}
}
