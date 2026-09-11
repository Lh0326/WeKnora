package interfaces

import "errors"

var ErrLearningPreferenceConflict = errors.New("learning: saved plan preferences changed")

// Revision is the token returned by GET. Empty means no saved defaults yet.
// Transient exclusions and a voluntary fast-track attempt are not persisted.
type LearningPlanSettings struct {
	LimitToFolder     bool     `json:"limit_to_folder"`
	Revision          string   `json:"revision"`
	FolderID          string   `json:"folder_id"`
	Depth             string   `json:"depth"`
	TimeBudgetMinutes int      `json:"time_budget_minutes"`
	UseMemory         bool     `json:"use_memory"`
	GoalObjectives    []string `json:"goal_objectives"`
}
