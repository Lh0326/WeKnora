package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
)

// ---- replay input (the export payload plus edges if available) ----

type replayInput struct {
	Events []types.LearningEvent `json:"events"`
	Edges  []types.LearningEdge  `json:"edges,omitempty"`
}

// ---- per-ranker metrics (self-contained, no globals) ----

type benchMetrics struct {
	Label       string
	HitsByK     map[int]int // K → hit count
	Predictions int
	MRRSum      float64
}

func newBenchMetrics(label string) *benchMetrics {
	return &benchMetrics{Label: label, HitsByK: map[int]int{}}
}

func (m *benchMetrics) hitRate(k int) float64 {
	if m.Predictions == 0 {
		return 0
	}
	return float64(m.HitsByK[k]) / float64(m.Predictions)
}

func (m *benchMetrics) mrr() float64 {
	if m.Predictions == 0 {
		return 0
	}
	return m.MRRSum / float64(m.Predictions)
}

// ---- replay ----

// applyWeightOverrides parses "-w K=V,K=V" into the learning package's
// tunable vars before a replay run — the sweep harness for the designated
// offline tuning loop. Unknown keys are ignored; values apply to this
// process only (production never sets them).
func applyWeightOverrides(spec string) {
	if spec == "" {
		return
	}
	set := func(f float64, dst *float64) { *dst = f }
	for _, kv := range strings.Split(spec, ",") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(parts[0]) {
		case "WeightAnswerCite":
			set(v, &learning.WeightAnswerCite)
		case "WeightCrossRef":
			set(v, &learning.WeightCrossRef)
		case "WeightReAsk":
			set(v, &learning.WeightReAsk)
		case "WeightTopicSignal":
			set(v, &learning.WeightTopicSignal)
		case "WeightQuizCorrect":
			set(v, &learning.WeightQuizCorrect)
		case "WeightQuizWrong":
			set(v, &learning.WeightQuizWrong)
		case "QuizRepeatDecay":
			set(v, &learning.QuizRepeatDecay)
		case "BackfillDiscount":
			set(v, &learning.BackfillDiscount)
		case "StabilityBaseDays":
			set(v, &learning.StabilityBaseDays)
		case "StabilityGrowth":
			set(v, &learning.StabilityGrowth)
		case "StabilitySaturationExp":
			set(v, &learning.StabilitySaturationExp)
		case "StabilityLapseShrink":
			set(v, &learning.StabilityLapseShrink)
		case "RecommendEpsilon":
			set(v, &learning.RecommendEpsilon)
		case "PrereqBlockedFloor":
			set(v, &learning.PrereqBlockedFloor)
		case "ScoreAffinity":
			set(v, &learning.ScoreAffinity)
		case "ScoreReviewDue":
			set(v, &learning.ScoreReviewDue)
		case "ScoreBlindSpot":
			set(v, &learning.ScoreBlindSpot)
		case "ScoreRecency":
			set(v, &learning.ScoreRecency)
		case "RecencyHalfLifeDays":
			set(v, &learning.RecencyHalfLifeDays)
		case "ScoreBypass":
			set(v, &learning.ScoreBypass)
		case "ScoreRemedial":
			set(v, &learning.ScoreRemedial)
		}
	}
}

