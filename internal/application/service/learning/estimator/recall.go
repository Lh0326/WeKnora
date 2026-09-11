package estimator

import (
	"fmt"
	"math"
	"sort"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
)

const RecallModelVersion = "fsrs6-go4-default-v1"

// RecallFact represents an actual recall assessment, NOT a page view, citation
// or declaration of familiarity. Its scope and revision chain must be validated
// by the service before projecting it. Ratings use the upstream 1..4 values.
type RecallFact struct {
	ID             string
	ContentVersion string
	At             time.Time
	Rating         int
}

type RecallEstimate struct {
	ModelVersion   string    `json:"model_version"`
	ContentVersion string    `json:"content_version"`
	Stability      float64   `json:"stability"`
	Difficulty     float64   `json:"difficulty"`
	Retrievability float64   `json:"retrievability"`
	Desired        float64   `json:"desired_retention"`
	DueAt          time.Time `json:"due_at"`
	LastRecallAt   time.Time `json:"last_recall_at"`
	Observations   int       `json:"observations"`
}

// ProjectRecall replays genuine assessments with the pinned official FSRS-6
// implementation. No invented initial success, no random interval fuzz, no
// wall-clock reads, no mutation of supplied facts. Other content versions and
// future observations cannot inform a current-content, current-time prediction.
func ProjectRecall(input []RecallFact, content string, now time.Time, desired float64) (*RecallEstimate, error) {
	if content == "" || now.IsZero() || math.IsNaN(desired) || desired <= 0 || desired >= 1 {
		return nil, fmt.Errorf("invalid recall projection context")
	}
	facts := make([]RecallFact, 0, len(input))
	seen := map[string]RecallFact{}
	for _, f := range input {
		if f.ContentVersion != content || f.At.After(now) {
			continue
		}
		if f.ID == "" || f.At.IsZero() || f.Rating < 1 || f.Rating > 4 {
			return nil, fmt.Errorf("invalid recall assessment")
		}
		if old, ok := seen[f.ID]; ok {
			if old.Rating != f.Rating || !old.At.Equal(f.At) || old.ContentVersion != f.ContentVersion {
				return nil, fmt.Errorf("conflicting recall assessment identity")
			}
			continue
		}
		seen[f.ID] = f
		facts = append(facts, f)
	}
	if len(facts) == 0 {
		return nil, nil
	}
	sort.Slice(facts, func(i, j int) bool {
		if !facts[i].At.Equal(facts[j].At) {
			return facts[i].At.Before(facts[j].At)
		}
		return facts[i].ID < facts[j].ID
	})
	parameters := fsrs.DefaultParam()
	parameters.RequestRetention = desired
	parameters.MaximumInterval = 365
	parameters.EnableFuzz = false
	engine := fsrs.NewFSRS(parameters)
	card := fsrs.NewCard(facts[0].At)
	for _, f := range facts {
		result, err := engine.Next(card, f.At, fsrs.Rating(f.Rating))
		if err != nil {
			return nil, fmt.Errorf("FSRS recall update: %w", err)
		}
		card = result.Card
	}
	r, err := engine.Retrievability(card, now)
	if err != nil {
		return nil, fmt.Errorf("FSRS recall prediction: %w", err)
	}
	return &RecallEstimate{
		ModelVersion: RecallModelVersion, ContentVersion: content,
		Stability: card.Stability, Difficulty: card.Difficulty,
		Retrievability: r, Desired: desired, DueAt: card.Due,
		LastRecallAt: facts[len(facts)-1].At, Observations: len(facts),
	}, nil
}
