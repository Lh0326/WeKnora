package learning

import (
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestGradeQuizCorrectAndWrong(t *testing.T) {
	got, err := GradeQuiz("B", "B", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Correct || got.EventType != types.LearningEventQuizCorrect || got.Weight != WeightQuizCorrect {
		t.Fatalf("correct answer graded %+v", got)
	}

	got, err = GradeQuiz("B", "D", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Correct || got.EventType != types.LearningEventQuizWrong || got.Weight != WeightQuizWrong {
		t.Fatalf("wrong answer graded %+v", got)
	}
}

func TestGradeQuizRepeatAttemptsDecay(t *testing.T) {
	cases := []struct {
		prior  int
		factor float64
	}{{0, 1}, {1, QuizRepeatDecay}, {2, QuizRepeatDecay * QuizRepeatDecay}}
	for _, tc := range cases {
		got, err := GradeQuiz("A", "A", tc.prior)
		if err != nil {
			t.Fatalf("prior=%d: %v", tc.prior, err)
		}
		if want := WeightQuizCorrect * tc.factor; math.Abs(got.Weight-want) > 1e-12 {
			t.Errorf("prior=%d weight=%v, want %v", tc.prior, got.Weight, want)
		}
	}
	// The decay exponent caps at QuizRepeatDecayCap: deeper attempt
	// histories must not starve practice to zero.
	got, _ := GradeQuiz("A", "A", 7)
	if want := WeightQuizCorrect * QuizRepeatDecay * QuizRepeatDecay; math.Abs(got.Weight-want) > 1e-12 {
		t.Errorf("prior=7 weight=%v, want floored %v", got.Weight, want)
	}
	// The same decay applies to wrong answers.
	got, _ = GradeQuiz("A", "C", 1)
	if want := WeightQuizWrong * QuizRepeatDecay; math.Abs(got.Weight-want) > 1e-12 {
		t.Errorf("wrong-answer repeat weight=%v, want %v", got.Weight, want)
	}
}

func TestGradeQuizRejectsInvalidKeys(t *testing.T) {
	for _, chosen := range []string{"", "E", "b", "AA", "1"} {
		if _, err := GradeQuiz("B", chosen, 0); err == nil {
			t.Errorf("chosen key %q should be rejected", chosen)
		}
	}
	if _, err := GradeQuiz("Z", "A", 0); err == nil {
		t.Error("malformed correct key should be rejected")
	}
}
