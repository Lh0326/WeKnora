package learning

import "testing"

func TestLevelOfPromotesAtUpThresholds(t *testing.T) {
	cases := []struct {
		name string
		pEff float64
		prev Level
		want Level
	}{
		{"below touched-up stays unseen", 0.299, LevelUnseen, LevelUnseen},
		{"at touched-up promotes", 0.30, LevelUnseen, LevelTouched},
		{"between touched and familiar holds touched", 0.54, LevelTouched, LevelTouched},
		{"at familiar-up promotes", 0.55, LevelTouched, LevelFamiliar},
		{"at mastered-up promotes multi-tier from unseen", 0.80, LevelUnseen, LevelMastered},
		{"empty prev defaults to unseen band", 0.31, "", LevelTouched},
	}
	for _, tc := range cases {
		if got := LevelOf(tc.pEff, 5, tc.prev).Level; got != tc.want {
			t.Errorf("%s: LevelOf(%v, %q) = %q, want %q", tc.name, tc.pEff, tc.prev, got, tc.want)
		}
	}
}

func TestLevelOfDemotesOnlyBelowDownThresholds(t *testing.T) {
	cases := []struct {
		name string
		pEff float64
		prev Level
		want Level
	}{
		{"mastered holds just above its down gate", 0.70, LevelMastered, LevelMastered},
		{"mastered demotes just below its down gate", 0.699, LevelMastered, LevelFamiliar},
		{"familiar holds inside its band", 0.46, LevelFamiliar, LevelFamiliar},
		{"familiar demotes below its down gate", 0.449, LevelFamiliar, LevelTouched},
		{"touched demotes below its down gate", 0.219, LevelTouched, LevelUnseen},
		{"deep drop walks tiers down in one call", 0.10, LevelMastered, LevelUnseen},
	}
	for _, tc := range cases {
		if got := LevelOf(tc.pEff, 5, tc.prev).Level; got != tc.want {
			t.Errorf("%s: LevelOf(%v, %q) = %q, want %q", tc.name, tc.pEff, tc.prev, got, tc.want)
		}
	}
}

func TestLevelOfHysteresisAbsorbsBandJitter(t *testing.T) {
	// p_eff oscillates inside the touched band [0.22, 0.30): the level must
	// stay whatever it already was, in both directions, forever.
	for _, prev := range []Level{LevelUnseen, LevelTouched} {
		for _, pEff := range []float64{0.22, 0.26, 0.2999} {
			if got := LevelOf(pEff, 5, prev).Level; got != prev {
				t.Errorf("jitter %v with prev %q flapped to %q", pEff, prev, got)
			}
		}
	}
	// Only crossing the band edges moves the level.
	if got := LevelOf(0.2199, 5, LevelTouched).Level; got != LevelUnseen {
		t.Errorf("below band should demote, got %q", got)
	}
	if got := LevelOf(0.30, 5, LevelUnseen).Level; got != LevelTouched {
		t.Errorf("pEff 0.30 from unseen should promote to touched, got %q", got)
	}
	if got := LevelOf(0.55, 5, LevelTouched).Level; got != LevelFamiliar {
		t.Errorf("pEff 0.55 from touched should promote to familiar, got %q", got)
	}
}

func TestLevelOfLowConfidenceFlag(t *testing.T) {
	cases := []struct {
		evidence int
		want     bool
	}{{0, true}, {1, true}, {2, true}, {3, false}, {9, false}}
	for _, tc := range cases {
		if got := LevelOf(0.9, tc.evidence, LevelUnseen).LowConfidence; got != tc.want {
			t.Errorf("evidence=%d lowConfidence=%v, want %v", tc.evidence, got, tc.want)
		}
	}
}
