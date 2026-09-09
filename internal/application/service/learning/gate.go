package learning

import (
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// The direct-evidence gate. Every signal except a correct quiz answer is
// indirect evidence of mastery: a citation means the topic came up, a page
// read means the page was open — neither demonstrates the person can
// retrieve the knowledge. Before this gate the fold math let three page
// reads (3 × 0.5 = logit 1.5 → p 0.82) reach "mastered" and one lucky
// four-way guess (logit 2.2 → p 0.90) do the same, which made every tier
// above touched a statement about activity, not competence. The gate caps
// what indirect signals alone may display, mirroring Khan Academy's
// mastery-system rule (advance on demonstrated competence, not
// participation): tiers stay the slow, honest variable, while the
// tier-progress bar and next-tier hints (see TierProgress/NextTierHint)
// provide the fast positive feedback.
//
// The gate reads only the quiz attempt log — no new state, no migration:
// ListAttempts + CollectDirectFacts derive the facts at read time,
// so historical data re-grades the moment the code ships.

// DirectQuizFact is one distinct correctly-answered quiz item: the item id
// and when the subject FIRST answered it correctly. Re-answering the same
// item never creates a second fact — farming one memorised answer cannot
// fabricate breadth; only different items count.
type DirectQuizFact struct {
	ItemID string
	// FirstCorrectAt is the first time this item was answered correctly;
	// LastCorrectAt is the most recent correct answer (equal on a one-shot
	// item). The mastered gate reads the span earliest-first → latest-last:
	// a learner who answers the WHOLE item bank correctly on day 1 and
	// again after the session gap must qualify — firsts-only would lock
	// them out forever once every item's first correct is used up (the
	// audit's P1-C dead corner), rewarding "save one item for later"
	// instead of stable delayed retrieval.
	FirstCorrectAt time.Time
	LastCorrectAt  time.Time
}

type itemSpan struct {
	first time.Time
	last  time.Time
}

// IndependentQuizAttempts keeps attempts not exposed to feedback on the
// same item in the preceding 48 hours. Input includes wrong/unsure answers.
// Legacy rows without an item ID cannot be deduplicated reliably here.
func IndependentQuizAttempts(attempts []types.LearningQuizAttempt) []types.LearningQuizAttempt {
	ordered := append([]types.LearningQuizAttempt(nil), attempts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].AnsweredAt.Equal(ordered[j].AnsweredAt) {
			return ordered[i].AnsweredAt.Before(ordered[j].AnsweredAt)
		}
		// At indistinguishable timestamps, a failed/unsure attempt must not
		// be hidden by a correct retry whose answer may already be disclosed.
		if ordered[i].IsCorrect != ordered[j].IsCorrect {
			return !ordered[i].IsCorrect
		}
		return ordered[i].ID < ordered[j].ID
	})
	type attemptKey struct {
		tenant                  uint64
		kb, subject, slug, item string
	}
	lastAttempt := map[attemptKey]time.Time{}
	var independent []types.LearningQuizAttempt
	for _, a := range ordered {
		key := attemptKey{a.TenantID, a.KnowledgeBaseID, a.SubjectID, a.Slug, a.QuizItemID}
		previous, seen := lastAttempt[key]
		lastAttempt[key] = a.AnsweredAt
		if a.QuizItemID != "" && seen && a.AnsweredAt.Sub(previous) < ReAskWindowHours*time.Hour {
			continue
		}
		independent = append(independent, a)
	}
	return independent
}

// CollectDirectFacts reduces eligible correct attempts to distinct-item
// coverage and delayed verification. Callers supply the complete history.
func CollectDirectFacts(attempts []types.LearningQuizAttempt) map[string][]DirectQuizFact {
	spans := map[string]map[string]itemSpan{}
	for _, a := range IndependentQuizAttempts(attempts) {
		if !a.IsCorrect {
			continue
		}
		items := spans[a.Slug]
		if items == nil {
			items = map[string]itemSpan{}
			spans[a.Slug] = items
		}
		sp, ok := items[a.QuizItemID]
		if !ok {
			items[a.QuizItemID] = itemSpan{first: a.AnsweredAt, last: a.AnsweredAt}
			continue
		}
		if a.AnsweredAt.Before(sp.first) {
			sp.first = a.AnsweredAt
		}
		if a.AnsweredAt.After(sp.last) {
			sp.last = a.AnsweredAt
		}
		items[a.QuizItemID] = sp
	}
	out := make(map[string][]DirectQuizFact, len(spans))
	for slug, items := range spans {
		facts := make([]DirectQuizFact, 0, len(items))
		for id, sp := range items {
			facts = append(facts, DirectQuizFact{ItemID: id, FirstCorrectAt: sp.first, LastCorrectAt: sp.last})
		}
		sort.Slice(facts, func(i, j int) bool {
			if facts[i].FirstCorrectAt.Equal(facts[j].FirstCorrectAt) {
				return facts[i].ItemID < facts[j].ItemID
			}
			return facts[i].FirstCorrectAt.Before(facts[j].FirstCorrectAt)
		})
		out[slug] = facts
	}
	return out
}

