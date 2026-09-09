package learning

import (
	"encoding/json"
	"math/rand"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// This file exports the two seams learning-bench (stage 5) needs from this
// package. They are thin wrappers, not new logic: the bench tool lives in
// cmd/learning-bench (a different package) and cannot reach unexported
// identifiers.

// BenchAnchoredLevel derives a node's display level for a fold state —
// the read-path view the recommender and progress endpoints share.
func BenchAnchoredLevel(state FoldState, now time.Time) LevelResult {
	return anchoredLevel(state, now)
}

// BenchRecommendInput is the exported shape of recommendInput.
type BenchRecommendInput = recommendInput

// The exported input is an alias, so production fields cannot silently disappear
// at the bench boundary. Callers must provide historical metadata or disclose
// that they are evaluating an observed-node proxy.
func BenchRecommendNodes(in BenchRecommendInput, now time.Time, rng *rand.Rand, limit int) []Recommendation {
	return recommendNodes(in, now, rng, limit)
}

func BenchHistoryInput(in BenchRecommendInput, history []types.LearningEvent, attempts []types.LearningQuizAttempt, now time.Time) BenchRecommendInput {
	window := []types.LearningEvent{}
	for _, ev := range history {
		if !ev.OccurredAt.Before(now.Add(-touchLookback)) && ev.OccurredAt.Before(now) {
			window = append(window, ev)
		}
	}
	in.Recent = recentAnchors(window, now)
	in.LastVisit = learningLastVisits(window, now)
	in.DirectFacts = CollectDirectFacts(attempts)
	in.QuizStruggled = map[string]bool{}
	in.SelfAssess = map[string]interfaces.SelfAssessMark{}
	for _, e := range history {
		if !e.OccurredAt.Before(now) {
			continue
		}
		if e.Type == types.LearningEventQuizWrong && !e.OccurredAt.Before(now.Add(-touchLookback)) {
			in.QuizStruggled[e.Slug] = true
		}
		if strings.HasPrefix(e.Type, "self_assess_") && !e.OccurredAt.Before(now.Add(-interfaces.SelfAssessVisibleWindow)) {
			old, ok := in.SelfAssess[e.Slug]
			if !ok || !e.OccurredAt.Before(old.At) {
				direction := "down"
				if e.Type == types.LearningEventSelfAssessUp {
					direction = "up"
				}
				in.SelfAssess[e.Slug] = interfaces.SelfAssessMark{Direction: direction, EventType: e.Type, At: e.OccurredAt}
			}
		}
	}
	return in
}

// ---- Verify-mode seams (stage "five-layer verification"): thin exported
// wrappers over the private association/node-layer logic so the offline
// bench can drive it without widening the production surface. ----

// BenchTopicVerdict lifts the private topic-map verdict for bench consumers.
type BenchTopicVerdict = topicMapVerdict

// BenchTouchedSlugs runs channel A offline: which node pages do the given
// answer references touch (chunk ∩ ChunkRefs, doc ∩ SourceRefs).
func BenchTouchedSlugs(pages []*types.WikiPage, refs types.References, kbID string) []string {
	return buildPageRefIndex(pages).touchedSlugs(citationsByKB(refs)[kbID])
}

// BenchClassifyTouch lifts the touch classifier (cite / re_ask / cross_ref /
// capped-empty) for offline verification.
func BenchClassifyTouch(now time.Time, slug string, prior []types.LearningEvent) string {
	return classifyTouch(now, slug, prior)
}

// BenchTopicCandidates lifts channel B's deterministic candidate recall.
func BenchTopicCandidates(stat *types.MemoryTopicStat, pages []*types.WikiPage, maxCandidates int) []string {
	return topicCandidates(stat, pages, maxCandidates)
}

// BenchAcceptedTopicMapsRaw parses a raw adjudication JSON and filters it
// through the anti-hallucination + confidence gates.
func BenchAcceptedTopicMapsRaw(rawJSON string, candidatesByTopic map[string][]string) map[string]BenchTopicVerdict {
	var resp topicMapResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		return nil
	}
	return acceptedTopicMaps(resp, candidatesByTopic)
}

// BenchEdgeCandidatePairs lifts the structural heuristic edge-candidate
// generator (wiki-link / same-folder / shared-document pairs, ordered by
// document position when the rank map is non-nil).
func BenchEdgeCandidatePairs(pages []*types.WikiPage) [][2]string {
	return edgeCandidatePairs(pages, nil)
}

// BenchEdgeVerdict lifts the private edge verdict for bench consumers.
type BenchEdgeVerdict = edgeVerdict

// BenchAcceptedEdgesRaw parses a raw adjudication JSON and filters it down
// to storable prerequisite edges.
func BenchAcceptedEdgesRaw(rawJSON string, submitted [][2]string) []BenchEdgeVerdict {
	var resp edgeResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		return nil
	}
	return acceptedEdges(resp, submitted)
}

// BenchCanonicalAliases uses only pages from the caller's historical snapshot.
func BenchCanonicalAliases(pages []*types.WikiPage) map[string]string {
	_, aliases := canonicalAliasIndex(pages)
	return aliases
}
