package repository

import (
	"context"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (r *learningRepository) ListComponents(ctx context.Context, tenant uint64, kb string) ([]types.LearningComponent, error) {
	rows := []types.LearningComponent{}
	err := r.database(ctx).Where("tenant_id = ? AND knowledge_base_id = ?", tenant, kb).Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListComponentMemoryInputs(ctx context.Context, scope interfaces.LearningScope) ([]types.MemoryDocAffinity, []types.MemoryWikiMap, error) {
	var reset types.LearningSubjectEpoch
	db := r.database(ctx)
	err := db.Where("subject_id = ?", scope.SubjectID).First(&reset).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	aq := db.Where("tenant_id = ? AND subject_id = ? AND knowledge_base_id = ?", scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID)
	mq := db.Where("tenant_id = ? AND subject_id = ? AND knowledge_base_id = ?", scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID)
	if reset.Epoch > 0 {
		// A refreshed mapping alone is insufficient: background maintenance
		// might recreate it from an old topic. Require a genuinely newer input.
		aq = aq.Where("last_used_at > ?", reset.UpdatedAt)
		mq = mq.Where("updated_at > ?", reset.UpdatedAt)
		var topics []types.MemoryTopicStat
		if err = db.Where("tenant_id = ? AND subject_id = ? AND last_seen_at > ? AND last_seen_at <= ?", scope.TenantID, scope.SubjectID, reset.UpdatedAt, time.Now()).Find(&topics).Error; err != nil {
			return nil, nil, err
		}
		keys := make([]string, 0, len(topics))
		for _, topic := range topics {
			keys = append(keys, topic.NormalizedKey)
		}
		if len(keys) == 0 {
			mq = mq.Where("1 = 0")
		} else {
			mq = mq.Where("normalized_topic_key IN ?", keys)
		}
	}
	var affinities []types.MemoryDocAffinity
	var mappings []types.MemoryWikiMap
	if err = aq.Find(&affinities).Error; err != nil {
		return nil, nil, err
	}
	if err = mq.Find(&mappings).Error; err != nil {
		return nil, nil, err
	}
	return affinities, mappings, nil
}
func (r *learningRepository) SaveComponents(ctx context.Context, rows []types.LearningComponent) error {
	return r.database(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, Where: clause.Where{Exprs: []clause.Expression{
				clause.Eq{Column: clause.Column{Table: "learning_components", Name: "tenant_id"}, Value: row.TenantID},
				clause.Eq{Column: clause.Column{Table: "learning_components", Name: "knowledge_base_id"}, Value: row.KnowledgeBaseID},
			}}, DoUpdates: clause.AssignmentColumns([]string{"version", "definition", "updated_at"})}).Create(&row)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("component belongs to another scope")
			}
		}
		return nil
	})
}