func runReplay(exportPath, topKStr string) error {
	applyWeightOverrides(weightOverrides)

	raw, err := os.ReadFile(exportPath)
	if err != nil {
		return err
	}
	var input replayInput
	if err := json.Unmarshal(raw, &input); err != nil {
		// Try the full ExportPayload shape (has events nested under .data).
		var payload struct {
			Data struct {
				Events []types.LearningEvent `json:"events"`
			} `json:"data"`
		}
		if err2 := json.Unmarshal(raw, &payload); err2 == nil && len(payload.Data.Events) > 0 {
			input.Events = payload.Data.Events
		} else {
			return fmt.Errorf("export parse: %w (also tried .data.events)", err)
		}
	}
	if len(input.Events) < 10 {
		return fmt.Errorf("need ≥10 events for replay, got %d", len(input.Events))
	}

	// Parse K values.
	ks := []int{5, 10}
	if topKStr != "" {
		ks = nil
		for _, s := range strings.Split(topKStr, ",") {
			if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 {
				ks = append(ks, v)
			}
		}
	}

	// Sort by time (defensive).
	events := input.Events
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })

	// 80/20 split.
	splitIdx := int(float64(len(events)) * 0.8)
	prefix := events[:splitIdx]
	suffix := events[splitIdx:]

	fmt.Printf("  events: %d total, %d prefix, %d suffix\n", len(events), len(prefix), len(suffix))

	// Fold prefix into per-slug states. With -refold, the frozen per-row
	// weights are IGNORED and recomputed from the currently-set package
	// weights (plus quiz repeat decay per slug) — the "what if these weights
	// had been in force" mode the tuning sweep needs; without it, retuning
	// is by design a no-op on history (weights are frozen at write time).
	prefixStates := map[string]learning.FoldState{}
	quizSeen := map[string]int{}
	for _, ev := range prefix {
		w := ev.Weight
		if refold {
			w = refoldWeight(ev, quizSeen)
		}
		state := prefixStates[ev.Slug]
		prefixStates[ev.Slug] = learning.FoldEvent(state, learning.Event{
			Type: ev.Type, Weight: w, OccurredAt: ev.OccurredAt,
		})
	}

	// Build the page universe from all slugs seen.
	slugSet := map[string]bool{}
	for _, ev := range events {
		slugSet[ev.Slug] = true
	}
	var pages []*types.WikiPage
	for slug := range slugSet {
		pt := "concept"
		if strings.HasPrefix(slug, "entity/") {
			pt = "entity"
		}
		pages = append(pages, &types.WikiPage{Slug: slug, Title: slug, PageType: pt})
	}

	// Affinity: every slug with events in the prefix.
	affinity := map[string]bool{}
	for _, ev := range prefix {
		affinity[ev.Slug] = true
	}

	// HasQuiz: any slug with a quiz event.
	hasQuiz := map[string]bool{}
	for _, ev := range events {
		if ev.Type == types.LearningEventQuizCorrect || ev.Type == types.LearningEventQuizWrong {
			hasQuiz[ev.Slug] = true
		}
	}

	// QuizStruggled: slugs with a wrong answer in the prefix — the direct
	// remedial signal, same derivation the service read path uses.
	quizStruggled := map[string]bool{}
	for _, ev := range prefix {
		if ev.Type == types.LearningEventQuizWrong {
			quizStruggled[ev.Slug] = true
		}
	}

	// Popularity: total event count per slug in the prefix.
	popCount := map[string]int{}
	for _, ev := range prefix {
		popCount[ev.Slug]++
	}

	// Ground truths: unique next-touch slugs from the suffix.
	var groundTruths []string
	seen := map[string]bool{}
	for _, ev := range suffix {
		if !seen[ev.Slug] {
			seen[ev.Slug] = true
			groundTruths = append(groundTruths, ev.Slug)
		}
	}
	if len(groundTruths) == 0 {
		return fmt.Errorf("no unique next-touch slugs in the suffix")
	}

	rng := rand.New(rand.NewSource(42)) // deterministic for reproducibility
	now := time.Now()
	maxK := maxOf(ks)

	// ---- three rankers, each producing top-maxK for every ground truth ----
	recRanker := func() []string {
		recs := learning.BenchRecommendNodes(learning.BenchRecommendInput{
			Pages: pages, Edges: input.Edges, States: prefixStates,
			Affinity: affinity, HasQuiz: hasQuiz, QuizStruggled: quizStruggled,
		}, now, rng, maxK)
		var out []string
		for _, r := range recs {
			out = append(out, r.Slug)
		}
		return out
	}
	randRanker := func() []string {
		var unseen []string
		for _, p := range pages {
			if s, ok := prefixStates[p.Slug]; !ok || s.EvidenceCount == 0 {
				unseen = append(unseen, p.Slug)
			}
		}
		rng.Shuffle(len(unseen), func(i, j int) { unseen[i], unseen[j] = unseen[j], unseen[i] })
		if len(unseen) > maxK {
			unseen = unseen[:maxK]
		}
		return unseen
	}
	popRanker := func() []string {
		var popSlugs []string
		for slug := range popCount {
			popSlugs = append(popSlugs, slug)
		}
		sort.Slice(popSlugs, func(i, j int) bool {
			if popCount[popSlugs[i]] != popCount[popSlugs[j]] {
				return popCount[popSlugs[i]] > popCount[popSlugs[j]]
			}
			return popSlugs[i] < popSlugs[j]
		})
		if len(popSlugs) > maxK {
			popSlugs = popSlugs[:maxK]
		}
		return popSlugs
	}

	recM := evalOne("recommender", recRanker, groundTruths, ks)
	randM := evalOne("random", randRanker, groundTruths, ks)
	popM := evalOne("popularity", popRanker, groundTruths, ks)

	// ---- online protocol ----
	// The static table above asks whether ONE list frozen at the split
	// covers all future touches. The online protocol is how the system
	// actually serves: before every touch, the recommend endpoint re-ranks
	// from the CURRENT fold state, the touch happens, the state folds it
	// in. Same metrics, same baselines, recomputed per step for all three
	// rankers alike — the learner's own next interaction is the target.
	onlineRecM, onlineRandM, onlinePopM := runOnlineReplay(events, splitIdx, ks, input.Edges)

	// Output table.
	printTable := func(title string, ms []*benchMetrics) {
		fmt.Printf("\n%s\n", title)
		fmt.Printf("  %-14s", "Ranker")
		for _, k := range ks {
			fmt.Printf("  HR@%-3d", k)
		}
		fmt.Printf("  MRR\n")
		fmt.Printf("  %-14s", "--------------")
		for range ks {
			fmt.Printf("  ------")
		}
		fmt.Printf("  ----\n")
		for _, m := range ms {
			fmt.Printf("  %-14s", m.Label)
			for _, k := range ks {
				fmt.Printf("  %.3f ", m.hitRate(k))
			}
			fmt.Printf("  %.3f\n", m.mrr())
		}
	}
	printTable("  [static protocol] one list frozen at the 80% split, scored on every unique next touch",
		[]*benchMetrics{recM, randM, popM})
	printTable("  [online protocol] re-ranked before every suffix touch, all rankers recomputed per step",
		[]*benchMetrics{onlineRecM, onlineRandM, onlinePopM})
	fmt.Printf("\n  predictions: static %d (unique next touches), online %d (every suffix event)\n",
		recM.Predictions, onlineRecM.Predictions)

	// Sanity: recommender must beat random on at least one metric (goal stop condition).
	anyBetter := recM.mrr() > randM.mrr()
	for _, k := range ks {
		if recM.hitRate(k) > randM.hitRate(k) {
			anyBetter = true
		}
	}
	if !anyBetter {
		return fmt.Errorf("recommender failed to beat random baseline on any metric — " +
			"see numbers above; consider tuning WeightAnswerCite/WeightQuizCorrect ratio")
	}
	return nil
}

