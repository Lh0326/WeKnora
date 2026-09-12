package learning

import (
	"context"
	"fmt"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"sort"
	"time"
)

func (s *Service) GetPlanPreferences(ctx context.Context, kb string) (*interfaces.LearningPlanSettings, error) {
	scope, err := resolveReadScope(ctx, kb)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.GetPlanPreference(ctx, scope)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &interfaces.LearningPlanSettings{Depth: "aware", TimeBudgetMinutes: 15, UseMemory: true, GoalObjectives: []string{}}, nil
	}
	return planSettingsOf(row), nil
}

func planSettingsOf(row *types.LearningPlanPreference) *interfaces.LearningPlanSettings {
	return &interfaces.LearningPlanSettings{Revision: row.Revision, LimitToFolder: row.LimitToFolder, FolderID: row.FolderID, Depth: row.Depth, TimeBudgetMinutes: row.TimeBudgetMinutes, UseMemory: row.UseMemory, GoalObjectives: append([]string{}, row.GoalObjectives...)}
}

func (s *Service) UpdatePlanPreferences(ctx context.Context, kb string, input interfaces.LearningPlanSettings) (*interfaces.LearningPlanSettings, error) {
	scope, err := resolveReadScope(ctx, kb)
	if err != nil {
		return nil, err
	}
	if (!input.LimitToFolder && input.FolderID != "") || input.TimeBudgetMinutes < 1 || input.TimeBudgetMinutes > 120 || len(input.Revision) > 36 || len(input.FolderID) > 128 || len(input.GoalObjectives) > 200 || (input.Depth != "aware" && input.Depth != "operate" && input.Depth != "analyze") {
		return nil, fmt.Errorf("%w: invalid plan preferences", ErrInvalidLearningRequest)
	}
	var result *interfaces.LearningPlanSettings
	err = s.runSubject(ctx, scope.SubjectID, false, func(tx context.Context) error {
		if input.LimitToFolder {
			pages, err := s.nodePages(tx, kb)
			if err != nil {
				return err
			}
			found := false
			for _, p := range pages {
				if p != nil && p.FolderID == input.FolderID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: learning module is unavailable", ErrInvalidLearningRequest)
			}
		}
		goals := []string{}
		seen := map[string]bool{}
		if len(input.GoalObjectives) > 0 {
			defs, err := s.currentObjectiveDefinitions(tx, scope.TenantID, kb)
			if err != nil {
				return err
			}
			valid := map[string]bool{}
			for _, o := range defs {
				if o.Status == types.LearningObjectiveStatusPublished {
					valid[o.ID] = true
				}
			}
			for _, id := range input.GoalObjectives {
				if !valid[id] {
					return fmt.Errorf("%w: unavailable learning goal", ErrInvalidLearningRequest)
				}
				if !seen[id] {
					goals = append(goals, id)
					seen[id] = true
				}
			}
		}
		sort.Strings(goals)
		row := types.LearningPlanPreference{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: kb, Revision: uuid.NewString(), FolderID: input.FolderID, LimitToFolder: input.LimitToFolder, Depth: input.Depth, TimeBudgetMinutes: input.TimeBudgetMinutes, UseMemory: input.UseMemory, GoalObjectives: types.RefList(goals), UpdatedAt: time.Now()}
		if err := s.repo.SavePlanPreference(tx, &row, input.Revision); err != nil {
			return err
		}
		result = planSettingsOf(&row)
		return nil
	})
	return result, err
}