// DirectGateTier is the highest tier the subject's direct evidence has
// unlocked for a node:
//
//	no correct item           → touched (indirect signals stop here)
//	≥1 distinct correct item  → familiar
//	≥2 distinct items whose correct answers (first→last span) straddle
//	  MasteredSessionGapHours → mastered (cross-session verification: the
//	  knowledge held across at least two separate sittings, the spacing
//	  effect as a promotion requirement rather than a bonus)
//
// `now` is accepted for symmetry with the decay-aware level path; facts
// never expire, only decay demotes.
func DirectGateTier(facts []DirectQuizFact, now time.Time) Level {
	effectiveLast := func(f DirectQuizFact) time.Time {
		if f.LastCorrectAt.IsZero() || f.LastCorrectAt.Before(f.FirstCorrectAt) {
			return f.FirstCorrectAt
		}
		return f.LastCorrectAt
	}
	switch {
	case len(facts) >= 2:
		earliest, latest := facts[0].FirstCorrectAt, effectiveLast(facts[0])
		for _, f := range facts[1:] {
			if f.FirstCorrectAt.Before(earliest) {
				earliest = f.FirstCorrectAt
			}
			if l := effectiveLast(f); l.After(latest) {
				latest = l
			}
		}
		if latest.Sub(earliest) >= MasteredSessionGap {
			return LevelMastered
		}
		return LevelFamiliar
	case len(facts) == 1:
		return LevelFamiliar
	default:
		return LevelTouched
	}
}

// ApplyDirectGate caps a derived level at what direct evidence unlocked.
// The cap only ever lowers a tier — it never raises one — and it never
// blocks demotion: a gated "familiar" that decays still falls to touched.
// The result therefore composes with the hysteresis bands as a ceiling,
// not a floor.
func ApplyDirectGate(level Level, facts []DirectQuizFact, now time.Time) Level {
	cap := DirectGateTier(facts, now)
	if levelRank(level) > levelRank(cap) {
		return cap
	}
	return level
}

// gatedAnchoredLevel is the level derivation every display and pool
// decision goes through: the undecayed anchor, the decayed view, then the
// direct-evidence ceiling. Callers that already hold the facts map should
// use this instead of anchoredLevel.
func gatedAnchoredLevel(state FoldState, facts []DirectQuizFact, now time.Time) LevelResult {
	lv := anchoredLevel(state, now)
	lv.Level = ApplyDirectGate(lv.Level, facts, now)
	return lv
}

// gatedAnchorAndView is anchorAndView with the gate applied to the anchor:
// indirect evidence never "earned" familiar/mastered, so decay cannot
// demote a node out of a tier the gate would not have granted — the
// passive-change channel (遗忘动态) and the review-due score stay honest.
func gatedAnchorAndView(state FoldState, facts []DirectQuizFact, now time.Time) (anchor, view Level) {
	anchor, view = anchorAndView(state, now)
	anchor = ApplyDirectGate(anchor, facts, now)
	if levelRank(view) > levelRank(anchor) {
		view = anchor // the view can never exceed the gated anchor
	}
	return anchor, view
}

// TierProgress is the fast feedback variable: where p_eff sits inside the
// node's current tier band, as a 0..1 fraction. A correct answer moves the
// bar immediately even when the (gated, slower) tier itself does not
// change — progress is continuous, promotion is earned.
func TierProgress(level Level, pEff float64) float64 {
	var lo, hi float64
	switch level {
	case LevelMastered:
		lo, hi = LevelMasteredUp, 1.0
	case LevelFamiliar:
		lo, hi = LevelFamiliarUp, LevelMasteredUp
	case LevelTouched:
		lo, hi = LevelTouchedUp, LevelFamiliarUp
	default: // unseen (incl. faded): everything below the lit gate counts
		lo, hi = 0, LevelTouchedUp
	}
	if hi <= lo {
		return 0
	}
	p := (pEff - lo) / (hi - lo)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// Next-tier hint keys: the transparent path to the next promotion. The
// gate's requirements are shown to the learner as an achievable goal
// rather than hidden arithmetic.
const (
	HintFirstTouch         = "first_touch"          // touch the node: read or ask
	HintQuizUnlockFamiliar = "quiz_unlock_familiar" // answer 1 new quiz item correctly
	HintQuizUnlockMastered = "quiz_unlock_mastered" // answer another item ≥48h later
	HintGrowMastery        = "grow_mastery"         // keep answering to raise p_eff
	HintKeepReviewing      = "keep_reviewing"       // follow the review schedule
)

// NextTierHint names the single most useful next action for a node, given
// its displayed tier and what the direct gate has unlocked. Deterministic
// from the same inputs as the tier itself.
func NextTierHint(level Level, facts []DirectQuizFact, now time.Time) string {
	switch level {
	case LevelUnseen:
		return HintFirstTouch
	case LevelTouched:
		if len(facts) == 0 {
			return HintQuizUnlockFamiliar
		}
		return HintGrowMastery // unlocked but decayed back: earn it again
	case LevelFamiliar:
		if DirectGateTier(facts, now) != LevelMastered {
			return HintQuizUnlockMastered
		}
		return HintGrowMastery
	default: // mastered
		return HintKeepReviewing
	}
}