// evalOne runs a ranker once per ground truth and accumulates metrics.
func evalOne(label string, rank func() []string, groundTruths []string, ks []int) *benchMetrics {
	m := newBenchMetrics(label)
	for _, gt := range groundTruths {
		ranked := rank()
		m.Predictions++
		rank := 0
		for i, slug := range ranked {
			if slug == gt {
				rank = i + 1
				break
			}
		}
		if rank > 0 {
			m.MRRSum += 1.0 / float64(rank)
			for _, k := range ks {
				if rank <= k {
					m.HitsByK[k]++
				}
			}
		}
	}
	return m
}

func maxOf(ks []int) int {
	if len(ks) == 0 {
		return 10
	}
	max := ks[0]
	for _, k := range ks {
		if k > max {
			max = k
		}
	}
	return max
}

// runOnlineReplay walks the suffix event by event: before each event every
// ranker re-ranks from the current fold state, the event's slug is the
// target, then the event folds in (recomputing weights under -refold the
// same way the prefix fold does). Deterministic: the recommender runs with
// a nil rng (no exploration slot), the random baseline reseeds per step.
func runOnlineReplay(events []types.LearningEvent, splitIdx int, ks []int, edges []types.LearningEdge) (*benchMetrics, *benchMetrics, *benchMetrics) {
	states := map[string]learning.FoldState{}
	popCount := map[string]int{}
	quizSeen := map[string]int{}
	quizStruggled := map[string]bool{}
	fold := func(ev types.LearningEvent) {
		w := ev.Weight
		if refold {
			w = refoldWeight(ev, quizSeen)
		}
		states[ev.Slug] = learning.FoldEvent(states[ev.Slug], learning.Event{
			Type: ev.Type, Weight: w, OccurredAt: ev.OccurredAt,
		})
		popCount[ev.Slug]++
		if ev.Type == types.LearningEventQuizWrong {
			quizStruggled[ev.Slug] = true
		}
	}
	for _, ev := range events[:splitIdx] {
		fold(ev)
	}

	var pages []*types.WikiPage
	pageSet := map[string]bool{}
	for _, ev := range events {
		if !pageSet[ev.Slug] {
			pageSet[ev.Slug] = true
			pt := "concept"
			if strings.HasPrefix(ev.Slug, "entity/") {
				pt = "entity"
			}
			pages = append(pages, &types.WikiPage{Slug: ev.Slug, Title: ev.Slug, PageType: pt})
		}
	}
	hasQuiz := map[string]bool{}
	for _, ev := range events {
		if ev.Type == types.LearningEventQuizCorrect || ev.Type == types.LearningEventQuizWrong {
			hasQuiz[ev.Slug] = true
		}
	}

	recM := newBenchMetrics("recommender")
	randM := newBenchMetrics("random")
	popM := newBenchMetrics("popularity")
	maxK := maxOf(ks)
	for _, ev := range events[splitIdx:] {
		affinity := map[string]bool{}
		for slug := range popCount {
			affinity[slug] = true
		}
		recs := learning.BenchRecommendNodes(learning.BenchRecommendInput{
			Pages: pages, Edges: edges, States: states,
			Affinity: affinity, HasQuiz: hasQuiz, QuizStruggled: quizStruggled,
		}, ev.OccurredAt, nil, maxK)
		var recList []string
		for _, r := range recs {
			recList = append(recList, r.Slug)
		}

		var unseen []string
		for _, p := range pages {
			if s, ok := states[p.Slug]; !ok || s.EvidenceCount == 0 {
				unseen = append(unseen, p.Slug)
			}
		}
		rng := rand.New(rand.NewSource(int64(len(popCount))<<32 | int64(len(unseen))))
		rng.Shuffle(len(unseen), func(i, j int) { unseen[i], unseen[j] = unseen[j], unseen[i] })
		if len(unseen) > maxK {
			unseen = unseen[:maxK]
		}

		var popSlugs []string
		for slug := range popCount {
			popSlugs = append(popSlugs, slug)
		}
		sort.Slice(popSlugs, func(i, j int) bool {
			if popCount[popSlugs[i]] != popCount[popSlugs[j]] {
				return popCount[popSlugs[i]] > popCount[popSlugs[j]]
			}
			return popSlugs[i] < popSlugs[j]
		})
		if len(popSlugs) > maxK {
			popSlugs = popSlugs[:maxK]
		}

		score := func(m *benchMetrics, ranked []string) {
			m.Predictions++
			rank := 0
			for i, slug := range ranked {
				if slug == ev.Slug {
					rank = i + 1
					break
				}
			}
			if rank > 0 {
				m.MRRSum += 1.0 / float64(rank)
				for _, k := range ks {
					if rank <= k {
						m.HitsByK[k]++
					}
				}
			}
		}
		score(recM, recList)
		score(randM, unseen)
		score(popM, popSlugs)

		fold(ev)
	}
	return recM, randM, popM
}

