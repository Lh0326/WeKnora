package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// The four adversarial learner personas the gate must separate. These are
// the formalisation of the real-world complaint that motivated the gate:
// a shallow clicker and a lucky guesser used to reach "mastered" without
// learning anything.
//
//	shallow-clicker: N page reads, never a quiz  → capped at touched
//	lucky-guesser:   ONE correct four-way guess   → capped at familiar
//	same-item-farmer: one memorised item ×5       → capped at familiar
//	genuine-learner: 2 distinct items ≥48h apart   → mastered reachable
func TestDirectGateAdversarialPersonas(t *testing.T) {
	now := time.Now()

	reads := func(n int) FoldState {
		var st FoldState
		for i := 0; i < n; i++ {
			st = FoldEvent(st, Event{
				Type: types.LearningEventWikiToolRead, Weight: WeightTopicSignal,
				OccurredAt: now.Add(-time.Duration(n-i) * 49 * time.Hour),
			})
		}
		return st
	}
	oneCorrect := FoldAll(FoldState{}, []Event{quizEvent(WeightQuizCorrect, now)})
	farmFive := FoldAll(FoldState{}, []Event{
		quizEvent(WeightQuizCorrect, now),
		quizEvent(WeightQuizCorrect*QuizRepeatDecay, now),
		quizEvent(WeightQuizCorrect*QuizRepeatDecay*QuizRepeatDecay, now),
		quizEvent(WeightQuizCorrect*QuizRepeatDecay*QuizRepeatDecay, now),
		quizEvent(WeightQuizCorrect, now.Add(-72*time.Hour)), // window reset: full again
	})
	// Same item answered 5 times: only ONE distinct fact exists.
	farmFacts := []DirectQuizFact{{ItemID: "q1", FirstCorrectAt: now.Add(-72 * time.Hour)}}
	genuineFacts := []DirectQuizFact{
		{ItemID: "q1", FirstCorrectAt: now.Add(-5 * 24 * time.Hour)},
		{ItemID: "q2", FirstCorrectAt: now.Add(-1 * 24 * time.Hour)},
	}
	genuine := FoldAll(FoldState{}, []Event{
		quizEvent(WeightQuizCorrect, now.Add(-5*24*time.Hour)),
		quizEvent(WeightQuizCorrect, now.Add(-1*24*time.Hour)),
	})

	cases := []struct {
		name  string
		state FoldState
		facts []DirectQuizFact
		want  Level
	}{
		{"three page reads stay touched", reads(3), nil, LevelTouched},
		{"eight page reads still touched", reads(8), nil, LevelTouched},
		{"folded quiz without attempt rows degrades conservatively", oneCorrect, nil, LevelTouched},
		{"one lucky guess caps at familiar", oneCorrect,
			[]DirectQuizFact{{ItemID: "q1", FirstCorrectAt: now}}, LevelFamiliar},
		{"farming one item five times caps at familiar", farmFive, farmFacts, LevelFamiliar},
		{"two distinct items cross-session reach mastered", genuine, genuineFacts, LevelMastered},
		{"two items same sitting stay familiar", genuine, []DirectQuizFact{
			{ItemID: "q1", FirstCorrectAt: now.Add(-time.Hour)},
			{ItemID: "q2", FirstCorrectAt: now},
		}, LevelFamiliar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gatedAnchoredLevel(tc.state, tc.facts, now).Level
			if got != tc.want {
				t.Fatalf("gated level = %s, want %s", got, tc.want)
			}
		})
	}
}

func quizEvent(weight float64, at time.Time) Event {
	return Event{Type: types.LearningEventQuizCorrect, Weight: weight, OccurredAt: at}
}

// The gate must never block forgetting: a gated tier still decays down.
// This is the "cap, not floor" contract the passive channel relies on.
func TestDirectGateNeverBlocksDemotion(t *testing.T) {
	now := time.Now()
	// Mastered-grade evidence folded 300 days ago with the facts to match.
	state := FoldAll(FoldState{}, []Event{
		quizEvent(WeightQuizCorrect, now.Add(-300*24*time.Hour)),
		quizEvent(WeightQuizCorrect, now.Add(-299*24*time.Hour)),
	})
	facts := []DirectQuizFact{
		{ItemID: "q1", FirstCorrectAt: now.Add(-300 * 24 * time.Hour)},
		{ItemID: "q2", FirstCorrectAt: now.Add(-299 * 24 * time.Hour)},
	}
	if got := gatedAnchoredLevel(state, facts, now).Level; got == LevelMastered {
		t.Fatalf("300-day-idle mastered node must have decayed, got %s", got)
	}
	// And with the facts REMOVED (deleted attempts), the cap still applies
	// on top of the decay: never above familiar, never below what decay says.
	if got := gatedAnchoredLevel(state, nil, now).Level; got == LevelMastered || got == LevelFamiliar {
		t.Fatalf("no direct facts must cap the decayed tier to touched, got %s", got)
	}
}

