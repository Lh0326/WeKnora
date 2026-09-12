package repository

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *learningRepository) GetPlanPreference(ctx context.Context, scope interfaces.LearningScope) (*types.LearningPlanPreference, error) {
	var row types.LearningPlanPreference
	err := r.scoped(ctx, scope).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

// CAS prevents another browser, or a stale page after deletion, from
// overwriting newer defaults. The service also holds the deletion epoch lock.
func (r *learningRepository) SavePlanPreference(ctx context.Context, row *types.LearningPlanPreference, expected string) error {
	var result *gorm.DB
	if expected == "" {
		result = r.database(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	} else {
		scope := interfaces.LearningScope{TenantID: row.TenantID, SubjectID: row.SubjectID, KnowledgeBaseID: row.KnowledgeBaseID}
		result = r.scoped(ctx, scope).Model(&types.LearningPlanPreference{}).Where("revision = ?", expected).Select("revision", "folder_id", "limit_to_folder", "depth", "time_budget_minutes", "use_memory", "goal_objectives", "updated_at").Updates(row)
	}
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return interfaces.ErrLearningPreferenceConflict
	}
	return nil
}

func (r *learningRepository) ListPlanPreferencesBySubject(ctx context.Context, subject string) ([]types.LearningPlanPreference, error) {
	rows := []types.LearningPlanPreference{}
	err := r.database(ctx).Where("subject_id = ?", subject).Order("tenant_id, knowledge_base_id").Find(&rows).Error
	return rows, err
}
