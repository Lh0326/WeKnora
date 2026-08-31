package learning

import (
	"math"
	"testing"
	"time"
)

func TestEffectivePNoDecayAtZeroDistance(t *testing.T) {
	state := FoldAll(FoldState{}, []Event{cite(foldRef)})
	got := EffectiveP(state, foldRef) // now == last evidence
	want := 1 / (1 + math.Exp(-WeightAnswerCite))
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("p_eff = %v, want undecayed sigmoid %v", got, want)
	}
	// Even a "now" before the last evidence must not inflate the value.
	if got := EffectiveP(state, foldRef.Add(-time.Hour)); math.Abs(got-want) > 1e-12 {
		t.Fatalf("p_eff before last evidence = %v, want %v", got, want)
	}
}

func TestEffectivePDecaysMonotonicallyWhenIdle(t *testing.T) {
	state := FoldAll(FoldState{}, []Event{cite(foldRef)})
	prev := EffectiveP(state, foldRef)
	for d := 1; d <= 60; d += 7 {
		got := EffectiveP(state, foldRef.Add(time.Duration(d)*24*time.Hour))
		if got >= prev {
			t.Fatalf("p_eff not decreasing: day %d gives %v, previous %v", d, got, prev)
		}
		prev = got
	}
	// A month idle on a fresh node must be a visible but not total fade.
	month := EffectiveP(state, foldRef.Add(30*24*time.Hour))
	if month <= 0 || month >= EffectiveP(state, foldRef) {
		t.Fatalf("30-day idle p_eff = %v, want inside (0, undecayed)", month)
	}
}

func TestEffectivePStabilityGrowthSlowsDecay(t *testing.T) {
	// One positive evidence vs five: same logit would be ideal, but five
	// citations also raise the logit; compare the decay *factor* instead.
	fresh := FoldAll(FoldState{}, []Event{cite(foldRef)})
	settled := FoldAll(FoldState{}, []Event{
		cite(foldRef), cite(foldRef), cite(foldRef), cite(foldRef), cite(foldRef),
	})
	idle := 30 * 24 * time.Hour
	freshP := EffectiveP(fresh, foldRef.Add(idle)) / (1 / (1 + math.Exp(-fresh.Logit)))
	settledP := EffectiveP(settled, foldRef.Add(idle)) / (1 / (1 + math.Exp(-settled.Logit)))
	if settledP <= freshP {
		t.Fatalf("settled node decay factor %v should exceed fresh %v after the same idle time", settledP, freshP)
	}
}

// TestNextReviewDaysRoundTrip: the scheduled horizon is exactly when the
// power-law decayed p_eff crosses the tier's demotion threshold — folding
// the horizon back into EffectiveP must land on the threshold.
func TestNextReviewDaysRoundTrip(t *testing.T) {
	state := FoldAll(FoldState{}, []Event{cite(foldRef), cite(foldRef), quizCorrect(foldRef)})
	threshold := LevelMasteredDown // 0.70
	days := NextReviewDays(state, foldRef, threshold)
	if days == nil || *days <= 0 {
		t.Fatalf("horizon = %v, want positive", days)
	}
	at := EffectiveP(state, foldRef.Add(time.Duration(*days*24*float64(time.Hour))))
	if math.Abs(at-threshold) > 1e-6 {
		t.Fatalf("p_eff at horizon = %v, want threshold %v", at, threshold)
	}
	// Below-threshold state or unseen tier schedules nothing.
	sunk := FoldAll(FoldState{}, []Event{{Type: "quiz_wrong", Weight: WeightQuizWrong, OccurredAt: foldRef}})
	if NextReviewDays(sunk, foldRef, LevelTouchedDown) != nil {
		t.Fatal("below-threshold state must not schedule a future review")
	}
	if NextReviewDays(FoldState{}, foldRef, LevelTouchedDown) != nil {
		t.Fatal("unseen state must not schedule a future review")
	}
}

// TestPowerLawDecaySlowerThanExponential: with S reading as "days to 90%
// retrievability", a mastered node must hold its tier for weeks, not days —
// the calibration the plain exponential got wrong.
func TestPowerLawDecaySlowerThanExponential(t *testing.T) {
	state := FoldAll(FoldState{}, []Event{
		cite(foldRef), cite(foldRef), cite(foldRef), cite(foldRef),
	})
	p0 := 1 / (1 + math.Exp(-state.Logit))
	after20d := EffectiveP(state, foldRef.Add(20*24*time.Hour)) / p0
	if after20d < 0.8 {
		t.Fatalf("20-day retention factor = %v, want ≥0.8 at stability %v", after20d, state.Stability)
	}
}
