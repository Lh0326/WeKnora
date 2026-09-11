package interfaces

import (
	"errors"
	"time"
)

var ErrLearningReviewConflict = errors.New("learning: review or source changed; reload before submitting")

type LearningReviewInput struct {
	Slug           string `json:"slug"`
	Action         string `json:"action"`
	Revision       string `json:"revision"`
	ContentVersion string `json:"content_version"`
	RequestID      string `json:"request_id"`
}

// Self-reported recall scheduling, separate from objective verification.
type LearningReviewStatus struct {
	Revision       string    `json:"revision"`
	ContentVersion string    `json:"content_version"`
	ContentChanged bool      `json:"content_changed"`
	PolicyVersion  string    `json:"policy_version"`
	Active         bool      `json:"active"`
	Due            bool      `json:"due"`
	DueAt          time.Time `json:"due_at"`
	IntervalDays   int       `json:"interval_days"`
	Repetitions    int       `json:"repetitions"`
	Ease           float64   `json:"ease"`
	LastRating     string    `json:"last_rating,omitempty"`
	EarlyPractice  bool      `json:"early_practice"`
}
