package estimator

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestBayesianObservationSeparatesLearning(t *testing.T) {
	p := KnowledgeParameters{Initial: 0.2, Learn: 0.1, Guess: 0.2, Slip: 0.1}
	pass, err := p.Observe(0.2, true, true)
	if err != nil || math.Abs(pass.PredictedCorrect-0.34) > 1e-12 ||
		math.Abs(pass.AfterObservation-9.0/17.0) > 1e-12 ||
		math.Abs(pass.AfterLearning-(9.0/17.0+8.0/17.0*0.1)) > 1e-12 {
		t.Fatalf("incorrect Bayesian update: %+v, %v", pass, err)
	}
	fail, err := p.Observe(0.2, false, false)
	if err != nil || math.Abs(fail.AfterObservation-1.0/33.0) > 1e-12 || fail.AfterLearning != fail.AfterObservation {
		t.Fatalf("failure without learning: %+v, %v", fail, err)
	}
}

func TestReadingIsPredictionAndSelfReportIsBounded(t *testing.T) {
	p := KnowledgeParameters{Initial: 0.2, Learn: 0.1, Guess: 0.2, Slip: 0.1}
	read, err := p.ReadOpportunity(0.2, 1, 0.25)
	pass, _ := p.Observe(0.2, true, false)
	if err != nil || read <= 0.2 || read >= pass.AfterObservation {
		t.Fatalf("reading incorrectly treated as a correct answer: %v, %v", read, err)
	}
	none, _ := p.ReadOpportunity(0.2, 0, 0.25)
	if none != 0.2 {
		t.Fatal("zero exposure changed belief")
	}
	corrected, err := CorrectBelief(0.2, 3)
	if err != nil || math.Abs(corrected-3.0/7.0) > 1e-12 {
		t.Fatalf("noisy correction: %v, %v", corrected, err)
	}
	if _, err := CorrectBelief(0.2, math.Inf(1)); err == nil {
		t.Fatal("accepted an unbounded correction")
	}
}

func TestInvalidKnowledgeParameters(t *testing.T) {
	for _, p := range []KnowledgeParameters{
		{0.2, 0.1, 0.8, 0.3}, {math.NaN(), 0.1, 0.2, 0.1}, {0.2, 0, 0.2, 0.1},
	} {
		if p.Validate() == nil {
			t.Fatalf("accepted invalid parameters: %+v", p)
		}
	}
}

func TestFSRSDefaultFirstRecallAndForgetting(t *testing.T) {
	at := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	facts := []RecallFact{{ID: "a", ContentVersion: "v1", At: at, Rating: 3}}
	first, err := ProjectRecall(facts, "v1", at, 0.9)
	if err != nil || first == nil {
		t.Fatalf("first recall: %+v, %v", first, err)
	}
	// FSRS-6 published default Good stability, independent of our adapter.
	if math.Abs(first.Stability-2.3065) > 1e-12 || first.Retrievability != 1 || first.Observations != 1 {
		t.Fatalf("wrong official default initialization: %+v", first)
	}
	later, err := ProjectRecall(facts, "v1", at.Add(30*24*time.Hour), 0.9)
	if err != nil || later.Retrievability >= first.Retrievability || later.Stability != first.Stability {
		t.Fatalf("elapsed time should decay recall, not mutate stability: %+v, %v", later, err)
	}
}

func TestFSRSReplayIsDeterministicVersionedAndCausal(t *testing.T) {
	at := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	a := RecallFact{ID: "a", ContentVersion: "v1", At: at, Rating: 3}
	b := RecallFact{ID: "b", ContentVersion: "v1", At: at.Add(3 * 24 * time.Hour), Rating: 1}
	input := []RecallFact{b, a, a}
	before := append([]RecallFact{}, input...)
	want, _ := ProjectRecall([]RecallFact{a, b}, "v1", b.At, 0.9)
	got, err := ProjectRecall(input, "v1", b.At, 0.9)
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(input, before) || got.Observations != 2 {
		t.Fatalf("replay changed by arrival order or duplicate: %+v, %v", got, err)
	}
	past, _ := ProjectRecall(input, "v1", at, 0.9)
	if past.Observations != 1 {
		t.Fatal("future assessment leaked into prediction")
	}
	newContent, err := ProjectRecall(input, "v2", b.At, 0.9)
	if err != nil || newContent != nil {
		t.Fatal("old-version success was treated as new-version recall")
	}
	conflict := a
	conflict.Rating = 1
	if _, err := ProjectRecall([]RecallFact{a, conflict}, "v1", at, 0.9); err == nil {
		t.Fatal("conflicting assessment identity accepted")
	}
}

func TestNoAssessmentCannotInventRecallState(t *testing.T) {
	at := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	got, err := ProjectRecall(nil, "v1", at, 0.9)
	if got != nil || err != nil {
		t.Fatalf("empty history must have no memory estimate: %+v, %v", got, err)
	}
}

func TestDiagnosticInformationIsNotLearningGain(t *testing.T) {
	p := KnowledgeParameters{Initial: 0.5, Learn: 0.1, Guess: 0.1, Slip: 0.1}
	got, err := p.InformationGain(0.5)
	// A binary symmetric observation with 10% error carries 1-H2(0.1) bits.
	if err != nil || math.Abs(got-0.5310044064107188) > 1e-12 {
		t.Fatalf("diagnostic information: %.15f %v", got, err)
	}
	p.Learn = 0.9
	other, _ := p.InformationGain(0.5)
	nearCertain, _ := p.InformationGain(0.999)
	if got != other || nearCertain >= got {
		t.Fatal("learning transition or high certainty inflated diagnostic value")
	}
}
