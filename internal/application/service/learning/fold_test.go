package learning

import (
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var foldRef = time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

func cite(at time.Time) Event {
	return Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: at}
}

func reAsk(at time.Time) Event {
	return Event{Type: types.LearningEventReAsk, Weight: WeightReAsk, OccurredAt: at}
}

func quizCorrect(at time.Time) Event {
	return Event{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: at}
}

func TestFoldEventMonotonicRise(t *testing.T) {
	state := FoldState{}
	for i := 0; i < 4; i++ {
		state = FoldEvent(state, cite(foldRef.Add(time.Duration(i)*time.Hour)))
	}
	if state.Logit != 4*WeightAnswerCite {
		t.Fatalf("logit = %v, want %v", state.Logit, 4*WeightAnswerCite)
	}
	if state.PositiveCount != 4 || state.EvidenceCount != 4 || state.NegativeCount != 0 {
		t.Fatalf("counts = +%d/-%d/%d, want +4/-0/4", state.PositiveCount, state.NegativeCount, state.EvidenceCount)
	}
	// s = Base·(1+Growth·pos)^SatExp·LapseShrink^neg (see derivedStability)
	wantStability := derivedStability(4, 0)
	if math.Abs(state.Stability-wantStability) > 1e-9 {
		t.Fatalf("stability = %v, want %v", state.Stability, wantStability)
	}
	// Growth must be marginal-decreasing: the 4th repetition's increment is
	// smaller than the 1st's (the FSRS saturation property).
	firstHop := derivedStability(1, 0) - derivedStability(0, 0)
	fourthHop := derivedStability(4, 0) - derivedStability(3, 0)
	if fourthHop >= firstHop {
		t.Fatalf("stability growth not saturating: first hop %v, fourth hop %v", firstHop, fourthHop)
	}
	if !state.LastEvidenceAt.Equal(foldRef.Add(3 * time.Hour)) {
		t.Fatalf("last_evidence_at = %v, want %v", state.LastEvidenceAt, foldRef.Add(3*time.Hour))
	}
}

func TestFoldEventNegativeDrops(t *testing.T) {
	state := FoldEvent(FoldEvent(FoldState{}, cite(foldRef)), reAsk(foldRef.Add(time.Minute)))
	if math.Abs(state.Logit-(WeightAnswerCite+WeightReAsk)) > 1e-9 {
		t.Fatalf("logit = %v, want %v", state.Logit, WeightAnswerCite+WeightReAsk)
	}
	if state.PositiveCount != 1 || state.NegativeCount != 1 || state.EvidenceCount != 2 {
		t.Fatalf("counts = +%d/-%d/%d, want +1/-1/2", state.PositiveCount, state.NegativeCount, state.EvidenceCount)
	}
	// Negative evidence must not grow stability — it must shrink it while
	// retaining the prior (the FSRS post-lapse behaviour).
	if want := derivedStability(1, 1); math.Abs(state.Stability-want) > 1e-9 {
		t.Fatalf("stability = %v, want %v", state.Stability, want)
	}
	if state.Stability >= derivedStability(1, 0) {
		t.Fatal("a lapse must shrink stability below what the positives alone earned")
	}
	if state.Stability <= 0 {
		t.Fatal("a lapse must retain the prior, never zero the stability")
	}
}

func TestFoldEventFirstEvidenceInitialisesStabilityEvenWhenNegative(t *testing.T) {
	state := FoldEvent(FoldState{}, reAsk(foldRef))
	if want := derivedStability(0, 1); math.Abs(state.Stability-want) > 1e-9 {
		t.Fatalf("stability = %v, want %v on first negative evidence", state.Stability, want)
	}
	if state.Stability <= 0 {
		t.Fatal("first negative evidence must still leave decay a scale to work with")
	}
}

func TestFoldEventClampsAtCapAndFloor(t *testing.T) {
	state := FoldState{}
	for i := 0; i < 5; i++ { // 5×2.2 = 11 ≫ cap 4
		state = FoldEvent(state, quizCorrect(foldRef))
	}
	if state.Logit != LogitCap {
		t.Fatalf("logit = %v, want clamp %v", state.Logit, LogitCap)
	}

	state = FoldState{}
	for i := 0; i < 5; i++ { // 5×(-1.5) = -7.5 ≪ floor -4
		state = FoldEvent(state, Event{Type: types.LearningEventQuizWrong, Weight: WeightQuizWrong, OccurredAt: foldRef})
	}
	if state.Logit != LogitFloor {
		t.Fatalf("logit = %v, want clamp %v", state.Logit, LogitFloor)
	}
	// Evidence still counts even when the logit is pinned at a bound.
	if state.EvidenceCount != 5 {
		t.Fatalf("evidence_count = %d, want 5 (clamp must not swallow evidence)", state.EvidenceCount)
	}
}

func TestFoldEventOrderIndependentForReverseChronologicalInput(t *testing.T) {
	events := []Event{
		cite(foldRef),
		cite(foldRef.Add(1 * time.Hour)),
		reAsk(foldRef.Add(2 * time.Hour)),
		quizCorrect(foldRef.Add(3 * time.Hour)),
	}
	forward := FoldAll(FoldState{}, events)

	reversed := make([]Event, len(events))
	for i := range events {
		reversed[i] = events[len(events)-1-i]
	}
	backward := FoldAll(FoldState{}, reversed)

	if forward.Logit != backward.Logit ||
		forward.EvidenceCount != backward.EvidenceCount ||
		forward.PositiveCount != backward.PositiveCount ||
		forward.NegativeCount != backward.NegativeCount ||
		forward.Stability != backward.Stability {
		t.Fatalf("fold is not order-independent: forward %+v, reversed %+v", forward, backward)
	}
	if !forward.FirstSeenAt.Equal(backward.FirstSeenAt) || !forward.LastEvidenceAt.Equal(backward.LastEvidenceAt) {
		t.Fatalf("seen bounds differ: forward %v..%v, reversed %v..%v",
			forward.FirstSeenAt, forward.LastEvidenceAt, backward.FirstSeenAt, backward.LastEvidenceAt)
	}
}
