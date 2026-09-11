// Package estimator holds the versioned, deterministic learning models.
// Evidence collection, authorization and recommendation policy live outside it.
package estimator

import (
	"fmt"
	"math"
)

// KnowledgeParameters are the four interpretable BKT parameters. Callers must
// identify their provenance; the package does not label heuristic defaults as
// calibrated enterprise-reading parameters.
type KnowledgeParameters struct {
	Initial float64 `json:"initial"`
	Learn   float64 `json:"learn"`
	Guess   float64 `json:"guess"`
	Slip    float64 `json:"slip"`
}

func (p KnowledgeParameters) Validate() error {
	for _, x := range []float64{p.Initial, p.Learn, p.Guess, p.Slip} {
		if math.IsNaN(x) || math.IsInf(x, 0) || x <= 0 || x >= 1 {
			return fmt.Errorf("BKT parameters must be finite and strictly between zero and one")
		}
	}
	if p.Guess+p.Slip >= 1 {
		return fmt.Errorf("BKT requires correct answers to be more likely when proficient")
	}
	return nil
}

type KnowledgeUpdate struct {
	PredictedCorrect float64 `json:"predicted_correct"`
	AfterObservation float64 `json:"after_observation"`
	AfterLearning    float64 `json:"after_learning"`
}

func validBelief(m float64) bool {
	return !math.IsNaN(m) && !math.IsInf(m, 0) && m > 0 && m < 1
}

func boundedBelief(m float64) float64 {
	return math.Max(1e-9, math.Min(1-1e-9, m))
}

// Observe implements Bayes' rule followed by an optional learning transition.
// The distinct outputs prevent post-feedback learning from being represented as
// evidence that the learner had already known an answer they got wrong.
func (p KnowledgeParameters) Observe(m float64, correct, feedbackRead bool) (KnowledgeUpdate, error) {
	if err := p.Validate(); err != nil {
		return KnowledgeUpdate{}, err
	}
	if !validBelief(m) {
		return KnowledgeUpdate{}, fmt.Errorf("invalid prior belief")
	}
	q := m*(1-p.Slip) + (1-m)*p.Guess
	posterior := m * p.Slip / (1 - q)
	if correct {
		posterior = m * (1 - p.Slip) / q
	}
	next := posterior
	if feedbackRead {
		next += (1 - posterior) * p.Learn
	}
	return KnowledgeUpdate{q, boundedBelief(posterior), boundedBelief(next)}, nil
}

// ReadOpportunity is a prediction-only extension: a reading opportunity may
// cause learning but is NOT a correct observation. Dose and relative efficacy
// are explicit, uncalibrated reading assumptions. Deduplication and session
// saturation must happen before calling this function.
func (p KnowledgeParameters) ReadOpportunity(m, dose, relativeEfficacy float64) (float64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if !validBelief(m) || math.IsNaN(dose) || math.IsNaN(relativeEfficacy) ||
		dose < 0 || dose > 1 || relativeEfficacy < 0 || relativeEfficacy > 1 {
		return 0, fmt.Errorf("invalid reading opportunity")
	}
	transition := -math.Expm1(dose * relativeEfficacy * math.Log1p(-p.Learn))
	return boundedBelief(m + (1-m)*transition), nil
}

// InformationGain is the expected reduction in uncertainty about the latent
// knowledge state from one answer, in bits. Learning after the answer is NOT
// included: otherwise an instructional effect would inflate diagnostic value.
func (p KnowledgeParameters) InformationGain(m float64) (float64, error) {
	correct, err := p.Observe(m, true, false)
	if err != nil {
		return 0, err
	}
	wrong, _ := p.Observe(m, false, false)
	entropy := func(x float64) float64 {
		return -x*math.Log2(x) - (1-x)*math.Log2(1-x)
	}
	q := correct.PredictedCorrect
	return math.Max(0, entropy(m)-q*entropy(correct.AfterObservation)-(1-q)*entropy(wrong.AfterObservation)), nil
}

// CorrectBelief accepts a bounded likelihood ratio from a noisy self-report.
// It adjusts a belief, never creates a correct-answer fact or an FSRS review.
// The ratio must come from an explicit policy; repeating an identical report
// must be deduplicated by the caller.
func CorrectBelief(m, likelihoodRatio float64) (float64, error) {
	if !validBelief(m) || math.IsNaN(likelihoodRatio) || likelihoodRatio < 0.125 || likelihoodRatio > 8 {
		return 0, fmt.Errorf("invalid self-report likelihood ratio")
	}
	return boundedBelief(m * likelihoodRatio / (1 - m + m*likelihoodRatio)), nil
}
