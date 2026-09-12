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
	Events    []types.LearningEvent       `json:"events"`
	Edges     []types.LearningEdge        `json:"edges,omitempty"` // legacy current graph: deliberately not used as history
	Attempts  []types.LearningQuizAttempt `json:"quiz_attempts,omitempty"`
	Snapshots []replaySnapshot            `json:"recommendation_snapshots,omitempty"`
}

type replayScope struct {
	Tenant      uint64
	Subject, KB string
}
type replaySnapshot struct {
	TenantID        uint64    `json:"tenant_id"`
	SubjectID       string    `json:"subject_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	At              time.Time `json:"at"`
	// Input contains the production metadata known at At (pages, active quiz
	// eligibility, edges, material order, skips, affinity). State and direct
	// facts are always reconstructed from past events/attempts, never trusted.
	Input learning.BenchRecommendInput `json:"input"`
}

func eventScope(e types.LearningEvent) replayScope {
	return replayScope{e.TenantID, e.SubjectID, e.KnowledgeBaseID}
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

func loadReplayInput(path string) (replayInput, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return replayInput{}, err
	}
	var in replayInput
	if err = json.Unmarshal(raw, &in); err != nil {
		return in, err
	}
	if len(in.Events) == 0 {
		var wrapped struct {
			Data replayInput `json:"data"`
		}
		if err = json.Unmarshal(raw, &wrapped); err != nil {
			return in, err
		}
		in = wrapped.Data
	}
	if len(in.Events) == 0 {
		return in, fmt.Errorf("export contains no events")
	}
	return in, nil
}

func runReplay(exportPath, topKStr string) error {
	applyWeightOverrides(weightOverrides)
	input, err := loadReplayInput(exportPath)
	if err != nil {
		return err
	}
	ks := []int{5, 10}
	if topKStr != "" {
		ks = nil
		for _, v := range strings.Split(topKStr, ",") {
			k, e := strconv.Atoi(strings.TrimSpace(v))
			if e != nil || k <= 0 {
				return fmt.Errorf("invalid top-k %q", v)
			}
			ks = append(ks, k)
		}
	}
	groups := map[replayScope][]types.LearningEvent{}
	ignored := 0
	for _, e := range input.Events {
		if e.Type == types.LearningEventAgentRead {
			ignored++
			continue
		}
		if e.TenantID == 0 || e.SubjectID == "" || e.KnowledgeBaseID == "" || e.Slug == "" || e.ID == "" || e.OccurredAt.IsZero() {
			return fmt.Errorf("replay requires full scope, event id, slug and timestamp")
		}
		groups[eventScope(e)] = append(groups[eventScope(e)], e)
	}
	scopes := make([]replayScope, 0, len(groups))
	for scope := range groups {
		scopes = append(scopes, scope)
	}
	sort.Slice(scopes, func(i, j int) bool {
		a, b := scopes[i], scopes[j]
		if a.Tenant != b.Tenant {
			return a.Tenant < b.Tenant
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.KB < b.KB
	})
	totals := []*benchMetrics{newBenchMetrics("recommender"), newBenchMetrics("random"), newBenchMetrics("popularity")}
	evaluated, skipped, unreachable, snapshotsUsed := 0, 0, 0, 0
	macro := make([]float64, 3)
	for n, scope := range scopes {
		events := orderedReplayEvents(groups[scope])
		split := replaySplit(events)
		if split <= 0 || split >= len(events) {
			skipped++
			continue
		}
		cutoff := events[split].OccurredAt
		state := buildReplayState(input, events[:split], scope, cutoff)
		truths := []string{}
		seen := map[string]bool{}
		for _, e := range events[split:] {
			e.Slug = replayTargetSlug(state, e)
			if types.IsHumanLearningEvent(e.Type) && !seen[e.Slug] {
				truths = append(truths, e.Slug)
				seen[e.Slug] = true
			}
		}
		if len(truths) == 0 {
			skipped++
			continue
		}
		lists := rankReplayState(state, cutoff, maxOf(ks), 42)
		sm := make([]*benchMetrics, 3)
		for i, name := range []string{"recommender", "random", "popularity"} {
			fixed := lists[i]
			sm[i] = evalOne(name, func() []string { return fixed }, truths, ks)
		}
		online := evaluateOnlineReplay(input, events, split, ks)
		fmt.Printf("\n  scope %d: tenant=%d kb=%s; events=%d; static targets=%d; online targets=%d\n", n+1, scope.Tenant, scope.KB, len(events), len(truths), online.metrics[0].Predictions)
		printReplayMetrics("static (one frozen list)", sm, ks)
		printReplayMetrics("online (predict whole equal-time batch before observation)", online.metrics, ks)
		for i, m := range online.metrics {
			totals[i].Predictions += m.Predictions
			totals[i].MRRSum += m.MRRSum
			for k, v := range m.HitsByK {
				totals[i].HitsByK[k] += v
			}
			macro[i] += m.mrr()
		}
		unreachable += online.unreachable
		snapshotsUsed += online.snapshotsUsed
		evaluated++
	}
	if evaluated == 0 {
		return fmt.Errorf("no scope has separate prefix and human holdout time groups")
	}
	printReplayMetrics("online aggregate (micro)", totals, ks)
	fmt.Printf("  macro MRR across %d scopes: recommender=%.4f random=%.4f popularity=%.4f\n", evaluated, macro[0]/float64(evaluated), macro[1]/float64(evaluated), macro[2]/float64(evaluated))
	fmt.Printf("  skipped scopes=%d; agent traces excluded=%d; unreachable human targets=%d/%d; predictions with historical metadata=%d/%d\n", skipped, ignored, unreachable, totals[0].Predictions, snapshotsUsed, totals[0].Predictions)
	fmt.Println("  Protocol: full-scope isolation; (occurred_at,id) order; strict past visibility including created_at when available; no equal-time feedback; independent seeded baselines; current exported edges ignored.")
	fmt.Println("  Without historical recommendation snapshots this is an observed-node proxy, not production-equivalent guidance. Missing quiz attempts cannot establish direct mastery facts. Single-seed descriptive metrics do not establish statistical significance or learning gains.")
	// An experiment remains valid when a baseline wins. Never turn a losing
	// holdout result into an instruction to tune on that same holdout.
	return nil
}

func printReplayMetrics(title string, ms []*benchMetrics, ks []int) {
	fmt.Printf("  [%s]\n", title)
	for _, m := range ms {
		fmt.Printf("  %-14s", m.Label)
		for _, k := range ks {
			fmt.Printf(" HR@%d=%.4f", k, m.hitRate(k))
		}
		fmt.Printf(" MRR=%.4f n=%d\n", m.mrr(), m.Predictions)
	}
}

// evalOne freezes one ranking before scoring its held-out targets.
func evalOne(label string, rank func() []string, groundTruths []string, ks []int) *benchMetrics {
	m := newBenchMetrics(label)
	ranked := rank()
	for _, gt := range groundTruths {
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

func orderedReplayEvents(events []types.LearningEvent) []types.LearningEvent {
	out := append([]types.LearningEvent{}, events...)
	for i := range out {
		if out[i].OriginalSlug != "" {
			out[i].Slug = out[i].OriginalSlug
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].OccurredAt.Before(out[j].OccurredAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}
func replaySplit(events []types.LearningEvent) int {
	if len(events) < 2 {
		return 0
	}
	split := int(float64(len(events)) * .8)
	for split > 0 && events[split-1].OccurredAt.Equal(events[split].OccurredAt) {
		split--
	}
	return split
}

type replayState struct {
	in         learning.BenchRecommendInput
	popularity map[string]int
	historical bool
	aliases    map[string]string
}

func buildReplayState(input replayInput, history []types.LearningEvent, scope replayScope, now time.Time) replayState {
	result := replayState{popularity: map[string]int{}}
	var latest time.Time
	for _, snap := range input.Snapshots {
		if (replayScope{snap.TenantID, snap.SubjectID, snap.KnowledgeBaseID}) != scope || snap.At.IsZero() || !snap.At.Before(now) {
			continue
		}
		if latest.IsZero() || snap.At.After(latest) {
			result.in = snap.Input
			latest = snap.At
			result.historical = true
		}
	}
	result.in.States = map[string]learning.FoldState{}
	result.in.HasQuiz = cloneQuizFlags(result.in.HasQuiz)
	if !result.historical {
		result.in.Affinity = map[string]bool{}
	}
	aliases := learning.BenchCanonicalAliases(result.in.Pages)
	result.aliases = aliases
	slugs := map[string]bool{}
	visible := []types.LearningEvent{}
	for _, e := range orderedReplayEvents(history) {
		if eventScope(e) != scope || e.Type == types.LearningEventAgentRead || !e.OccurredAt.Before(now) || (!e.CreatedAt.IsZero() && !e.CreatedAt.Before(now)) {
			continue
		}
		if to := aliases[e.Slug]; to != "" {
			e.Slug = to
		}
		visible = append(visible, e)
		w := e.Weight
		if refold {
			w = refoldWeight(e, nil)
		}
		result.in.States[e.Slug] = learning.FoldEvent(result.in.States[e.Slug], learning.Event{ID: e.ID, Type: e.Type, Weight: w, OccurredAt: e.OccurredAt})
		if types.IsHumanLearningEvent(e.Type) {
			slugs[e.Slug] = true
			result.popularity[e.Slug]++
			if !result.historical {
				result.in.Affinity[e.Slug] = true
			}
		}
		if !result.historical && (e.Type == types.LearningEventQuizCorrect || e.Type == types.LearningEventQuizWrong) {
			result.in.HasQuiz[e.Slug] = true
		}
	}
	if !result.historical {
		order := []string{}
		for slug := range slugs {
			order = append(order, slug)
		}
		sort.Strings(order)
		for _, slug := range order {
			kind := "concept"
			if strings.HasPrefix(slug, "entity/") {
				kind = "entity"
			}
			result.in.Pages = append(result.in.Pages, &types.WikiPage{Slug: slug, Title: slug, PageType: kind})
		}
	}
	attempts := []types.LearningQuizAttempt{}
	for _, a := range input.Attempts {
		if a.TenantID == scope.Tenant && a.SubjectID == scope.Subject && a.KnowledgeBaseID == scope.KB && a.AnsweredAt.Before(now) && (a.CreatedAt.IsZero() || a.CreatedAt.Before(now)) {
			if a.OriginalSlug != "" {
				a.Slug = a.OriginalSlug
			}
			if to := aliases[a.Slug]; to != "" {
				a.Slug = to
			}
			attempts = append(attempts, a)
		}
	}
	result.in = learning.BenchHistoryInput(result.in, visible, attempts, now)
	// Apply scope and inventory validity before ANY baseline sees candidates.
	pages := []*types.WikiPage{}
	for _, p := range result.in.Pages {
		if p != nil && p.Slug != "" && (p.PageType == "concept" || p.PageType == "entity") && !result.in.Skips[p.Slug] {
			pages = append(pages, p)
		}
	}
	result.in.Pages = pages
	edges := []types.LearningEdge{}
	for _, edge := range result.in.Edges {
		if edge.TenantID == scope.Tenant && edge.KnowledgeBaseID == scope.KB {
			edges = append(edges, edge)
		}
	}
	result.in.Edges = edges
	return result
}

// Targets use only aliases available to this prediction, just like its evidence.
func replayTargetSlug(state replayState, e types.LearningEvent) string {
	slug := e.Slug
	if e.OriginalSlug != "" {
		slug = e.OriginalSlug
	}
	if target := state.aliases[slug]; target != "" {
		return target
	}
	return slug
}
func cloneQuizFlags(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func rankReplayState(state replayState, now time.Time, k int, seed int64) [3][]string {
	var lists [3][]string
	recs := learning.BenchRecommendNodes(state.in, now, rand.New(rand.NewSource(seed)), k)
	for _, rec := range recs {
		lists[0] = append(lists[0], rec.Slug)
	}
	pool := []string{}
	seen := map[string]bool{}
	for _, page := range state.in.Pages {
		if !seen[page.Slug] {
			pool = append(pool, page.Slug)
			seen[page.Slug] = true
		}
	}
	sort.Strings(pool)
	lists[1] = append([]string{}, pool...)
	rand.New(rand.NewSource(seed+1000003)).Shuffle(len(lists[1]), func(i, j int) { lists[1][i], lists[1][j] = lists[1][j], lists[1][i] })
	lists[2] = append([]string{}, pool...)
	sort.Slice(lists[2], func(i, j int) bool {
		a, b := lists[2][i], lists[2][j]
		if state.popularity[a] != state.popularity[b] {
			return state.popularity[a] > state.popularity[b]
		}
		return a < b
	})
	for i := range lists {
		if len(lists[i]) > k {
			lists[i] = lists[i][:k]
		}
	}
	return lists
}

type onlineReplayResult struct {
	metrics                    []*benchMetrics
	unreachable, snapshotsUsed int
}

func evaluateOnlineReplay(input replayInput, events []types.LearningEvent, split int, ks []int) onlineReplayResult {
	result := onlineReplayResult{metrics: []*benchMetrics{newBenchMetrics("recommender"), newBenchMetrics("random"), newBenchMetrics("popularity")}}
	if len(events) == 0 {
		return result
	}
	// Equal timestamp cohorts see the same prefix, even if a caller split one.
	for split > 0 && split < len(events) && events[split-1].OccurredAt.Equal(events[split].OccurredAt) {
		split--
	}
	for i := split; i < len(events); {
		end := i + 1
		for end < len(events) && events[end].OccurredAt.Equal(events[i].OccurredAt) {
			end++
		}
		scope := eventScope(events[i])
		state := buildReplayState(input, events[:i], scope, events[i].OccurredAt)
		lists := rankReplayState(state, events[i].OccurredAt, maxOf(ks), 42+events[i].OccurredAt.UnixNano())
		known := map[string]bool{}
		for _, p := range state.in.Pages {
			known[p.Slug] = true
		}
		for _, e := range events[i:end] {
			e.Slug = replayTargetSlug(state, e)
			if !types.IsHumanLearningEvent(e.Type) {
				continue
			}
			if !known[e.Slug] {
				result.unreachable++
			}
			if state.historical {
				result.snapshotsUsed++
			}
			for r := range lists {
				fixed := lists[r]
				m := evalOne(result.metrics[r].Label, func() []string { return fixed }, []string{e.Slug}, ks)
				result.metrics[r].Predictions += m.Predictions
				result.metrics[r].MRRSum += m.MRRSum
				for k, v := range m.HitsByK {
					result.metrics[r].HitsByK[k] += v
				}
			}
		}
		i = end
	}
	return result
}

// Legacy test seam evaluates each scope independently; current edges are not
// historical metadata and cannot be injected into past prediction inputs.
func runOnlineReplay(events []types.LearningEvent, splitIdx int, ks []int, _ []types.LearningEdge) (*benchMetrics, *benchMetrics, *benchMetrics) {
	groups := map[replayScope][]types.LearningEvent{}
	prefix := map[replayScope]int{}
	for i, e := range events {
		if e.Type == types.LearningEventAgentRead {
			continue
		}
		key := eventScope(e)
		groups[key] = append(groups[key], e)
		if i < splitIdx {
			prefix[key]++
		}
	}
	sums := []*benchMetrics{newBenchMetrics("recommender"), newBenchMetrics("random"), newBenchMetrics("popularity")}
	for scope, group := range groups {
		r := evaluateOnlineReplay(replayInput{}, orderedReplayEvents(group), prefix[scope], ks)
		for i, m := range r.metrics {
			sums[i].Predictions += m.Predictions
			sums[i].MRRSum += m.MRRSum
			for k, v := range m.HitsByK {
				sums[i].HitsByK[k] += v
			}
		}
	}
	return sums[0], sums[1], sums[2]
}

// weightOverrides is bound to the -w flag in main; replay applies it before
// folding so the sweep harness can search the tuning space in one process.
var weightOverrides string

// refold reports whether replay recomputes weights from the currently-set
// package vars instead of the frozen per-row values (bound to -refold).
var refold bool

// Capture the shipped table before CLI overrides or fitting mutate it.
var shippedEventWeights = currentEventWeights()

func currentEventWeights() map[string]float64 {
	return map[string]float64{
		types.LearningEventAnswerCite:   learning.WeightAnswerCite,
		types.LearningEventCrossRef:     learning.WeightCrossRef,
		types.LearningEventReAsk:        learning.WeightReAsk,
		types.LearningEventTopicSignal:  learning.WeightTopicSignal,
		types.LearningEventWikiToolRead: learning.WeightTopicSignal,
		types.LearningEventAgentRead:    0, // tool access is trace, not learning
		types.LearningEventWikiDeepRead: learning.WeightWikiDeepRead,
		types.LearningEventQuizCorrect:  learning.WeightQuizCorrect,
		types.LearningEventQuizWrong:    learning.WeightQuizWrong,
	}
}

// Preserve the recorded eligibility/repeat discount and scale only the
// base event weight. A legacy event has no item ID: a per-slug lifetime
// count cannot reconstruct the online same-item, 48-hour rule.
func rescaleEventWeight(ev types.LearningEvent, baseline map[string]float64) float64 {
	if ev.Weight == 0 {
		return 0
	}
	if len(baseline) == 0 {
		baseline = shippedEventWeights
	}
	old, known := baseline[ev.Type]
	current, tunable := currentEventWeights()[ev.Type]
	if !known || !tunable || old == 0 {
		return ev.Weight
	}
	return ev.Weight * current / old
}

func refoldWeight(ev types.LearningEvent, _ map[string]int) float64 {
	return rescaleEventWeight(ev, nil)
}
