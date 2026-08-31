package learning

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Recommendation is the interfaces type; aliased for terse use here.
type Recommendation = interfaces.Recommendation

// recommendInput is everything the rule-based recommender needs; it is a
// plain value so the whole policy is table-testable without I/O.
type recommendInput struct {
	Pages    []*types.WikiPage    // entity/concept pages of the KB
	Edges    []types.LearningEdge // prerequisite edges of the KB
	States   map[string]FoldState // the subject's folds, keyed by slug
	Affinity map[string]bool      // slugs whose pages cite the subject's familiar docs
	HasQuiz  map[string]bool      // slugs with at least one active item
	// QuizStruggled marks slugs where THIS subject answered at least one
	// quiz item wrong — direct negative evidence, the strongest remedial
	// signal (the spec's hierarchy: quiz verdicts outrank every indirect
	// signal).
	QuizStruggled map[string]bool
}

// anchoredLevel derives a node's display level the way every read path
// does: an undecayed anchor tier (what the accumulated evidence says) and
// then the decayed view held against the hysteresis bands.
func anchoredLevel(state FoldState, now time.Time) LevelResult {
	// Zero evidence means no row was ever folded for this node: unseen by
	// definition, not by arithmetic (a zero logit would read sigmoid(0)=
	// 0.5, which is a statement nobody earned).
	if state.EvidenceCount == 0 && state.LastEvidenceAt.IsZero() {
		return LevelResult{Level: LevelUnseen, LowConfidence: true}
	}
	anchor := LevelOf(1/(1+math.Exp(-state.Logit)), state.EvidenceCount, LevelUnseen).Level
	return LevelOf(EffectiveP(state, now), state.EvidenceCount, anchor)
}

// anchorAndView returns both tiers of anchoredLevel: what the evidence
// says ignoring decay (anchor) and what decay has worn it down to (view).
// The gap between them is the review signal: anchor above view means the
// person once earned a tier that forgetting is eating — the spaced-
// repetition "due" state.
func anchorAndView(state FoldState, now time.Time) (anchor, view Level) {
	if state.EvidenceCount == 0 && state.LastEvidenceAt.IsZero() {
		return LevelUnseen, LevelUnseen
	}
	anchor = LevelOf(1/(1+math.Exp(-state.Logit)), state.EvidenceCount, LevelUnseen).Level
	view = LevelOf(EffectiveP(state, now), state.EvidenceCount, anchor).Level
	return anchor, view
}

// Multi-objective scores (spec §2.5: 补盲区/巩固边缘/拓展兴趣, plus the
// decay-driven review dimension). All deterministic, all sweepable via
// learning-bench replay; production never assigns them.
var (
	// ScoreAffinity: the node's pages cite documents this person keeps
	// working from — the 拓展 interest objective.
	ScoreAffinity = 1.0
	// ScoreReviewDue: the anchor tier sits above the decayed view — earned
	// knowledge is fading, which is the single most time-sensitive thing to
	// do next (the FSRS due-queue idea).
	ScoreReviewDue = 1.6
	// ScoreBlindSpot: a never-touched node on the frontier — the 补盲区
	// objective.
	ScoreBlindSpot = 0.5
	// ScoreRecency: continuing a fresh, successful thread ("resume where
	// you left off"); scaled by exp(-Δdays/RecencyHalfLifeDays) and by the
	// current p_eff so a thread the learner is failing does not get a
	// "continue" nudge (failing nodes surface through consolidation or the
	// stuck-prereq shoring instead).
	ScoreRecency        = 0.6
	RecencyHalfLifeDays = 2.0
	// ScoreBypass: the wiki-link neighbour of a dam-blocked node — the
	// spec's 绕行 (bypass) candidate, similar content without the dam.
	ScoreBypass = 0.2
	// ScoreRemedial: the mistake-notebook channel. A node with a real
	// struggle history (≥3 evidence, negative but recoverable logit) is
	// the highest-value practice target — the testing-effect /
	// hypercorrection literature says failed retrievals followed by
	// feedback are where learning happens, and every learning product
	// resurfaces them. Below LogitRemedialFloor (p < ~5%) the node reads
	// as a fresh start instead: drilling what was effectively never
	// learned is re-learning, not remediation.
	ScoreRemedial = 0.9
	// LogitRemedialFloor is the recoverable-struggle boundary for the
	// mistake notebook (sigmoid(-3) ≈ 5%).
	LogitRemedialFloor = -3.0
)