func TestCollectDirectFactsDeduplicatesItems(t *testing.T) {
	now := time.Now()
	attempts := []types.LearningQuizAttempt{
		// q1 wrong first, then correct twice (later repeats keep the FIRST
		// correct time, not the earliest attempt).
		{Slug: "s", QuizItemID: "q1", IsCorrect: false, AnsweredAt: now.Add(-72 * time.Hour)},
		{Slug: "s", QuizItemID: "q1", IsCorrect: true, AnsweredAt: now.Add(-48 * time.Hour)},
		{Slug: "s", QuizItemID: "q1", IsCorrect: true, AnsweredAt: now.Add(-1 * time.Hour)},
		// q2 correct once; q3 only wrong → no fact.
		{Slug: "s", QuizItemID: "q2", IsCorrect: true, AnsweredAt: now.Add(-47 * time.Hour)},
		{Slug: "s", QuizItemID: "q3", IsCorrect: false, AnsweredAt: now},
		// A different slug entirely.
		{Slug: "other", QuizItemID: "q1", IsCorrect: true, AnsweredAt: now},
	}
	facts := CollectDirectFacts(attempts)
	if len(facts) != 2 {
		t.Fatalf("slugs with facts = %d, want 2 (s and other)", len(facts))
	}
	got := facts["s"]
	if len(got) != 2 {
		t.Fatalf("distinct facts = %d, want 2 (q1, q2)", len(got))
	}
	if got[0].ItemID != "q1" || !got[0].FirstCorrectAt.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("q1 fact = %+v, want first correct at -48h", got[0])
	}
	if got[1].ItemID != "q2" {
		t.Fatalf("facts sorted by time: got[1] = %+v, want q2", got[1])
	}
	// -48h → -47h straddles nothing; -48h → -1h does (gap 47h < 48h → no).
	if DirectGateTier(got, now) != LevelFamiliar {
		t.Fatalf("47h gap must not unlock mastered")
	}
	wide := []DirectQuizFact{
		{ItemID: "q1", FirstCorrectAt: now.Add(-49 * time.Hour)},
		{ItemID: "q2", FirstCorrectAt: now},
	}
	if DirectGateTier(wide, now) != LevelMastered {
		t.Fatalf("49h gap must unlock mastered")
	}
}

func TestTierProgressBands(t *testing.T) {
	cases := []struct {
		level Level
		pEff  float64
		want  float64
	}{
		{LevelUnseen, 0, 0},
		{LevelUnseen, LevelTouchedUp, 1},
		{LevelUnseen, LevelTouchedUp / 2, 0.5},
		{LevelTouched, LevelTouchedUp, 0},
		{LevelTouched, LevelFamiliarUp, 1},
		{LevelFamiliar, LevelFamiliarUp, 0},
		{LevelFamiliar, LevelMasteredUp, 1},
		{LevelMastered, LevelMasteredUp, 0},
		{LevelMastered, 1.0, 1},
		{LevelFamiliar, 0.7, 0.6},
		{LevelTouched, 0.9, 1}, // clamped
		{LevelUnseen, -0.2, 0}, // clamped
	}
	for _, tc := range cases {
		got := TierProgress(tc.level, tc.pEff)
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("TierProgress(%s, %.2f) = %.3f, want %.3f", tc.level, tc.pEff, got, tc.want)
		}
	}
}

func TestNextTierHintMatrix(t *testing.T) {
	now := time.Now()
	cases := []struct {
		level Level
		facts []DirectQuizFact
		want  string
	}{
		{LevelUnseen, nil, HintFirstTouch},
		{LevelTouched, nil, HintQuizUnlockFamiliar},
		{LevelTouched, []DirectQuizFact{{ItemID: "q1", FirstCorrectAt: now}}, HintGrowMastery},
		{LevelFamiliar, []DirectQuizFact{{ItemID: "q1", FirstCorrectAt: now}}, HintQuizUnlockMastered},
		{LevelFamiliar, []DirectQuizFact{ // 2 items, no gap
			{ItemID: "q1", FirstCorrectAt: now}, {ItemID: "q2", FirstCorrectAt: now.Add(-time.Hour)},
		}, HintQuizUnlockMastered},
		{LevelFamiliar, []DirectQuizFact{ // gap unlocked
			{ItemID: "q1", FirstCorrectAt: now.Add(-49 * time.Hour)}, {ItemID: "q2", FirstCorrectAt: now},
		}, HintGrowMastery},
		{LevelMastered, nil, HintKeepReviewing},
	}
	for _, tc := range cases {
		if got := NextTierHint(tc.level, tc.facts, now); got != tc.want {
			t.Errorf("NextTierHint(%s, %d facts) = %q, want %q", tc.level, len(tc.facts), got, tc.want)
		}
	}
}
