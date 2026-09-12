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
	// SelfAssess carries the subject's latest self-assessment per slug,
	// already bounded to the visible window by the caller. The freshest
	// explicit user claim outranks every INFERRED signal when attributing
	// the reason: a "quiz too easy" demotion followed by a 错题重练 label
	// (built from stale wrong answers the user just re-contextualised) is
	// the system ignoring the user — the self-verify channel exists so the
	// recommendation says "you claimed a state; confirm it" instead.
	SelfAssess map[string]interfaces.SelfAssessMark
	// DirectFacts carries the subject's distinct-item correct answers per
	// slug, so the pool's tier decisions (frontier readiness, dam
	// detection, consolidation) see the same gated tiers the display
	// shows — pool and card can never disagree.
	DirectFacts map[string][]DirectQuizFact
	// DocOrder carries each node's position in the source material
	// (0 = introduced earliest), the 从浅入深 channel. A cold start has no
	// behavioural signal to break ties with, and the title-alphabetical
	// fallback would happily offer chapter 2 before the chapter 1 nodes it
	// silently assumes — document order is the only shallow-to-deep order
	// the corpus itself states.
	DocOrder map[string]int
	// Skips carries the subject's standing "已掌握，不再推荐" declarations:
	// queue suppression the user authored. A skipped node never appears in
	// any channel — not as a frontier candidate, not as a dam to shore up,
	// not as an exploration pick — and a skipped PREREQUISITE reads as
	// satisfied for the frontier gate (the user claims the knowledge; the
	// gate must not brick the nodes behind it forever).
	Skips map[string]bool
	// Recent carries the subject's recent STRONG-behaviour nodes (quiz
	// answers, deliberate page reads, answer citations — passive topic
	// mappings excluded by the caller), newest first, bounded to a few
	// anchors. The continuity channel (承上启下) rewards candidates that
	// extend them.
	Recent []RecentNode
	// LastVisit carries per slug the newest USER-visit timestamp (≥5s
	// deliberate page read, deep read, Q&A touch or re-ask — the read-tier
	// shield means a quick flip lands nothing here). The pressure pins
	// (self-verify, remedial) step aside once the user has actually visited
	// the node: 顺承只看"到过"，深度自愿加码. Nil map = no visit data
	// (tests, degraded reads) — pins keep their pre-existing behaviour.
	LastVisit map[string]time.Time
	// Material carries each node's source-material context (chapter label,
	// home document) — the continuity same-chapter test and the narrative
	// both read it. Nil map = no material (tests, degraded reads).
	Material map[string]NodeMaterial
}

