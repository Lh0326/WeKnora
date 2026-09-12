package interfaces

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"time"
)

// Optional extension keeps the existing page model and its consumers intact.
type LearningComponentRepository interface {
	ListComponents(context.Context, uint64, string) ([]types.LearningComponent, error)
	SaveComponents(context.Context, []types.LearningComponent) error
}

// Assessment reads source versions through the learning repository so they
// share the caller's transaction connection with material and personal facts.
// Isolation still follows that database transaction's configured level.
type LearningComponentAssessmentRepository interface {
	ListComponentAssessmentSources(context.Context, uint64, string) ([]*types.WikiPage, error)
}

// The memory subsystem has its own lifecycle. Learning consumes only inputs
// observed after a profile reset, without deleting core assistant memories.
type LearningComponentMemoryRepository interface {
	ListComponentMemoryInputs(context.Context, LearningScope) ([]types.MemoryDocAffinity, []types.MemoryWikiMap, error)
}
type LearningComponentService interface {
	ComponentView(context.Context, string, int, string) (*ComponentView, error)
	RecordComponentAction(context.Context, string, ComponentAction) (*ComponentActionResult, error)
	ImportComponents(context.Context, string, []types.ComponentDefinition) (int, error)
}

// Optional scope-aware reader; legacy readers keep their existing signature.
type LearningComponentScopeService interface {
	ComponentViewWithTopic(context.Context, string, int, string, string) (*ComponentView, error)
}

// Separate optional interface avoids changing clients that only read/act.
type LearningComponentDraftService interface {
	DraftComponents(context.Context, string, ComponentDraftRequest) (*ComponentDraftResult, error)
}
type ComponentDraftRequest struct {
	Slugs       []string `json:"slugs"`
	Limit       int      `json:"limit"`
	ModelSource string   `json:"model_source,omitempty"`
	ModelID     string   `json:"model_id,omitempty"`
}
type ComponentDraft struct {
	Material types.ComponentDefinition `json:"material"`
	Warnings []string                  `json:"warnings"`
}
type ComponentDraftIssue struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}
type ComponentDraftResult struct {
	PolicyVersion  string                      `json:"policy_version"`
	ModelID        string                      `json:"model_id"`
	Status         string                      `json:"status"`
	ReviewRequired bool                        `json:"review_required"`
	Candidates     []ComponentDraft            `json:"candidates"`
	Rejected       []ComponentDraftIssue       `json:"rejected"`
	SourceHashes   map[string]string           `json:"source_hashes"`
	ModelNotes     []string                    `json:"model_notes"`
	ModelProposals []types.ComponentDefinition `json:"model_proposals"`
}
type ComponentState struct {
	Level                  string  `json:"level"`
	Reads                  int     `json:"reads"`
	Checks                 int     `json:"checks"`
	Passes                 int     `json:"passes"`
	Practice               int     `json:"practice"`
	LegacyTouches          int     `json:"legacy_touches"`
	LegacyComponentTouches int     `json:"legacy_component_touches"`
	Familiarity            float64 `json:"familiarity"`
	// False means the numeric prior is not supported by performance observations.
	PerformanceObserved bool       `json:"performance_observed"`
	Lower               float64    `json:"lower"`
	Upper               float64    `json:"upper"`
	Basis               string     `json:"basis"`
	SelfReport          string     `json:"self_report,omitempty"`
	LastStudyAt         time.Time  `json:"last_study_at,omitempty"`
	LastReadAt          time.Time  `json:"last_read_at,omitempty"`
	LastRecallAt        time.Time  `json:"last_recall_at,omitempty"`
	LastCheckAt         time.Time  `json:"last_check_at,omitempty"`
	LastDifficultyAt    time.Time  `json:"last_difficulty_at,omitempty"`
	DueAt               *time.Time `json:"due_at,omitempty"`
	ReadCheckIDs        []string   `json:"seen_check_ids"`
}
type ComponentEntry struct {
	ID            string                    `json:"id"`
	Version       string                    `json:"version"`
	Material      types.ComponentDefinition `json:"material"`
	Available     bool                      `json:"available"`
	SourceProblem string                    `json:"source_problem,omitempty"`
	State         ComponentState            `json:"state"`
	Relevance     *ComponentRelevance       `json:"relevance,omitempty"`
}

// Relevance is personal navigation context, never performance evidence.
type ComponentRelevance struct {
	Weight  float64                  `json:"weight"`
	Sources int                      `json:"sources"`
	Links   []ComponentRelevanceLink `json:"links"`
}
type ComponentRelevanceLink struct {
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	SharedTargets int    `json:"shared_targets"`
	Reason        string `json:"reason"`
}
type ComponentStep struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Action          string   `json:"action"`
	Reason          string   `json:"reason"`
	Minutes         int      `json:"minutes"`
	Utility         int      `json:"utility"`
	Conditional     bool     `json:"conditional"`
	PrerequisiteIDs []string `json:"prerequisite_ids,omitempty"`
}
type ComponentPlanInfo struct {
	// Search metrics are conditional on the selected focus/closure. They do
	// not establish optimal focus selection or measured learning benefit.
	PolicyVersion  string   `json:"policy_version"`
	Exact          bool     `json:"exact"`
	Expanded       int      `json:"expanded"`
	Candidates     int      `json:"candidates"`
	Utility        int      `json:"utility"`
	UpperBound     int      `json:"upper_bound"`
	CheckBudget    int      `json:"check_budget"`
	Notice         string   `json:"notice,omitempty"`
	TopicScope     string   `json:"topic_scope,omitempty"`
	FocusGoalID    string   `json:"focus_goal_id,omitempty"`
	FocusGoalTitle string   `json:"focus_goal_title,omitempty"`
	FocusReason    string   `json:"focus_reason,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	Progression    string   `json:"progression,omitempty"`
	Limitations    []string `json:"limitations,omitempty"`
}
type ComponentView struct {
	ModelVersion    string            `json:"model_version"`
	Components      []ComponentEntry  `json:"components"`
	Steps           []ComponentStep   `json:"steps"`
	Budget          int               `json:"budget"`
	UsedMinutes     int               `json:"used_minutes"`
	RelevanceStatus string            `json:"relevance_status"`
	Plan            ComponentPlanInfo `json:"plan"`
}
type ComponentAction struct {
	ID          string `json:"component_id"`
	Version     string `json:"version"`
	Action      string `json:"action"`
	OperationID string `json:"operation_id"`
	SessionID   string `json:"session_id"`
	CheckID     string `json:"check_id"`
	Answer      string `json:"answer"`
	Helped      bool   `json:"helped"`
	Rating      int    `json:"rating"`
}
type ComponentActionResult struct {
	Recorded         bool            `json:"recorded"`
	Duplicate        bool            `json:"duplicate"`
	ReadAfterSeconds int             `json:"read_after_seconds"`
	Correct          *bool           `json:"correct,omitempty"`
	Eligible         bool            `json:"eligible"`
	Explanation      string          `json:"explanation,omitempty"`
	State            *ComponentState `json:"state,omitempty"`
}