// weightOverrides is bound to the -w flag in main; replay applies it before
// folding so the sweep harness can search the tuning space in one process.
var weightOverrides string

// refold reports whether replay recomputes weights from the currently-set
// package vars instead of the frozen per-row values (bound to -refold).
var refold bool

// refoldWeight rebuilds one event's weight from the CURRENT package weights
// so the sweep can ask "what if these values had been in force". Quiz
// repeats decay by QuizRepeatDecay^(n-1) capped at QuizRepeatDecayCap,
// mirroring the live grader (diminishing, never zero); backfill keeps its
// frozen value (its discount is part of the historical fact, not a tunable).
func refoldWeight(ev types.LearningEvent, quizSeen map[string]int) float64 {
	quizPrior := func(slug string) int {
		n := quizSeen[slug]
		quizSeen[slug]++
		if n > learning.QuizRepeatDecayCap {
			n = learning.QuizRepeatDecayCap
		}
		return n
	}
	switch ev.Type {
	case types.LearningEventAnswerCite:
		return learning.WeightAnswerCite
	case types.LearningEventCrossRef:
		return learning.WeightCrossRef
	case types.LearningEventReAsk:
		return learning.WeightReAsk
	case types.LearningEventTopicSignal, types.LearningEventWikiToolRead:
		return learning.WeightTopicSignal
	case types.LearningEventQuizCorrect:
		w := learning.WeightQuizCorrect
		for i := 0; i < quizPrior(ev.Slug); i++ {
			w *= learning.QuizRepeatDecay
		}
		return w
	case types.LearningEventQuizWrong:
		w := learning.WeightQuizWrong
		for i := 0; i < quizPrior(ev.Slug); i++ {
			w *= learning.QuizRepeatDecay
		}
		return w
	default:
		return ev.Weight
	}
}