// RecentNode is one recent strong-behaviour learning anchor.
type RecentNode struct {
	Slug string
	At   time.Time
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
	// ScoreFoundation: one of the earliest never-touched nodes in document
	// order — the 从浅入深 on-ramp. Sits between blind-spot and affinity on
	// purpose: it must lift the earliest material above equal-score blind
	// spots, but never above a genuine personal signal (affinity, review,
	// remedial). The frontier gate already keeps a learner off advanced
	// nodes while prerequisites are unmet; this nudges them toward the
	// front of the book when nothing else speaks.
	ScoreFoundation = 0.3
	// FoundationWindow is how many of the earliest never-touched nodes (in
	// document order) carry the foundation bonus at once. Five matches the
	// default card limit, so a cold start's entire first list reads in
	// source-material order instead of only its head.
	FoundationWindow = 5
	// ExploreWindow bounds ε-exploration: the random pick happens among the
	// earliest never-served ready nodes only, never the whole book — the
	// filter-bubble guard must not hand a day-one learner a chapter-20
	// card (document order is a ranking hint, not a gate; the bound keeps
	// exploration compatible with it).
	ExploreWindow = 12
	// ScoreContinue: the 承上启下 channel — the candidate directly extends
	// what the learner just studied (prerequisite successor of a recent
	// node, or a same-document same-chapter neighbour). Above affinity:
	// sequencing is the user's explicit ask and trumps generic interest.
	// Below review-due: forgetting stays the most time-sensitive signal.
	ScoreContinue = 1.2
	// ContinuityWindow bounds how old a "recently learned" node may be and
	// still anchor the continuity channel; RecentFreshHours inside it
	// earns the full bonus, older anchors decay to half.
	ContinuityWindow = 72 * time.Hour
	RecentFreshHours = 24 * time.Hour
	// ScoreChainLink / ScoreChainSection: the visible-list chaining bonus.
	// When the previously picked card directly prepares a candidate (edge)
	// or sits in the same document chapter, the candidate steps forward so
	// the top-5 reads as one path, not five parallel facts.
	ScoreChainLink    = 0.3
	ScoreChainSection = 0.2
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
		return gatedAnchoredLevel(state(slug), in.DirectFacts[slug], now).Level
	}

	pageBySlug := map[string]*types.WikiPage{}
	for _, p := range in.Pages {
		if p != nil {
			pageBySlug[p.Slug] = p
		}
	}
	// Prerequisites per node: node's edge froms. Two storage anomalies are
	// filtered here rather than trusted: a self-edge (A prepares A) is a
	// tautology that would gate A forever, and an edge whose prerequisite
	// page no longer exists (deleted or renamed away — the reconciler heals
	// the table, the reader must not brick in the meantime) can never be
	// satisfied by learning.
	pres := map[string][]string{}
	for _, e := range in.Edges {
		if e.Relation != types.LearningEdgePrerequisite {
			continue
		}
		if e.FromSlug == e.ToSlug || pageBySlug[e.FromSlug] == nil || pageBySlug[e.ToSlug] == nil {
			continue
		}
		pres[e.ToSlug] = append(pres[e.ToSlug], e.FromSlug)
	}
	// Cycle guard: an adjudicated prerequisite cycle (A prepares B, B
	// prepares A) is unsatisfiable — every member waits on another member,
	// so readyNode would never open and the whole component would vanish
	// from every channel with no shore-up fallback (unseen nodes cannot be
	// dams). The honest degradation is to serve cycle members as if ready:
	// the learner sees the nodes, learns them, and the cycle stops mattering.
	// (The edge write path refuses to mint new cycles; this defends reads
	// against legacy and hand-migrated rows.)
	cyclic := map[string]bool{}
	var reaches func(start, cur string, seen map[string]bool) bool
	reaches = func(start, cur string, seen map[string]bool) bool {
		for _, next := range pres[cur] {
			if next == start {
				return true
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			if reaches(start, next, seen) {
				return true
			}
		}
		return false
	}
	for slug := range pres {
		if reaches(slug, slug, map[string]bool{}) {
			cyclic[slug] = true
		}
	}
	// visitedWithin reports a real user visit (deliberate ≥5s read / Q&A
	// touch — quick flips record nothing) inside the given window: the
	// 首访让位 signal that releases the pressure pins.
	visitedWithin := func(slug string, window time.Duration) bool {
		if in.LastVisit == nil {
			return false
		}
		v, ok := in.LastVisit[slug]
		return ok && !v.After(now) && now.Sub(v) <= window
	}
	// The dam test: a prerequisite with real evidence that has sat below
	// the floor for longer than the re-ask window — actively-studied
	// prerequisites are learning, not blockage ("长期停在低掌握度").
	isDam := func(slug string, st FoldState) bool {
		return gatedAnchoredLevel(st, in.DirectFacts[slug], now).Level != LevelUnseen &&
			EffectiveP(st, now) < PrereqBlockedFloor &&
			st.EvidenceCount >= LowConfidenceEvidence &&
			!st.LastEvidenceAt.IsZero() &&
			now.Sub(st.LastEvidenceAt) > ReAskWindowHours*time.Hour
	}
	// The frontier gate: every prerequisite at least familiar. A skipped
	// prerequisite counts as satisfied — the user's declaration says they
	// bring the knowledge, and honouring anything less would leave the
	// nodes behind it permanently gated on a prerequisite the queue itself
	// refuses to serve. A node inside a prerequisite cycle reads as ready
	// for the same reason: an unsatisfiable constraint is a storage bug,
	// not a lesson plan.
	readyNode := func(slug string) bool {
		if cyclic[slug] {
			return true
		}
		for _, from := range pres[slug] {
			if in.Skips[from] {
				continue
			}
			if fl := levelOf(from); fl != LevelFamiliar && fl != LevelMastered {
				return false
			}
		}
		return true
	}

	// Document order (从浅入深): rank of each node's first appearance in
	// the source material; unresolvable positions read as "last".
	docRank := func(slug string) int {
		if r, ok := in.DocOrder[slug]; ok {
			return r
		}
		return math.MaxInt32
	}

	// The 承上启下 continuity anchors: for every recent strong-behaviour
	// node, the candidates it directly extends — prerequisite successors
	// (edge recent→candidate) and same-document same-chapter neighbours.
	// Weights: the newest anchor earns the full bonus (the "刚学完→下一
	// 个" thread), later anchors half; anchors older than the fresh hours
	// decay to half as well; a same-chapter neighbour earns 0.9× of a
	// prerequisite successor at equal weight — the directional edge is the
	// stronger 承上启下 claim and must win the tie. Same-document is part
	// of the same-chapter test on purpose: two documents can both carry a
	// "第2章", and only the document identity disambiguates them.
	type contHit struct {
		ref    string // the recent anchor's slug
		kind   string // "edge" | "section"
		weight float64
	}
	cont := map[string]contHit{}
	for i, r := range in.Recent {
		w := 1.0
		if now.Sub(r.At) > RecentFreshHours {
			w = 0.5
		}
		if i > 0 {
			w *= 0.5 // only the newest anchor is the mainline
		}
		if w <= 0 {
			continue
		}
		for _, e := range in.Edges {
			if e.Relation != types.LearningEdgePrerequisite || e.FromSlug != r.Slug || e.FromSlug == e.ToSlug {
				continue // a self-edge would let a node "continue itself"
			}
			if cur, ok := cont[e.ToSlug]; !ok || w > cur.weight {
				cont[e.ToSlug] = contHit{ref: r.Slug, kind: "edge", weight: w}
			}
		}
		rm := in.Material[r.Slug]
		if rm.Section == "" || rm.DocID == "" {
			continue
		}
		for _, p := range in.Pages {
			if p == nil || p.Slug == "" || p.Slug == r.Slug {
				continue
			}
			pm := in.Material[p.Slug]
			if pm.DocID != rm.DocID || pm.Section != rm.Section {
				continue
			}
			if cur, ok := cont[p.Slug]; !ok || w*0.9 > cur.weight {
				cont[p.Slug] = contHit{ref: r.Slug, kind: "section", weight: w * 0.9}
			}
		}
	}

	// The foundation window: the earliest never-touched nodes in document
	// order. Computed before the main loop so the bonus does not depend on
	// pool iteration order, and restricted to nodes that could actually be
	// served: mastered nodes never re-enter the list, skipped nodes must
	// not consume window slots the queue can never collect on, and nodes
	// gated behind unmet prerequisites cannot collect either.
	var neverTouched []string
	for _, p := range in.Pages {
		if p == nil || p.Slug == "" || (p.PageType != "entity" && p.PageType != "concept") {
			continue
		}
		if in.Skips[p.Slug] || !readyNode(p.Slug) {
			continue
		}
		if st := state(p.Slug); st.EvidenceCount == 0 && st.LastEvidenceAt.IsZero() {
			neverTouched = append(neverTouched, p.Slug)
		}
	}
	sort.Slice(neverTouched, func(i, j int) bool {
		if ri, rj := docRank(neverTouched[i]), docRank(neverTouched[j]); ri != rj {
			return ri < rj
		}
		return neverTouched[i] < neverTouched[j]
	})
	foundation := map[string]bool{}
	for i, slug := range neverTouched {
		if i >= FoundationWindow {
			break
		}
		foundation[slug] = true
	}

	type scored struct {
		rec      Recommendation
		score    float64
		remedial bool // mistake-notebook channel; ordered most-struggling-first
		// selfVerify: the user explicitly claimed this node's state inside
		// the visible window and the tier is not yet proven — the claim is
		// the reason, and the channel leads the list, freshest claim first.
		selfVerify bool
		assessAt   time.Time
		logit      float64
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
		// 承上启下: the candidate extends what was just learned. The score
		// always lands; the chip label only replaces generic ones (the
		// specialized channels keep their story), and the narrative keys
		// ride regardless so the card can say which thread it continues.
		why, whyRef := "", ""
		if hit, ok := cont[slug]; ok {
			score += ScoreContinue * hit.weight
			whyRef = hit.ref
			if hit.kind == "edge" {
				why = "continues_prereq"
			} else {
				why = "same_section"
			}
			// Relabel only when the continuity hit is a real mainline
			// thread (weight >= 0.5): a stale third-anchor brush (0.27)
			// must not mislabel the dominant foundation signal.
			if hit.weight >= 0.5 &&
				(reason == "frontier" || reason == "consolidate" || reason == "affinity" || reason == "foundation") {
				reason = "continue"
			}
		}
		// 巩固边缘: the two decay-aware consolidation signals. The review
		// channel is verified-only: a node the user merely browsed (never
		// quizzed, never claimed) decays out of recommendations entirely —
		// awareness-tier nodes live in the passive digest, not in the
		// queue. Nagging someone to "consolidate" knowledge they only ever
		// needed to see once is exactly the 度量焦虑 this policy removes.
		anchor, view := gatedAnchorAndView(st, in.DirectFacts[slug], now)
		_, hasAssess := in.SelfAssess[slug]
		verifiedIntent := in.QuizStruggled[slug] || len(in.DirectFacts[slug]) > 0 || hasAssess
		if levelRank(anchor) > levelRank(view) && levelRank(anchor) > 0 && verifiedIntent {
			// Earned knowledge fading below its tier: review is due.
			score += ScoreReviewDue
			reason = "review"
		} else if st.EvidenceCount >= LowConfidenceEvidence &&
			st.Logit < 0 && st.Logit > LogitRemedialFloor &&
			!visitedWithin(slug, ReAskWindowHours*time.Hour) {
			// 错题本频道: a real struggle history that is still recoverable
			// (negative but not floor-clamped) — the highest-value practice
			// target. The floor-clamped case is deliberately excluded: that
			// node is effectively unlearned again and belongs to the
			// fresh-start channels, not the drill queue. The reason splits
			// by evidence kind so the label never lies: "remedial" promises
			// an actual wrong answer (direct evidence); indirect struggle —
			// repeated re-asks where the answer never landed — gets its own
			// honest label instead of claiming a quiz failure that never
			// happened. 首访让位 also applies: a real visit inside the
			// re-ask window means the user just faced this node — the drill
			// pin steps aside and the card returns to ordinary ranking.
			score += ScoreRemedial
			remedial = true
			if in.QuizStruggled[slug] {
				reason = "remedial"
			} else {
				reason = "struggling"
			}
		} else if st.EvidenceCount == 0 {
			// 补盲区: never-touched frontier node.
			score += ScoreBlindSpot
			if reason == "consolidate" {
				reason = "frontier"
			}
			// 从浅入深: one of the earliest never-touched nodes in the
			// source material — the on-ramp a cold start should see first.
			// Structural labels (bypass, affinity) keep their own story;
			// the bonus coexists with a strong continue thread (both
			// signals are real), but the LABEL yields to the more specific
			// personal thread.
			if foundation[slug] {
				score += ScoreFoundation
				if reason == "frontier" {
					reason = "foundation"
				}
			}
		} else if !st.LastEvidenceAt.IsZero() && now.After(st.LastEvidenceAt) {
			days := now.Sub(st.LastEvidenceAt).Hours() / 24
			score += ScoreRecency * math.Exp(-days/RecencyHalfLifeDays) * EffectiveP(st, now)
		}
		// Self-verification channel: an explicit claim (up = "I know this
		// better", down = "less, because …") outranks every inferred label —
		// including the mistake notebook built from wrong answers the user
		// may have just re-contextualised ("题目太简单"). Once quiz proof
		// lands (tier reaches mastered for an up claim) the claim is settled
		// and the ordinary channels take over again. An up-claim only leads
		// while a proving ground EXISTS (active quiz items): pinning a
		// "please verify" card the user has no way to act on is the nag this
		// channel must never become — without items the node falls back to
		// its ordinary channels until the quiz pass generates them.
		var selfVerify bool
		var assessAt time.Time
		if mark, ok := in.SelfAssess[slug]; ok && levelOf(slug) != LevelMastered {
			// 首访让位: the pin holds only until the user actually visits
			// the node (a ≥5s deliberate read or Q&A touch AFTER the claim —
			// quick flips record nothing, so 误触 cannot disturb the path).
			// The claim's acknowledgement stays on the node (badge, in-page
			// verification entry); it just stops occupying the ① slot for
			// the rest of the window. Depth stays voluntary.
			visitedAfterClaim := false
			if v, ok2 := in.LastVisit[slug]; ok2 && v.After(mark.At) {
				visitedAfterClaim = true
			}
			if !visitedAfterClaim && (mark.Direction != "up" || in.HasQuiz[slug]) {
				selfVerify = true
				assessAt = mark.At
				reason = "self-verify"
			}
		}
		pool = append(pool, scored{rec: Recommendation{
			Slug: slug, Title: p.Title, Reason: reason, HasQuiz: in.HasQuiz[slug],
			Why: why, WhyRef: whyRef,
		}, score: score, remedial: remedial, selfVerify: selfVerify, assessAt: assessAt, logit: st.Logit})
	}

	// Dams are decided before the main loop so page order cannot matter: a
	// dam discovered via one blocked node must never slip into the frontier
	// pool as an ordinary candidate just because it was iterated first.
	// A skipped prerequisite never dams: the user retired it from the
	// queue, so shoring it up would be the exact nag the skip forbids.
	// Edges filtered out of `pres` (dangling endpoints) are skipped here
	// too — a dam whose page is gone cannot be shored, only removed.
	damSet := map[string]bool{}
	for _, e := range in.Edges {
		if e.Relation != types.LearningEdgePrerequisite {
			continue
		}
		if in.Skips[e.FromSlug] || pageBySlug[e.FromSlug] == nil || pageBySlug[e.ToSlug] == nil || e.FromSlug == e.ToSlug {
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
		// User-retired: out of every channel, mastered included.
		if in.Skips[p.Slug] {
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
				if in.Skips[neighbor] {
					continue // retired by the user: not a bypass target either
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

	// Ordering: the self-verification channel leads (an explicit claim is
	// a conversation the system must answer — freshest first), then the
	// mistake-notebook channel (drilling failed retrievals is the
	// highest-value practice), then the weighted multi-objective score (the
	// spec's ranking). Within the notebook, direct quiz failures come
	// before indirect struggle and order by severity (most negative logit
	// first); indirect struggle orders by closeness to promotion — the
	// desirable-difficulty zone, never the demoralizing end. Remaining
	// ties: least-evidence first (blind spots lead), then document order —
	// equal-signal nodes read in the order the source material introduces
	// them (从浅入深), the title-alphabetical fallback only when positions
	// are unresolvable. A deterministic total order.
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].selfVerify != pool[j].selfVerify {
			return pool[i].selfVerify
		}
		if pool[i].selfVerify {
			return pool[i].assessAt.After(pool[j].assessAt) // freshest claim first
		}
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
		if di, dj := docRank(pool[i].rec.Slug), docRank(pool[j].rec.Slug); di != dj {
			return di < dj // source-material order before alphabetical noise
		}
		if pool[i].rec.Title != pool[j].rec.Title {
			return pool[i].rec.Title < pool[j].rec.Title
		}
		return pool[i].rec.Slug < pool[j].rec.Slug
	})
	sort.SliceStable(shoreUp, func(i, j int) bool { return shoreUp[i].Slug < shoreUp[j].Slug })
	// The cap below shows only some dams per request; a static window would
	// starve the alphabetically-later ones forever (a dam never leaves the
	// set until its state changes). Rotate the window by calendar day so
	// every dam takes its turn across refreshes.
	if n := len(shoreUp); n > 1 {
		off := int(now.Unix()/86400) % n
		if off > 0 {
			shoreUp = append(append([]Recommendation{}, shoreUp[off:]...), shoreUp[:off]...)
		}
	}

	// Assembly order follows the documented channel priority: the self-
	// verification conversation leads (the sorted pool already places it
	// first), then the shore-up cards capped at half the slots — a pile of
	// stale prerequisites must not crowd out every user claim and the
	// mistake notebook — then the rest of the weighted pool.
	var out []Recommendation
	inOut := map[string]bool{}
	for _, s := range pool {
		if !s.selfVerify {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, s.rec)
		inOut[s.rec.Slug] = true
	}
	shoreCap := limit / 2
	if shoreCap < 1 {
		shoreCap = 1
	}
	for _, s := range shoreUp {
		if len(out) >= limit || shoreCap <= 0 {
			break
		}
		out = append(out, s)
		inOut[s.Slug] = true
		shoreCap--
	}
	// The remaining slots fill greedily with a chaining bonus toward the
	// previously picked card: a prerequisite successor of the last pick
	// (edge) or a same-document same-chapter neighbour steps forward, so
	// the visible list reads as one path (一步接一步) rather than five
	// parallel facts. Strict-greater comparison keeps the underlying
	// deterministic order on ties; anchors never chain (their own rule
	// ordered them) and the bonus only reorders, never rescored.
	chainBonus := func(candSlug, last string) float64 {
		if last == "" {
			return 0
		}
		for _, from := range pres[candSlug] {
			if from == last {
				return ScoreChainLink
			}
		}
		lm, cm := in.Material[last], in.Material[candSlug]
		if lm.DocID != "" && lm.DocID == cm.DocID && lm.Section != "" && lm.Section == cm.Section {
			return ScoreChainSection
		}
		return 0
	}
	remaining := make([]scored, 0, len(pool))
	for _, s := range pool {
		if !inOut[s.rec.Slug] {
			remaining = append(remaining, s)
		}
	}
	last := ""
	for len(out) < limit && len(remaining) > 0 {
		bestIx, bestVal := 0, math.Inf(-1)
		for i, c := range remaining {
			if v := c.score + chainBonus(c.rec.Slug, last); v > bestVal {
				bestVal, bestIx = v, i
			}
		}
		pick := remaining[bestIx]
		remaining = append(remaining[:bestIx], remaining[bestIx+1:]...)
		if bonus := chainBonus(pick.rec.Slug, last); bonus > 0 && pick.rec.Why == "" {
			pick.rec.Why = "continues_prev"
			pick.rec.WhyRef = last // the service layer translates the slug
		}
		out = append(out, pick.rec)
		inOut[pick.rec.Slug] = true
		last = pick.rec.Slug
	}

	// ε exploration: with the configured probability, the last slot goes to
	// a random unseen node instead — the filter-bubble guard. Candidates
	// are ready, unskipped, not-yet-SERVED unseen nodes (the pool holds
	// every candidate, so the exclusion is "not already on a card", not
	// "not already pooled"), and the draw happens among the earliest
	// ExploreWindow of them only: the dice roll can promote diversity
	// inside the on-ramp but never jump to the back of the book. It also
	// never displaces a pending self-verification claim: a dice roll must
	// not mute the conversation the system owes the user.
	if rng != nil && len(out) >= limit && rng.Float64() < RecommendEpsilon {
		if last := out[len(out)-1].Reason; last != "self-verify" && last != "prerequisite-stuck" {
			var blind []string
			for _, p := range in.Pages {
				if p == nil || p.Slug == "" || in.Skips[p.Slug] || inOut[p.Slug] {
					continue
				}
				if p.PageType != "entity" && p.PageType != "concept" {
					continue
				}
				if !readyNode(p.Slug) {
					continue // do not explore into a gated node either
				}
				st := state(p.Slug)
				if st.EvidenceCount == 0 && st.LastEvidenceAt.IsZero() {
					blind = append(blind, p.Slug) // never-touched only: faded != blind spot
				}
			}
			sort.Slice(blind, func(i, j int) bool {
				if ri, rj := docRank(blind[i]), docRank(blind[j]); ri != rj {
					return ri < rj
				}
				return blind[i] < blind[j]
			})
			if len(blind) > ExploreWindow {
				blind = blind[:ExploreWindow]
			}
			if len(blind) > 0 {
				pick := blind[rng.Intn(len(blind))]
				if sp := pageBySlug[pick]; sp != nil {
					out[len(out)-1] = Recommendation{Slug: pick, Title: sp.Title, Reason: "explore", HasQuiz: in.HasQuiz[pick]}
				}
			}
		}
	}
	return out
}
