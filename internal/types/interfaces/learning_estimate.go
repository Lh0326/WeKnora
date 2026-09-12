package interfaces

import "time"

// LearningEstimate is explicitly a model estimate, not a verification verdict.
// Lower/Upper are a parameter-sensitivity envelope, not confidence intervals.
type LearningEstimate struct {
	ContentVersion       string                  `json:"content_version"`
	ModelVersion         string                  `json:"model_version"`
	Level                string                  `json:"level"`
	Familiarity          float64                 `json:"familiarity"`
	PerformanceObserved  bool                    `json:"performance_observed"`
	SelfReport           string                  `json:"self_report,omitempty"`
	ReadPriority         float64                 `json:"read_priority"`
	Lower                float64                 `json:"lower"`
	Upper                float64                 `json:"upper"`
	Coverage             float64                 `json:"coverage"`
	Opportunities        float64                 `json:"opportunities"`
	ExpectedGain         float64                 `json:"expected_gain"` // Deprecated: zero; reading yield is not observed.
	InformationGain      float64                 `json:"information_gain"`
	Answers              int                     `json:"answers"`
	Corrections          int                     `json:"corrections"`
	Basis                string                  `json:"basis"`
	LastStudyAt          time.Time               `json:"last_study_at,omitempty"`
	LastAnswerAt         time.Time               `json:"last_answer_at,omitempty"`
	LastAnswerPrediction float64                 `json:"last_answer_prediction,omitempty"`
	Memory               *LearningMemoryEstimate `json:"memory,omitempty"`
}

type LearningMemoryEstimate struct {
	ModelVersion   string    `json:"model_version"`
	Stability      float64   `json:"stability"`
	Difficulty     float64   `json:"difficulty"`
	Retrievability float64   `json:"retrievability"`
	DueAt          time.Time `json:"due_at"`
	Observations   int       `json:"observations"`
}
