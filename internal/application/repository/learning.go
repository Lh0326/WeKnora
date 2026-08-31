package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// defaultLearningEventLimit bounds ListEvents when the caller passes no
// limit; large enough for a full timeline page, small enough that an
// unbounded replay request cannot pull the whole table in one go.
const defaultLearningEventLimit = 500

type learningRepository struct {
	db *gorm.DB
}

// NewLearningRepository creates the learning-layer repository.
func NewLearningRepository(db *gorm.DB) interfaces.LearningRepository {
	return &learningRepository{db: db}
}

// scoped starts every subject-scoped query already filtered by workspace,
// subject and knowledge base, so a missing scope predicate is impossible —
// the same containment strategy as the memory repository.
func (r *learningRepository) scoped(ctx context.Context, scope interfaces.LearningScope) *gorm.DB {
	return r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ?",
			scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID,
		)
}

func (r *learningRepository) AppendEvent(ctx context.Context, event *types.LearningEvent) error {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *learningRepository) ListEvents(
	ctx context.Context, scope interfaces.LearningScope, since time.Time, limit int,
) ([]types.LearningEvent, error) {
	if limit <= 0 {
		limit = defaultLearningEventLimit
	}
	query := r.scoped(ctx, scope)
	if !since.IsZero() {
		query = query.Where("occurred_at >= ?", since)
	}
	var events []types.LearningEvent
	if err := query.Order("occurred_at DESC").Limit(limit).Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (r *learningRepository) GetMastery(
	ctx context.Context, scope interfaces.LearningScope, slug string,
) (*types.MasteryState, error) {
	var state types.MasteryState
	err := r.scoped(ctx, scope).Where("slug = ?", slug).First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func (r *learningRepository) UpsertMastery(ctx context.Context, state *types.MasteryState) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "subject_id"},
			{Name: "knowledge_base_id"},
			{Name: "slug"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"logit", "evidence_count", "positive_count", "negative_count",
			"stability", "last_evidence_at", "first_seen_at", "updated_at",
		}),
	}).Create(state).Error
}

func (r *learningRepository) ListMastery(
	ctx context.Context, scope interfaces.LearningScope,
) ([]types.MasteryState, error) {
	var states []types.MasteryState
	err := r.scoped(ctx, scope).Order("updated_at DESC").Find(&states).Error
	return states, err
}

func (r *learningRepository) GetSubjectPrefs(
	ctx context.Context, tenantID uint64, subjectID string,
) (*types.LearningSubjectPrefs, error) {
	var prefs types.LearningSubjectPrefs
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).
		First(&prefs).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &prefs, nil
}

func (r *learningRepository) UpsertSubjectPrefs(ctx context.Context, prefs *types.LearningSubjectPrefs) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "subject_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"collect_disabled", "updated_at"}),
	}).Create(prefs).Error
}

func (r *learningRepository) UpsertEdge(ctx context.Context, edge *types.LearningEdge) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "knowledge_base_id"},
			{Name: "from_slug"},
			{Name: "to_slug"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"relation", "confidence", "source"}),
	}).Create(edge).Error
}

func (r *learningRepository) ListEdges(
	ctx context.Context, tenantID uint64, knowledgeBaseID string,
) ([]types.LearningEdge, error) {
	var edges []types.LearningEdge
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Order("from_slug ASC, to_slug ASC").
		Find(&edges).Error
	return edges, err
}

func (r *learningRepository) UpsertQuizItem(ctx context.Context, item *types.LearningQuizItem) error {
	if item.ID == "" {
		item.ID = uuid.New().String()
	}
	if item.Status == "" {
		item.Status = types.LearningQuizStatusActive
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"question", "options", "correct_key", "explanation", "chunk_refs", "status", "updated_at",
		}),
	}).Create(item).Error
}

func (r *learningRepository) ListQuizItems(
	ctx context.Context, tenantID uint64, knowledgeBaseID, slug string,
) ([]types.LearningQuizItem, error) {
	var items []types.LearningQuizItem
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND slug = ?",
			tenantID, knowledgeBaseID, slug,
		).
		Order("created_at ASC").
		Find(&items).Error
	return items, err
}

