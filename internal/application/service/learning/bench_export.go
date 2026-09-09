package learning

import (
	"encoding/json"
	"math/rand"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
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
type BenchRecommendInput struct {
	Pages         []*types.WikiPage
	Edges         []types.LearningEdge
	States        map[string]FoldState
	Affinity      map[string]bool
	HasQuiz       map[string]bool
	QuizStruggled map[string]bool
	DirectFacts   map[string][]DirectQuizFact
}

// BenchRecommendNodes is the exported rule-based recommender.
func BenchRecommendNodes(in BenchRecommendInput, now time.Time, rng *rand.Rand, limit int) []Recommendation {
	return recommendNodes(recommendInput{
		Pages: in.Pages, Edges: in.Edges, States: in.States, Affinity: in.Affinity,
		HasQuiz: in.HasQuiz, QuizStruggled: in.QuizStruggled, DirectFacts: in.DirectFacts,
	}, now, rng, limit)
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
