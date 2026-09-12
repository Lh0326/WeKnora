package types

import "time"

// LearningPlanPreference is an explicit personal preference, not telemetry
// or mastery evidence. It participates in profile export and deletion.
type LearningPlanPreference struct {
	LimitToFolder     bool      `json:"limit_to_folder" gorm:"not null"`
	TenantID          uint64    `json:"tenant_id" gorm:"primaryKey;autoIncrement:false"`
	SubjectID         string    `json:"subject_id" gorm:"primaryKey;type:varchar(512)"`
	KnowledgeBaseID   string    `json:"knowledge_base_id" gorm:"primaryKey;type:varchar(36)"`
	Revision          string    `json:"revision" gorm:"type:varchar(36);not null"`
	FolderID          string    `json:"folder_id" gorm:"type:varchar(128);not null;default:''"`
	Depth             string    `json:"depth" gorm:"type:varchar(16);not null"`
	TimeBudgetMinutes int       `json:"time_budget_minutes" gorm:"not null"`
	UseMemory         bool      `json:"use_memory" gorm:"not null"`
	GoalObjectives    RefList   `json:"goal_objectives" gorm:"type:jsonb;not null"`
	UpdatedAt         time.Time `json:"updated_at" gorm:"not null"`
}

func (LearningPlanPreference) TableName() string { return "learning_plan_preferences" }