func (r *learningRepository) InsertAttempt(ctx context.Context, attempt *types.LearningQuizAttempt) error {
	if attempt.ID == "" {
		attempt.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(attempt).Error
}

func (r *learningRepository) ListAttempts(
	ctx context.Context, scope interfaces.LearningScope, slug string,
) ([]types.LearningQuizAttempt, error) {
	var attempts []types.LearningQuizAttempt
	err := r.scoped(ctx, scope).Where("slug = ?", slug).
		Order("answered_at ASC").
		Find(&attempts).Error
	return attempts, err
}

func (r *learningRepository) DeleteMastery(
	ctx context.Context, scope interfaces.LearningScope, slug string,
) error {
	return r.scoped(ctx, scope).Where("slug = ?", slug).
		Delete(&types.MasteryState{}).Error
}

func (r *learningRepository) ListAllMastery(ctx context.Context) ([]types.MasteryState, error) {
	var states []types.MasteryState
	err := r.db.WithContext(ctx).Find(&states).Error
	return states, err
}

func (r *learningRepository) ListBackfilledSlugs(
	ctx context.Context, scope interfaces.LearningScope,
) ([]string, error) {
	var slugs []string
	err := r.scoped(ctx, scope).
		Where("event_type = ?", types.LearningEventBackfillCite).
		Distinct("slug").
		Find(&slugs).Error
	return slugs, err
}

func (r *learningRepository) ListDocAffinityByScope(ctx context.Context, tenantID uint64, subjectID string) ([]types.MemoryDocAffinity, error) {
	var rows []types.MemoryDocAffinity
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).Find(&rows).Error
	return rows, err
}

func (r *learningRepository) GetQuizItemByID(ctx context.Context, tenantID uint64, itemID string) (*types.LearningQuizItem, error) {
	var item types.LearningQuizItem
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, itemID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *learningRepository) ListQuizItemsByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningQuizItem, error) {
	var items []types.LearningQuizItem
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).Find(&items).Error
	return items, err
}

func (r *learningRepository) ListDocAffinity(ctx context.Context) ([]types.MemoryDocAffinity, error) {
	var rows []types.MemoryDocAffinity
	err := r.db.WithContext(ctx).Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListTopicStats(ctx context.Context) ([]types.MemoryTopicStat, error) {
	var rows []types.MemoryTopicStat
	err := r.db.WithContext(ctx).Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListMapsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.MemoryWikiMap, error) {
	var rows []types.MemoryWikiMap
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).
		Order("updated_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListRecentEvents(ctx context.Context, scope interfaces.LearningScope, limit, offset int) ([]types.LearningEvent, int64, error) {
	var total int64
	if err := r.scoped(ctx, scope).Model(&types.LearningEvent{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var events []types.LearningEvent
	err := r.scoped(ctx, scope).
		Order("occurred_at DESC, created_at DESC").
		Limit(limit).Offset(offset).
		Find(&events).Error
	return events, total, err
}

func (r *learningRepository) ListEventsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.LearningEvent, error) {
	var rows []types.LearningEvent
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).
		Order("occurred_at ASC").
		Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListMasteryBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.MasteryState, error) {
	var rows []types.MasteryState
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).
		Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListAttemptsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.LearningQuizAttempt, error) {
	var rows []types.LearningQuizAttempt
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).
		Order("answered_at ASC").
		Find(&rows).Error
	return rows, err
}

func (r *learningRepository) DeleteLearningDataBySubject(ctx context.Context, tenantID uint64, subjectID string) error {
	return r.deleteLearningData(ctx, "tenant_id = ? AND subject_id = ?", tenantID, subjectID)
}

// DeleteLearningDataByKB removes every subject's learning data inside one
// knowledge base — the orphan sweep used when the KB itself is gone. The
// KB-shared quiz bank and edges are not personal and stay untouched, and
// subject-level opt-out prefs are orthogonal (subject scope, not KB scope).
func (r *learningRepository) DeleteLearningDataByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) error {
	return r.deleteLearningData(ctx, "tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID)
}

func (r *learningRepository) deleteLearningData(ctx context.Context, where string, args ...interface{}) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{
			"learning_events", "mastery_states", "memory_wiki_map", "learning_quiz_attempts",
		} {
			if err := tx.Table(table).Where(where, args...).Delete(nil).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *learningRepository) UpsertTopicMap(ctx context.Context, m *types.MemoryWikiMap) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "subject_id"},
			{Name: "knowledge_base_id"},
			{Name: "normalized_topic_key"},
			{Name: "slug"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"topic_label", "confidence", "decided_by", "updated_at"}),
	}).Create(m).Error
}
