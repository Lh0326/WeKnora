package learning

import (
	"math"
)

// FoldEvent applies one piece of evidence to a fold state. The rule set is
// deliberately tiny: add the frozen weight to the logit inside the clamp,
// bump the evidence counters by sign, grow stability only on positive
// evidence, and advance the seen-bounds. Positive evidence folds
// commutatively; NEGATIVE evidence is time-aware (see the absorption step
// below), so faithful replay means folding the event stream in order —
// the same ordered stream always converges to the same state.
func FoldEvent(state FoldState, event Event) FoldState {
	if event.Weight == 0 {
		return state
	}
	at := event.OccurredAt

	// 时间感知负更新（评审 P1-D）：一次长期搁置后的答错/复问是对
	// "当前已衰减的能力"的测量，不是对历史峰值的扣分。先把已发生的
	// 遗忘吸收进 logit（z ← logit(σ(z)·R(Δt,S))），再叠加负权重——
	// 否则旧高 logit 原样保留、时间锚又被推到现在，p_eff 反而跳升
	// （反例：三次答对后搁置 180 天答错，0.557 → 0.924）。正事件不
	// 吸收：延迟答对本身即保持证据，且普通重读不得侵蚀已验证状态。
	if event.Weight < 0 && !state.LastEvidenceAt.IsZero() && !at.IsZero() && at.After(state.LastEvidenceAt) {
		days := at.Sub(state.LastEvidenceAt).Hours() / 24
		p := 1 / (1 + math.Exp(-state.Logit))
		pEff := p * Retrievability(days, state.Stability)
		if pEff < 1e-9 {
			pEff = 1e-9
		}
		if pEff > 1-1e-9 {
			pEff = 1 - 1e-9
		}
		state.Logit = math.Log(pEff / (1 - pEff))
	}

	// Decay can take the pre-answer probability below sigmoid(LogitFloor).
	// Clamping a negative update back UP to that floor would reward failure.
	floor := LogitFloor
	if event.Weight < 0 {
		floor = math.Min(floor, state.Logit)
	}
	state.Logit += event.Weight
	if state.Logit > LogitCap {
		state.Logit = LogitCap
	}
	if state.Logit < floor {
		state.Logit = floor
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