// recommendNodes is the "look" half of guidance: the outer frontier of the
// knowledge space — nodes not yet mastered whose prerequisites are all at
// least familiar — ranked by the weighted multi-objective score, plus the
// blocked-prerequisite shoring list, the spec's neighbour bypass, and one
// ε exploration slot for an unseen blind spot.
func recommendNodes(in recommendInput, now time.Time, rng *rand.Rand, limit int) []Recommendation {
	if limit <= 0 {
		limit = 5
	}

	state := func(slug string) FoldState { return in.States[slug] }
	levelOf := func(slug string) Level {
		return anchoredLevel(state(slug), now).Level
	}

	// Prerequisites per node: node's edge froms.
	pres := map[string][]string{}
	for _, e := range in.Edges {
		if e.Relation == types.LearningEdgePrerequisite {
			pres[e.ToSlug] = append(pres[e.ToSlug], e.FromSlug)
		}
	}
	pageBySlug := map[string]*types.WikiPage{}
	for _, p := range in.Pages {
		if p != nil {
			pageBySlug[p.Slug] = p
		}
	}
	// The dam test: a prerequisite with real evidence that has sat below
	// the floor for longer than the re-ask window — actively-studied
	// prerequisites are learning, not blockage ("长期停在低掌握度").
	isDam := func(slug string, st FoldState) bool {
		return anchoredLevel(st, now).Level != LevelUnseen &&
			EffectiveP(st, now) < PrereqBlockedFloor &&
			st.EvidenceCount >= LowConfidenceEvidence &&
			!st.LastEvidenceAt.IsZero() &&
			now.Sub(st.LastEvidenceAt) > ReAskWindowHours*time.Hour
	}
	// The frontier gate: every prerequisite at least familiar.
	readyNode := func(slug string) bool {
		for _, from := range pres[slug] {
			if fl := levelOf(from); fl != LevelFamiliar && fl != LevelMastered {
				return false
			}
		}
		return true
	}

	type scored struct {
		rec      Recommendation
		score    float64
		remedial bool // mistake-notebook channel; ordered most-struggling-first
		logit    float64
	}
	var pool []scored
	var shoreUp []Recommendation
	seen := map[string]bool{}

	addToPool := func(slug string, bonus float64, reason string) {
		if seen[slug] {
			return
		}
		p := pageBySlug[slug]
		if p == nil || p.Slug == "" || (p.PageType != "entity" && p.PageType != "concept") {
			return
		}
		seen[slug] = true

		st := state(slug)
		score := 1.0 + bonus
		remedial := false
		// 拓展兴趣: relevance to the person's working documents.
		if in.Affinity[slug] {
			score += ScoreAffinity
			if reason == "frontier" || reason == "consolidate" {
				reason = "affinity"
			}
		}
		// 巩固边缘: the two decay-aware consolidation signals.
		anchor, view := anchorAndView(st, now)
		if levelRank(anchor) > levelRank(view) && levelRank(anchor) > 0 {
			// Earned knowledge fading below its tier: review is due.
			score += ScoreReviewDue
			reason = "review"
		} else if st.EvidenceCount >= LowConfidenceEvidence &&
			st.Logit < 0 && st.Logit > LogitRemedialFloor {
			// 错题重练: a real struggle history that is still recoverable
			// (negative but not floor-clamped) — the highest-value
			// practice target. The floor-clamped case is deliberately
			// excluded: that node is effectively unlearned again and
			// belongs to the fresh-start channels, not the drill queue.
			score += ScoreRemedial
			remedial = true
			reason = "remedial"
		} else if st.EvidenceCount == 0 {
			// 补盲区: never-touched frontier node.
			score += ScoreBlindSpot
			if reason == "consolidate" {
				reason = "frontier"
			}
		} else if !st.LastEvidenceAt.IsZero() && now.After(st.LastEvidenceAt) {
			days := now.Sub(st.LastEvidenceAt).Hours() / 24
			score += ScoreRecency * math.Exp(-days/RecencyHalfLifeDays) * EffectiveP(st, now)
		}
		pool = append(pool, scored{rec: Recommendation{
			Slug: slug, Title: p.Title, Reason: reason, HasQuiz: in.HasQuiz[slug],
		}, score: score, remedial: remedial, logit: st.Logit})
	}

	// Dams are decided before the main loop so page order cannot matter: a
	// dam discovered via one blocked node must never slip into the frontier
	// pool as an ordinary candidate just because it was iterated first.
	damSet := map[string]bool{}
	for _, e := range in.Edges {
		if e.Relation != types.LearningEdgePrerequisite {
			continue
		}
		if fs, ok := in.States[e.FromSlug]; ok && isDam(e.FromSlug, fs) {
			damSet[e.FromSlug] = true
		}
	}

	for _, p := range in.Pages {
		if p == nil || p.Slug == "" {
			continue
		}
		if p.PageType != "entity" && p.PageType != "concept" {
			continue
		}
		if levelOf(p.Slug) == LevelMastered {
			continue
		}

		// A dam surfaces as the shore-up card, never as a frontier node:
		// the blocked nodes behind it will point here.
		if damSet[p.Slug] {
			if !seen[p.Slug] {
				shoreUp = append(shoreUp, Recommendation{
					Slug: p.Slug, Title: p.Title,
					Reason: "prerequisite-stuck", HasQuiz: in.HasQuiz[p.Slug],
				})
				seen[p.Slug] = true
			}
			continue
		}

		// A node behind a dam waits; exploration keeps moving through its
		// wiki-link neighbours (the spec's 绕行 bypass).
		stuck := ""
		for _, from := range pres[p.Slug] {
			if damSet[from] {
				stuck = from
				break
			}
		}
		if stuck != "" {
			for _, neighbor := range p.OutLinks {
				if neighbor == p.Slug || damSet[neighbor] || pageBySlug[neighbor] == nil {
					continue
				}
				if nl := levelOf(neighbor); nl == LevelMastered {
					continue
				}
				if !readyNode(neighbor) {
					continue // do not bypass into another gated node
				}
				addToPool(neighbor, ScoreBypass, "bypass")
			}
			continue // the node behind the dam waits
		}

		// Outer frontier: not mastered with all prerequisites ≥ familiar.
		if !readyNode(p.Slug) {
			continue
		}
		reason := "frontier"
		if lv := levelOf(p.Slug); lv == LevelTouched || lv == LevelFamiliar {
			reason = "consolidate"
		}
		addToPool(p.Slug, 0, reason)
	}

	// Ordering: the mistake-notebook channel leads (drilling failed
	// retrievals is the highest-value practice), then the weighted
	// multi-objective score (the spec's ranking). Within the notebook,
	// direct quiz failures come before indirect struggle and order by
	// severity (most negative logit first); indirect struggle orders by
	// closeness to promotion — the desirable-difficulty zone, never the
	// demoralizing end. Remaining ties: least-evidence first (blind spots
	// lead), then title — a deterministic total order.
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].remedial != pool[j].remedial {
			return pool[i].remedial
		}
		if pool[i].score != pool[j].score {
			return pool[i].score > pool[j].score
		}
		qi, qj := in.QuizStruggled[pool[i].rec.Slug], in.QuizStruggled[pool[j].rec.Slug]
		if qi != qj {
			return qi
		}
		if pool[i].logit != pool[j].logit {
			if qi {
				return pool[i].logit < pool[j].logit // drill the weakest direct failure first
			}
			return pool[i].logit > pool[j].logit // harvest the winnable: closest to promotion
		}
		si := in.States[pool[i].rec.Slug]
		sj := in.States[pool[j].rec.Slug]
		if si.EvidenceCount != sj.EvidenceCount {
			return si.EvidenceCount < sj.EvidenceCount
		}
		if pool[i].rec.Title != pool[j].rec.Title {
			return pool[i].rec.Title < pool[j].rec.Title
		}
		return pool[i].rec.Slug < pool[j].rec.Slug
	})
	sort.SliceStable(shoreUp, func(i, j int) bool { return shoreUp[i].Slug < shoreUp[j].Slug })

	var out []Recommendation
	for _, s := range shoreUp {
		if len(out) >= limit {
			break
		}
		out = append(out, s)
	}
	for _, s := range pool {
		if len(out) >= limit {
			break
		}
		out = append(out, s.rec)
	}

	// ε exploration: with the configured probability, the last slot goes to
	// a random unseen node instead — the filter-bubble guard.
	if rng != nil && len(out) >= limit && rng.Float64() < RecommendEpsilon {
		var blind []string
		for _, p := range in.Pages {
			if p != nil && p.Slug != "" && !seen[p.Slug] && levelOf(p.Slug) == LevelUnseen {
				blind = append(blind, p.Slug)
			}
		}
		if len(blind) > 0 {
			pick := blind[rng.Intn(len(blind))]
			if sp := pageBySlug[pick]; sp != nil {
				out[len(out)-1] = Recommendation{Slug: pick, Title: sp.Title, Reason: "explore", HasQuiz: in.HasQuiz[pick]}
			}
		}
	}
	return out
}
