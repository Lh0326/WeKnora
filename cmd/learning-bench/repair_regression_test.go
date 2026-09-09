package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCalibrationIncludesEmptyHistoryAndSortsAnswers(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	a := types.LearningQuizAttempt{Slug: "a", IsCorrect: true, AnsweredAt: at}
	loss, n := quizLogLoss(optInput{Attempts: []types.LearningQuizAttempt{a}})
	if n != 1 || math.Abs(loss-math.Log(2)) > 1e-9 {
		t.Fatalf("no-history sample omitted: n=%d loss=%f", n, loss)
	}
	b := a
	b.AnsweredAt = at.Add(2 * time.Hour)
	in := optInput{Events: []types.LearningEvent{{Slug: "a", Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: at.Add(time.Hour)}}, Attempts: []types.LearningQuizAttempt{a, b}}
	want, _ := quizLogLoss(in)
	in.Attempts = []types.LearningQuizAttempt{b, a}
	got, _ := quizLogLoss(in)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("export order changes predictions: %f vs %f", got, want)
	}
}

func TestTemporalSplitDoesNotSplitSameSitting(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	in := optInput{Attempts: []types.LearningQuizAttempt{
		{Slug: "a", QuizItemID: "q1", AnsweredAt: at},
		{Slug: "a", QuizItemID: "q2", AnsweredAt: at},
		{Slug: "a", QuizItemID: "q3", AnsweredAt: at},
	}}
	train, val := temporalSplit(in, 0.7)
	if len(train.Attempts) != 0 || len(val.Attempts) != 3 {
		t.Fatal("same-timestamp sitting leaked across the split")
	}
	if _, err := runFit(in, false); err == nil {
		t.Fatal("fit accepted data with no chronological training set")
	}
}

func TestOptimizeReportNamesHoldoutBasis(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "report.json")
	blob, err := json.Marshal(syntheticCorpus(42))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, blob, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runOptimize(input, output, false); err != nil {
		t.Fatal(err)
	}
	blob, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]interface{}
	if err := json.Unmarshal(blob, &report); err != nil {
		t.Fatal(err)
	}
	if report["improvement_basis"] != "temporal_holdout" || report["holdout_attempts"].(float64) <= 0 {
		t.Fatalf("report hides validation protocol: %v", report)
	}
	before, after := report["holdout_logloss_before"].(float64), report["holdout_logloss_after"].(float64)
	if math.Abs(report["improvement"].(float64)-(before-after)/before) > 1e-9 {
		t.Fatal("improvement does not match displayed holdout losses")
	}
}

func TestCalibrationRetainsResolvedQuizDiscount(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	// Both are full-weight, spaced attempts (or distinct items). No item ID
	// on legacy events permits inferring a lifetime repeat count per slug.
	events := []types.LearningEvent{
		{Slug: "a", Type: types.LearningEventQuizCorrect, Weight: learning.WeightQuizCorrect, OccurredAt: at},
		{Slug: "a", Type: types.LearningEventQuizWrong, Weight: learning.WeightQuizWrong, OccurredAt: at.Add(72 * time.Hour)},
	}
	now := at.Add(73 * time.Hour)
	state := learning.FoldState{}
	for _, ev := range events {
		state = learning.FoldEvent(state, learning.Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
	}
	want := -math.Log(clampProb(learning.EffectiveP(state, now)))
	got, _ := quizLogLoss(optInput{Events: events, Attempts: []types.LearningQuizAttempt{{Slug: "a", IsCorrect: true, AnsweredAt: now}}})
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("calibration differs from production: %f vs %f", got, want)
	}
}
