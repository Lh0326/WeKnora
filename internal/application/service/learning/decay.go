package learning

import (
	"math"
	"time"
)

// The forgetting curve is the FSRS-4.5 power law rather than a plain
// exponential:
//
//	R(t, S) = (1 + FACTOR · t/S) ^ DECAY,   DECAY = −0.5, FACTOR = 19/81
//
// With this shape S carries the FSRS semantics verbatim: the number of
// days until retrievability falls to 90%. A plain exponential with the
// same S would hit the level thresholds within days — power-law forgetting
// is both the memory-research fit (Cepeda et al. 2006 meta-analysis) and
// the correctly calibrated one for the tier bands.
const (
	DecayFactor   = 19.0 / 81.0
	DecayExponent = -0.5
)

// Retrievability is the fraction of the undecayed probability that
// survives after the given days at the given stability.
func Retrievability(days, stability float64) float64 {
	if days <= 0 {
		return 1
	}
	if stability <= 0 {
		stability = StabilityBaseDays
	}
	return math.Pow(1+DecayFactor*days/stability, DecayExponent)
}

// EffectiveP is the read-time decayed mastery probability:
//
//	p_eff = sigmoid(logit) · Retrievability(Δdays, stability)
//
// Decay is computed lazily at read time and never persisted, so there is no
// background decay job and no write amplification: an idle node fades in the
// view the moment it is looked at, and stability (grown by FoldEvent from
// positive evidence, shrunk by negative evidence) is what makes
// well-established nodes fade slower — the FSRS idea in one line rather
// than the full three-component machinery.
func EffectiveP(state FoldState, now time.Time) float64 {
	p := 1 / (1 + math.Exp(-state.Logit))
	if state.LastEvidenceAt.IsZero() || !now.After(state.LastEvidenceAt) {
		return p
	}
	days := now.Sub(state.LastEvidenceAt).Hours() / 24
	return p * Retrievability(days, state.Stability)
}

// NextReviewDays solves for how many days the freshly folded state stays
// above the tier's demotion threshold — the deterministic review
// schedule ("下次复习约在 N 天后"). Returns nil when the state is already
// at or below the threshold (review now) or the tier is unseen (nothing
// to retain yet).
func NextReviewDays(state FoldState, now time.Time, threshold float64) *float64 {
	if state.LastEvidenceAt.IsZero() || state.EvidenceCount == 0 || threshold <= 0 {
		return nil
	}
	p0 := 1 / (1 + math.Exp(-state.Logit))
	if p0 <= threshold {
		return nil
	}
	// p0 · R(t, S) = threshold  →  t = S/FACTOR · ((p0/threshold)^(−1/DECAY) − 1)
	stability := state.Stability
	if stability <= 0 {
		stability = StabilityBaseDays
	}
	days := stability / DecayFactor * (math.Pow(p0/threshold, -1/DecayExponent) - 1)
	if days < 0 {
		days = 0
	}
	if days > 365 {
		days = 365
	}
	return &days
}
