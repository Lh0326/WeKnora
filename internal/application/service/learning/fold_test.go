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
	// The minute of idle before the re-ask absorbs a hair of decay into the
	// logit first (time-aware negative updates), so the exact sum is no
	// longer bit-equal — only near-gap events converge to it.
	if math.Abs(state.Logit-(WeightAnswerCite+WeightReAsk)) > 1e-3 {
		t.Fatalf("logit = %v, want ≈ %v", state.Logit, WeightAnswerCite+WeightReAsk)
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

// TestFoldOrderedReplayIsDeterministic：折叠的保证是"相同有序事件流
// 必然重放出相同状态"（后台重放与在线路径的一致性基础），而不再是任意
// 顺序无关——钳位截断本就破坏了交换律（评审 P2-A），且负事件现在显式
// 时间感知（P1-D）：先遗忘后扣分的语义只对按时间排序的流成立。
func TestFoldOrderedReplayIsDeterministic(t *testing.T) {
	events := []Event{
		cite(foldRef),
		cite(foldRef.Add(1 * time.Hour)),
		reAsk(foldRef.Add(2 * time.Hour)),
		quizCorrect(foldRef.Add(3 * time.Hour)),
	}
	first := FoldAll(FoldState{}, events)
	second := FoldAll(FoldState{}, append([]Event(nil), events...))
	if first != second {
		t.Fatalf("same ordered stream must replay identically: %+v vs %+v", first, second)
	}
	// 正事件流（无负证据）仍保持交换不变；负事件按到达序吸收遗忘。
	posOnly := []Event{cite(foldRef), quizCorrect(foldRef.Add(time.Hour))}
	a := FoldAll(FoldState{}, posOnly)
	b := FoldAll(FoldState{}, []Event{posOnly[1], posOnly[0]})
	if a.Logit != b.Logit || a.PositiveCount != b.PositiveCount {
		t.Fatalf("positive-only stream stays commutative: %+v vs %+v", a, b)
	}
}

// TestFoldNegativeEventAfterLongIdleDropsProbability（评审 P1-D 回归）：
// 三次答对后搁置 180 天再答错——掌握度必须下降，而不是因"旧峰值保留 +
// 时间锚重置"反弹上升（旧实现 p_eff 0.557 → 0.924）。
func TestFoldNegativeEventAfterLongIdleDropsProbability(t *testing.T) {
	now := time.Now()
	state := FoldState{}
	for i := 0; i < 3; i++ {
		state = FoldEvent(state, Event{Type: "quiz_correct", Weight: WeightQuizCorrect, OccurredAt: now.Add(-181 * 24 * time.Hour)})
	}
	before := EffectiveP(state, now) // Compare both states at the same observation time.
	state = FoldEvent(state, Event{Type: "quiz_wrong", Weight: WeightQuizWrong, OccurredAt: now})
	after := EffectiveP(state, now)
	if after >= before {
		t.Fatalf("a wrong answer after 180 idle days must lower p_eff: before=%.4f after=%.4f", before, after)
	}
	if after > 0.5 {
		t.Fatalf("post-lapse probability must sit well below the familiar band, got %.4f", after)
	}
	// 对照：紧接着的第二次答错继续下降（同日负事件按扣分走，无额外吸收）。
	second := FoldEvent(state, Event{Type: "quiz_wrong", Weight: WeightQuizWrong, OccurredAt: now})
	if p2 := EffectiveP(second, now); p2 >= after {
		t.Fatalf("repeated wrong answers keep descending: %.4f -> %.4f", after, p2)
	}
}
