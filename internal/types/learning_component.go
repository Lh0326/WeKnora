package types

import "time"

const LearningEventComponent = "component_learning"

// Definitions are KB-shared material. Personal facts remain in learning_events.
// ID is stable across source-page renames and independent of any single source.
type LearningComponent struct {
	ID              string              `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID        uint64              `json:"tenant_id" gorm:"not null;index:idx_learning_components_scope,priority:1"`
	KnowledgeBaseID string              `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_components_scope,priority:2"`
	Version         string              `json:"version" gorm:"type:varchar(64);not null"`
	Definition      ComponentDefinition `json:"-" gorm:"type:jsonb;serializer:json;not null"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

func (LearningComponent) TableName() string { return "learning_components" }

type ComponentDefinition struct {
	Key            string            `json:"key"`
	Title          string            `json:"title"`
	Topic          string            `json:"topic"`
	Condition      string            `json:"condition"`
	Goal           string            `json:"goal"`
	Explanation    string            `json:"explanation"`
	Example        string            `json:"example"`
	Minutes        int               `json:"minutes"`
	Sources        []ComponentSource `json:"sources"`
	Prerequisites  []string          `json:"prerequisites"`
	Related        []string          `json:"related"`
	Checks         []ComponentCheck  `json:"checks"`
	Provenance     string            `json:"provenance"`
	MaterialStatus string            `json:"material_status,omitempty"`
	ReviewNote     string            `json:"review_note,omitempty"`
}
type ComponentSource struct {
	Slug  string `json:"slug"`
	Quote string `json:"quote"`
	Role  string `json:"role"`
	Hash  string `json:"hash,omitempty"`
}
type ComponentCheck struct {
	ID          string            `json:"id"`
	Family      string            `json:"family"`
	Question    string            `json:"question"`
	Options     map[string]string `json:"options"`
	Answer      string            `json:"answer,omitempty"`
	Explanation string            `json:"explanation,omitempty"`
}
