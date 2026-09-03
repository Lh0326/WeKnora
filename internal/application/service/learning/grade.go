package learning

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// validQuizKeys are the only option keys a quiz item may use. Accepting
// anything else here would let a malformed item smuggle an ungradeable
// answer into the event stream.
var validQuizKeys = map[string]bool{"A": true, "B": true, "C": true, "D": true}

// QuizUnsureKey is the metacognitive escape hatch: the client renders it as
// a fifth "不确定" option and the server grades it as a declaration, not a
// guess (zero-weight quiz_unsure event).
const QuizUnsureKey = "E"

// GradeResult is the deterministic verdict of one quiz answer. The event
// type/weight pair is what the caller appends to learning_events; nothing in
// grading consults a model, so "LLM writes the question, determinism grades
// the answer" holds by construction.
type GradeResult struct {
	Correct   bool
	EventType string
	Weight    float64
}

// GradeQuiz grades chosenKey against the item's correct key and resolves the
// event to append: ±(quiz weight)·QuizRepeatDecay^priorAttempts, so rapid
// re-answers on the same item earn diminishing weight (farming a memorised
// answer converges toward, but never reaches, zero — see the cap below).
// priorAttempts is the caller's count of earlier attempts on this item inside
// the re-ask window (spaced practice counts as fresh, per the spacing
// effect). The decay exponent is capped so practice always moves the needle:
// anti-farming needs diminishing returns, not zero returns, and the logit
// clamp bounds the absolute total anyway. Keys outside A–D are rejected with
// an error rather than graded — except the unsure declaration, which is not
// a graded answer at all: it lands as a zero-weight quiz_unsure event so an
// honest "I don't know" is never punished and never rewards guessing.
func GradeQuiz(correctKey, chosenKey string, priorAttempts int) (GradeResult, error) {
	if !validQuizKeys[correctKey] {
		return GradeResult{}, fmt.Errorf("learning: invalid correct key %q", correctKey)
	}
	if chosenKey == QuizUnsureKey {
		return GradeResult{Correct: false, EventType: types.LearningEventQuizUnsure, Weight: 0}, nil
	}
	if !validQuizKeys[chosenKey] {
		return GradeResult{}, fmt.Errorf("learning: invalid chosen key %q", chosenKey)
	}
	if priorAttempts > QuizRepeatDecayCap {
		priorAttempts = QuizRepeatDecayCap
	}

	correct := chosenKey == correctKey
	res := GradeResult{Correct: correct}
	if correct {
		res.EventType = types.LearningEventQuizCorrect
		res.Weight = WeightQuizCorrect
	} else {
		res.EventType = types.LearningEventQuizWrong
		res.Weight = WeightQuizWrong
	}
	for i := 0; i < priorAttempts; i++ {
		res.Weight *= QuizRepeatDecay
	}
	return res, nil
}
