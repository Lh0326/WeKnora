package learning

import (
	"math"
)

// FoldEvent applies one piece of evidence to a fold state. The rule set is
// deliberately tiny: add the frozen weight to the logit inside the clamp,
// bump the evidence counters by sign, grow stability only on positive
// evidence, and advance the seen-bounds. Because addition and counting are
// commutative, folding is order-independent for logit and counters; the
// timestamp bounds take min/max so any input order converges to the same
// state.
func FoldEvent(state FoldState, event Event) FoldState {
	at := event.OccurredAt

	state.Logit += event.Weight
	if state.Logit > LogitCap {
		state.Logit = LogitCap
	}
	if state.Logit < LogitFloor {
		state.Logit = LogitFloor
	}

	state.EvidenceCount++
	if event.Weight > 0 {
		state.PositiveCount++
	} else if event.Weight < 0 {
		state.NegativeCount++
	}

	// Stability is derived from the folded counters, never accumulated
	// ad hoc: positive evidence grows it with marginal decrease (each
	// repetition adds less than the last — the FSRS S^−w9 saturation in
	// count space), negative evidence shrinks it while retaining the prior
	// (the FSRS post-lapse behaviour: a lapse means the memory was weaker
	// than believed, not that it never existed). Because both terms are
	// pure functions of the counters, folding stays order-independent.
	state.Stability = derivedStability(state.PositiveCount, state.NegativeCount)
	if state.Stability <= 0 {
		state.Stability = StabilityBaseDays
	}

	// Seen-bounds converge regardless of the order events arrive in.
	if state.FirstSeenAt.IsZero() || (!at.IsZero() && at.Before(state.FirstSeenAt)) {
		state.FirstSeenAt = at
	}
	if !at.IsZero() && at.After(state.LastEvidenceAt) {
		state.LastEvidenceAt = at
	}
	return state
}

// derivedStability is the FSRS-shaped stability as a pure function of the
// evidence counters:
//
//	S = Base · (1 + Growth·pos)^SatExp · LapseShrink^neg
//
// The power SatExp < 1 makes growth marginal-decreasing (the 10th touch
// adds far less than the 1st), the lapse factor shrinks stability on
// negative evidence while retaining the prior (never zeroing it), and the
// whole expression stays commutative in event order — the fold's core
// replay guarantee. With the power-law decay curve, S reads as "days
// until retrievability falls to 90%".
func derivedStability(pos, neg int) float64 {
	s := StabilityBaseDays *
		math.Pow(1+StabilityGrowth*float64(pos), StabilitySaturationExp) *
		math.Pow(StabilityLapseShrink, float64(neg))
	if s < 1 {
		s = 1
	}
	return s
}
