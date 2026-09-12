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

// GradeQuiz always returns feedback, but only an independent attempt earns
// evidence. priorAttempts counts all earlier answers (including unsure) to
// this item within 48 hours. Repeats have zero weight because feedback has
// disclosed the answer; a later sitting outside that window is eligible.
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

	correct := chosenKey == correctKey
	res := GradeResult{Correct: correct}
	if correct {
		res.EventType = types.LearningEventQuizCorrect
		res.Weight = WeightQuizCorrect
	} else {
		res.EventType = types.LearningEventQuizWrong
		res.Weight = WeightQuizWrong
	}
	// Every submission reveals the answer. Repeating it inside the same
	// 48-hour window is practice feedback, not independent ability evidence.
	if priorAttempts > 0 {
		res.Weight = 0
	}
	return res, nil
}
