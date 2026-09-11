package repository

import (
	"context"
	"errors"
	"sort"
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

// selfAssessEventTypes is the fixed self-assessment vocabulary: the five
// event types whose reason taxonomy carries maintenance semantics. Kept as
// an explicit list (not a LIKE prefix) so the query stays an IN-list the
// planner can constant-fold, and so a future self_assess_* type joins this
// table deliberately rather than silently.
var selfAssessEventTypes = []string{
	types.LearningEventSelfAssessUp,
	types.LearningEventSelfAssessDownAll,
	types.LearningEventSelfAssessDownDocGap,
	types.LearningEventSelfAssessDownDocUpdated,
	types.LearningEventSelfAssessDownQuizEasy,
}

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
	return r.database(ctx).
		Where(
			"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ?",
			scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID,
		)
}

func (r *learningRepository) AppendEvent(ctx context.Context, event *types.LearningEvent) error {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	return r.database(ctx).Create(event).Error
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

// ListEventsPaged is the replay reader: keyset pagination in the canonical
// deterministic order (occurred_at ASC, id ASC) — the id term is the
// tie-break that makes same-timestamp batches stable regardless of insert
// order, so a replay of the same history always folds the same sequence.
// The cursor is the last (occurred_at, id) of the previous page; an empty
// cursor starts from the beginning. A short page marks the end.
func (r *learningRepository) ListEventsPaged(
	ctx context.Context, scope interfaces.LearningScope,
	afterOccurredAt time.Time, afterID string, limit int,
) ([]types.LearningEvent, error) {
	if limit <= 0 {
		limit = defaultLearningEventLimit
	}
	query := r.scoped(ctx, scope)
	if afterID != "" || !afterOccurredAt.IsZero() {
		query = query.Where(
			"(occurred_at > ? OR (occurred_at = ? AND id > ?))",
			afterOccurredAt, afterOccurredAt, afterID,
		)
	}
	var events []types.LearningEvent
	if err := query.
		Order("occurred_at ASC, id ASC").
		Limit(limit).
		Find(&events).Error; err != nil {
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
	return r.database(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "subject_id"},
			{Name: "knowledge_base_id"},
			{Name: "slug"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"logit", "evidence_count", "positive_count", "negative_count",
			"stability", "last_evidence_at", "first_seen_at", "updated_at", "projection_version", "replay_hash",
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

func (r *learningRepository) ListLastActivity(ctx context.Context, scope interfaces.LearningScope) (map[string]time.Time, error) {
	// Select the original typed timestamp column. SQLite returns MAX(datetime)
	// as TEXT, which cannot be scanned directly into time.Time.
	latest := r.scoped(ctx, scope).Model(&types.LearningEvent{}).
		Where("event_type IN ?", types.HumanLearningEventTypes()).Select("slug, MAX(occurred_at)").Group("slug")
	var rows []types.LearningEvent
	err := r.scoped(ctx, scope).Model(&types.LearningEvent{}).
		Where("event_type IN ?", types.HumanLearningEventTypes()).
		Where("(slug, occurred_at) IN (?)", latest).Distinct("slug", "occurred_at").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := map[string]time.Time{}
	for _, row := range rows {
		result[row.Slug] = row.OccurredAt
	}
	return result, nil
}

func (r *learningRepository) ListSelfAssess(
	ctx context.Context, scope interfaces.LearningScope,
) (map[string]interfaces.SelfAssessMark, error) {
	// The visibility window rides the WHERE (an IN-list the planner
	// constant-folds, matching the file's no-LIKE-prefix convention), so the
	// fetch stays bounded by the window instead of the whole history.
	cutoff := time.Now().Add(-interfaces.SelfAssessVisibleWindow)
	var events []types.LearningEvent
	err := r.scoped(ctx, scope).
		Where("occurred_at >= ? AND event_type IN ?", cutoff, selfAssessEventTypes).
		Order("occurred_at DESC").
		Find(&events).Error
	if err != nil {
		return nil, err
	}
	out := map[string]interfaces.SelfAssessMark{}
	for _, ev := range events {
		if _, seen := out[ev.Slug]; seen {
			continue
		}
		direction := "down"
		if ev.Type == types.LearningEventSelfAssessUp {
			direction = "up"
		}
		out[ev.Slug] = interfaces.SelfAssessMark{Direction: direction, EventType: ev.Type, At: ev.OccurredAt}
	}
	return out, nil
}

// ListActiveDays returns the distinct event days (YYYY-MM-DD, server
// calendar) since the given time. The date expression is dialect-switched —
// the two stores WeKnora ships (PostgreSQL and SQLite) spell it differently
// and the repository layer owns that difference so callers see plain days.
func (r *learningRepository) ListActiveDays(
	ctx context.Context, scope interfaces.LearningScope, since time.Time,
) ([]string, error) {
	dayExpr := "strftime('%Y-%m-%d', occurred_at)"
	if r.db.Dialector.Name() == "postgres" {
		dayExpr = "to_char(occurred_at, 'YYYY-MM-DD')"
	}
	var days []string
	err := r.scoped(ctx, scope).
		Model(&types.LearningEvent{}).
		Where("event_type IN ?", types.HumanLearningEventTypes()).
		Where("occurred_at >= ?", since).
		Select("DISTINCT " + dayExpr).
		Scan(&days).Error
	if err != nil {
		return nil, err
	}
	sort.Strings(days)
	return days, nil
}

func (r *learningRepository) GetSubjectPrefs(
	ctx context.Context, subjectID string,
) (*types.LearningSubjectPrefs, error) {
	// Subject-scoped by design: the KB access guard rewrites the request's
	// effective tenant for shared KBs, so collection/setting reads under a
	// shared KB must not scope by that tenant or the person's home-tenant
	// opt-out row would vanish. When several rows exist, a disabled row
	// wins (data-sovereignty-first), then the most recently updated.
	var prefs types.LearningSubjectPrefs
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Order("collect_disabled DESC, updated_at DESC").
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
	return r.database(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockLearningSubject(tx, prefs.SubjectID); err != nil {
			return err
		}
		if err := tx.Model(&types.LearningSubjectEpoch{}).Where("subject_id = ?", prefs.SubjectID).Updates(map[string]interface{}{"epoch": gorm.Expr("epoch + 1"), "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		// Consent belongs to the person, including rows stored under shared KB owners.
		if err := tx.Model(&types.LearningSubjectPrefs{}).Where("subject_id = ?", prefs.SubjectID).Update("collect_disabled", prefs.CollectDisabled).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "subject_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{"collect_disabled", "updated_at"}),
		}).Create(prefs).Error
	})
}

func (r *learningRepository) UpsertEdge(ctx context.Context, edge *types.LearningEdge) error {
	return r.database(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "knowledge_base_id"},
			{Name: "from_slug"},
			{Name: "to_slug"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"relation", "confidence", "source"}),
	}).Create(edge).Error
}

// ListAllEdges returns every stored edge, ordered for deterministic walks.
func (r *learningRepository) ListAllEdges(ctx context.Context) ([]types.LearningEdge, error) {
	var edges []types.LearningEdge
	err := r.database(ctx).
		Order("tenant_id ASC, knowledge_base_id ASC, from_slug ASC, to_slug ASC").
		Find(&edges).Error
	return edges, err
}

// DeleteEdge removes one stored prerequisite edge (absent row = no-op).
func (r *learningRepository) DeleteEdge(
	ctx context.Context, tenantID uint64, knowledgeBaseID, fromSlug, toSlug string,
) error {
	return r.database(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND from_slug = ? AND to_slug = ?",
			tenantID, knowledgeBaseID, fromSlug, toSlug,
		).
		Delete(&types.LearningEdge{}).Error
}

func (r *learningRepository) ListEdges(
	ctx context.Context, tenantID uint64, knowledgeBaseID string,
) ([]types.LearningEdge, error) {
	var edges []types.LearningEdge
	err := r.database(ctx).
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
	result := r.database(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: "learning_quiz_items", Name: "tenant_id"}, Value: item.TenantID},
			clause.Eq{Column: clause.Column{Table: "learning_quiz_items", Name: "knowledge_base_id"}, Value: item.KnowledgeBaseID},
		}},
		DoUpdates: clause.AssignmentColumns([]string{
			"question", "options", "correct_key", "explanation", "chunk_refs", "evidence_hash",
			"objective_id", "family_id", "objective_version", "content_version", "rubric_version", "scorer_version",
			"assistance_mode", "family_fingerprint", "reviewer", "published_at", "review_note",
			"change_kind", "status", "updated_at",
		}),
	}).Create(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("learning: content id belongs to another scope")
	}
	return nil
}

// ListObjectives returns the KB's objective definitions, deterministic
// order (slug, id) so derivations and exports are reproducible.
func (r *learningRepository) ListObjectives(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningObjective, error) {
	var rows []types.LearningObjective
	err := r.database(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Order("slug ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// UpsertObjective inserts or updates one objective definition by id. The
// definition is KB-shared inventory: subject deletes never touch it.
func (r *learningRepository) UpsertObjective(ctx context.Context, objective *types.LearningObjective) error {
	if objective.ID == "" {
		return errors.New("learning: objective id required")
	}
	if objective.Status == "" {
		objective.Status = types.LearningObjectiveStatusDraft
	}
	if objective.ContractType == "" {
		objective.ContractType = types.ObjectiveContractConceptTwoFamily
	}
	if objective.ContractVersion == "" {
		objective.ContractVersion = types.ObjectiveContractVersion
	}
	result := r.database(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: "learning_objectives", Name: "tenant_id"}, Value: objective.TenantID},
			clause.Eq{Column: clause.Column{Table: "learning_objectives", Name: "knowledge_base_id"}, Value: objective.KnowledgeBaseID},
		}},
		DoUpdates: clause.AssignmentColumns([]string{
			"slug", "title", "behavior", "capability_type",
			"contract_type", "contract_params", "contract_version",
			"source_refs", "content_version", "evidence_hash", "reviewer", "published_at",
			"review_note", "change_kind", "status", "prereq_objective_id", "updated_at",
		}),
	}).Create(objective)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("learning: content id belongs to another scope")
	}
	return nil
}

// GetTaskByID resolves one structured task.
func (r *learningRepository) GetTaskByID(ctx context.Context, tenantID uint64, taskID string) (*types.LearningTask, error) {
	var t types.LearningTask
	err := r.database(ctx).Where("tenant_id = ? AND id = ?", tenantID, taskID).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks returns the KB's tasks in deterministic (slug, id) order.
func (r *learningRepository) ListTasks(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningTask, error) {
	var rows []types.LearningTask
	err := r.database(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).
		Order("slug ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// UpsertTask stores one task by id.
func (r *learningRepository) UpsertTask(ctx context.Context, t *types.LearningTask) error {
	if t.ID == "" {
		return errors.New("learning: task id required")
	}
	if t.Status == "" {
		t.Status = types.LearningQuizStatusDraft
	}
	result := r.database(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: "learning_tasks", Name: "tenant_id"}, Value: t.TenantID},
			clause.Eq{Column: clause.Column{Table: "learning_tasks", Name: "knowledge_base_id"}, Value: t.KnowledgeBaseID},
		}},
		DoUpdates: clause.AssignmentColumns([]string{
			"slug", "objective_id", "family_id", "title", "scenario", "fields", "answer_key",
			"critical_checks", "source_refs", "evidence_hash", "objective_version", "content_version", "rubric_version", "scorer_version",
			"assistance_mode", "reviewer", "published_at", "review_note", "change_kind", "status", "updated_at",
		}),
	}).Create(t)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("learning: content id belongs to another scope")
	}
	return nil
}

// InsertTaskAttempt stores one graded task submission.
func (r *learningRepository) InsertTaskAttempt(ctx context.Context, a *types.LearningTaskAttempt) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return r.database(ctx).Create(a).Error
}

// ListTaskAttempts returns the subject's task submissions in one KB,
// oldest first — the derivation input for task contracts.
func (r *learningRepository) ListTaskAttempts(ctx context.Context, scope interfaces.LearningScope) ([]types.LearningTaskAttempt, error) {
	var rows []types.LearningTaskAttempt
	err := r.scoped(ctx, scope).Order("submitted_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

// ListTaskAttemptsBySubject is the export-side variant.
func (r *learningRepository) ListTaskAttemptsBySubject(ctx context.Context, subjectID string) ([]types.LearningTaskAttempt, error) {
	var rows []types.LearningTaskAttempt
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Order("submitted_at ASC").
		Find(&rows).Error
	return rows, err
}

// StaleQuizItemsByEvidence auto-invalidates the slug's active items whose
// evidence version no longer matches: every active row whose evidence_hash
// differs from currentHash (legacy empty hashes included — "version
// unknown" cannot be trusted) flips to the stale status, which stops quiz
// serving and scoring until regeneration re-adjudicates against the new
// material. Human-disabled rows are never touched. Returns how many rows
// went stale.
func (r *learningRepository) StaleQuizItemsByEvidence(
	ctx context.Context, tenantID uint64, knowledgeBaseID, slug, currentHash string,
) (int64, error) {
	res := r.database(ctx).
		Model(&types.LearningQuizItem{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND slug = ? AND status = ? AND evidence_hash <> ?",
			tenantID, knowledgeBaseID, slug, types.LearningQuizStatusActive, currentHash,
		).
		Updates(map[string]interface{}{
			"status":     types.LearningQuizStatusStale,
			"updated_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

func (r *learningRepository) ListQuizItems(
	ctx context.Context, tenantID uint64, knowledgeBaseID, slug string,
) ([]types.LearningQuizItem, error) {
	var items []types.LearningQuizItem
	err := r.database(ctx).
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
	return r.database(ctx).Create(attempt).Error
}

func (r *learningRepository) ListAttempts(
	ctx context.Context, scope interfaces.LearningScope, slug string,
) ([]types.LearningQuizAttempt, error) {
	var attempts []types.LearningQuizAttempt
	query := r.scoped(ctx, scope)
	if slug != "" {
		query = query.Where("slug = ?", slug)
	}
	err := query.Order("answered_at ASC, id ASC").Find(&attempts).Error
	return attempts, err
}

func (r *learningRepository) ListCorrectAttempts(
	ctx context.Context, scope interfaces.LearningScope,
) ([]types.LearningQuizAttempt, error) {
	var attempts []types.LearningQuizAttempt
	err := r.scoped(ctx, scope).Where("is_correct = ?", true).
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

// MigrateMastery moves one (subject, node) fold from one slug to another in
// a single transaction: the denormalized quiz attempts follow the node
// (otherwise the direct-evidence facts of the renamed node stop matching its
// history), and the source row is retired — a crash between the two writes
// can no longer leave the same person's mastery described by two rows, nor
// the target row's evidence stranded under the stale slug.
func (r *learningRepository) MigrateMastery(
	ctx context.Context, scope interfaces.LearningScope, fromSlug, toSlug string, state *types.MasteryState,
) error {
	return r.database(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "subject_id"},
				{Name: "knowledge_base_id"},
				{Name: "slug"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"logit", "evidence_count", "positive_count", "negative_count",
				"stability", "last_evidence_at", "first_seen_at", "updated_at", "projection_version", "replay_hash",
			}),
		}).Create(state).Error; err != nil {
			return err
		}
		if err := tx.Model(&types.LearningQuizAttempt{}).
			Where(
				"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ? AND slug = ?",
				scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, fromSlug,
			).
			Updates(map[string]interface{}{"original_slug": gorm.Expr("CASE WHEN original_slug = '' THEN slug ELSE original_slug END"), "slug": toSlug}).Error; err != nil {
			return err
		}
		if err := tx.Model(&types.LearningEvent{}).Where("tenant_id = ? AND subject_id = ? AND knowledge_base_id = ? AND slug = ?", scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, fromSlug).Updates(map[string]interface{}{
			"original_slug": gorm.Expr("CASE WHEN original_slug = '' THEN slug ELSE original_slug END"), "slug": toSlug,
		}).Error; err != nil {
			return err
		}
		return tx.Where(
			"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ? AND slug = ?",
			scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, fromSlug,
		).Delete(&types.MasteryState{}).Error
	})
}

// MoveSkip relocates one skip declaration onto a live slug atomically:
// re-declare at the target (preserving the original declaration age) and
// retire the source in one transaction, so a rename can never leave the
// recommendation the user retired resurrected by a half-applied move.
func (r *learningRepository) MoveSkip(
	ctx context.Context, scope interfaces.LearningScope, fromSlug, toSlug string, createdAt time.Time,
) error {
	return r.database(ctx).Transaction(func(tx *gorm.DB) error {
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"},
				{Name: "subject_id"},
				{Name: "knowledge_base_id"},
				{Name: "slug"},
			},
			DoNothing: true,
		}).Create(&types.LearningSkip{
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: scope.KnowledgeBaseID,
			Slug:            toSlug,
			CreatedAt:       createdAt,
		}).Error; err != nil {
			return err
		}
		return tx.Where(
			"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ? AND slug = ?",
			scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, fromSlug,
		).Delete(&types.LearningSkip{}).Error
	})
}

func (r *learningRepository) ListAllMastery(ctx context.Context) ([]types.MasteryState, error) {
	var states []types.MasteryState
	err := r.database(ctx).Find(&states).Error
	return states, err
}

func (r *learningRepository) BackfillDone(
	ctx context.Context, scope interfaces.LearningScope,
) (bool, error) {
	var count int64
	err := r.database(ctx).Model(&types.LearningBackfillMark{}).
		Where(
			"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ?",
			scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID,
		).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *learningRepository) MarkBackfillDone(ctx context.Context, scope interfaces.LearningScope) error {
	return r.database(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "knowledge_base_id"}},
		DoNothing: true,
	}).Create(&types.LearningBackfillMark{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: scope.KnowledgeBaseID,
		CompletedAt:     time.Now(),
	}).Error
}

func (r *learningRepository) ListDocAffinityByScope(ctx context.Context, tenantID uint64, subjectID string) ([]types.MemoryDocAffinity, error) {
	var rows []types.MemoryDocAffinity
	err := r.database(ctx).Where("tenant_id = ? AND subject_id = ?", tenantID, subjectID).Find(&rows).Error
	return rows, err
}

func (r *learningRepository) GetQuizItemByID(ctx context.Context, tenantID uint64, itemID string) (*types.LearningQuizItem, error) {
	var item types.LearningQuizItem
	err := r.database(ctx).Where("tenant_id = ? AND id = ?", tenantID, itemID).First(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *learningRepository) ListQuizItemsByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningQuizItem, error) {
	var items []types.LearningQuizItem
	err := r.database(ctx).Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID).Find(&items).Error
	return items, err
}

func (r *learningRepository) ListDocAffinity(ctx context.Context) ([]types.MemoryDocAffinity, error) {
	var rows []types.MemoryDocAffinity
	err := r.database(ctx).Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListTopicStats(ctx context.Context) ([]types.MemoryTopicStat, error) {
	var rows []types.MemoryTopicStat
	err := r.database(ctx).Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListMapsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.MemoryWikiMap, error) {
	var rows []types.MemoryWikiMap
	err := r.database(ctx).
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

func (r *learningRepository) ListEventsBySubject(ctx context.Context, subjectID string) ([]types.LearningEvent, error) {
	// Subject-scoped: shared-KB learning rows land under the KB owner's
	// effective tenant, and the export must cover every row that belongs
	// to the person regardless of which workspace it was collected in.
	var rows []types.LearningEvent
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Order("occurred_at ASC").
		Find(&rows).Error
	return rows, err
}

// ListAllMapsBySubject is the export-side, subject-scoped variant of
// ListMapsBySubject: the export covers every workspace, while the
// maintenance pass keeps its per-tenant scope (it reconciles one scope's
// settled set at a time).
func (r *learningRepository) ListAllMapsBySubject(ctx context.Context, subjectID string) ([]types.MemoryWikiMap, error) {
	var rows []types.MemoryWikiMap
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Order("updated_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListMasteryBySubject(ctx context.Context, subjectID string) ([]types.MasteryState, error) {
	var rows []types.MasteryState
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Find(&rows).Error
	return rows, err
}

func (r *learningRepository) ListAttemptsBySubject(ctx context.Context, subjectID string) ([]types.LearningQuizAttempt, error) {
	var rows []types.LearningQuizAttempt
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Order("answered_at ASC").
		Find(&rows).Error
	return rows, err
}

// DeleteLearningDataByKB removes every subject's learning data inside one
// knowledge base — the orphan sweep used when the KB itself is gone. The
// KB-shared quiz bank and edges are not personal and stay untouched, and
// subject-level opt-out prefs are orthogonal (subject scope, not KB scope).
// The backfill marks go WITH the KB: for a dead KB the affinity orphan guard
// already blocks any re-run, so deleting them here is pure tidiness; keeping
// them in the subject-level delete above would undo its resurrection guard.
func (r *learningRepository) DeleteLearningDataByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) error {
	return r.deleteLearningData(ctx, []string{
		"learning_events", "mastery_states", "memory_wiki_map", "learning_quiz_attempts",
		"learning_backfill_marks", "learning_task_attempts", "learning_skips", "learning_plan_preferences",
	}, "tenant_id = ? AND knowledge_base_id = ?", tenantID, knowledgeBaseID)
}

func (r *learningRepository) DeleteLearningDataBySubject(ctx context.Context, subjectID string) error {
	// Subject-scoped on purpose: shared-KB collection lands under the KB
	// owner's effective tenant, and "delete my learning profile" must
	// remove every such row — a tenant predicate here would leave the
	// caller's rows in every workspace their shared KBs belong to.
	// learning_backfill_marks is deliberately absent: it is the tombstone
	// that keeps a deleted profile from being re-backfilled to life on the
	// next startup. learning_skips IS personal data and goes with the rest.
	return r.deleteLearningData(ctx, []string{
		"learning_events", "mastery_states", "memory_wiki_map", "learning_quiz_attempts",
		"learning_task_attempts", "learning_skips", "learning_plan_preferences",
	}, "subject_id = ?", subjectID)
}

// GetSubjectEpoch returns the subject's deletion generation. An absent row
// is epoch 0 — no profile deletion has ever fenced this subject's writes.
func (r *learningRepository) GetSubjectEpoch(ctx context.Context, subjectID string) (int64, error) {
	var row types.LearningSubjectEpoch
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return row.Epoch, nil
}

// DeleteProfileData is the atomic "delete my learning profile": the
// personal-data sweep, the collection opt-out and the deletion-epoch bump
// commit as one transaction. The epoch bump is what fences background
// writers that captured the old epoch before a long-running model call —
// their writes land after the delete and are discarded instead of
// resurrecting the profile. learning_backfill_marks is deliberately kept
// (the re-backfill tombstone), matching DeleteLearningDataBySubject.
func (r *learningRepository) DeleteProfileData(
	ctx context.Context, tenantID uint64, subjectID string, optOut bool,
) error {
	return r.database(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockLearningSubject(tx, subjectID); err != nil {
			return err
		}
		for _, table := range []string{
			"learning_events", "mastery_states", "memory_wiki_map", "learning_quiz_attempts",
			"learning_task_attempts", "learning_skips", "learning_plan_preferences",
		} {
			if err := tx.Table(table).Where("subject_id = ?", subjectID).Delete(nil).Error; err != nil {
				return err
			}
		}
		if optOut {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "tenant_id"}, {Name: "subject_id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"collect_disabled", "updated_at",
				}),
			}).Create(&types.LearningSubjectPrefs{
				TenantID: tenantID, SubjectID: subjectID,
				CollectDisabled: true, UpdatedAt: time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		now := time.Now()
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "subject_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"epoch":      gorm.Expr("learning_subject_epochs.epoch + 1"),
				"updated_at": now,
			}),
		}).Create(&types.LearningSubjectEpoch{
			SubjectID: subjectID, Epoch: 1, UpdatedAt: now,
		}).Error
	})
}

// ApplyTopicMapping stores one adjudicated topic mapping together with its
// optional projection event and mastery fold, fenced on the subject's
// deletion epoch. The epoch row is read under FOR UPDATE on PostgreSQL (a
// concurrent DeleteProfileData blocks until this transaction commits, so
// delete-then-write and write-then-delete serialize); SQLite serializes
// writers itself, where the plain read inside the transaction carries the
// same guarantee. The fold closure runs inside the transaction and receives
// the live mastery row (nil when unseen), keeping the fold algorithm in the
// service layer while the transaction boundary stays here.
func (r *learningRepository) ApplyTopicMapping(
	ctx context.Context, expectedEpoch int64, scope interfaces.LearningScope,
	mapping *types.MemoryWikiMap, event *types.LearningEvent,
	fold func(existing *types.MasteryState) types.MasteryState,
) error {
	if mapping == nil {
		return errors.New("learning: ApplyTopicMapping requires a mapping")
	}
	return r.WithSubject(ctx, scope.SubjectID, expectedEpoch, true, func(ctx context.Context) error {
		tx := r.database(ctx)
		if mapping != nil {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "tenant_id"},
					{Name: "subject_id"},
					{Name: "knowledge_base_id"},
					{Name: "normalized_topic_key"},
					{Name: "slug"},
				},
				DoUpdates: clause.AssignmentColumns([]string{
					"topic_label", "confidence", "decided_by", "updated_at",
				}),
			}).Create(mapping).Error; err != nil {
				return err
			}
		}
		if event != nil {
			var count int64
			if err := tx.Model(&types.LearningEvent{}).Where("tenant_id = ? AND subject_id = ? AND knowledge_base_id = ? AND slug = ? AND event_type = ? AND occurred_at >= ?", scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, mapping.Slug, types.LearningEventTopicSignal, event.OccurredAt.Add(-48*time.Hour)).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				event = nil
				fold = nil
			}
		}
		if event != nil {
			if event.ID == "" {
				event.ID = uuid.New().String()
			}
			if err := tx.Create(event).Error; err != nil {
				return err
			}
		}
		if fold != nil {
			var row types.MasteryState
			err := tx.Where(
				"tenant_id = ? AND subject_id = ? AND knowledge_base_id = ? AND slug = ?",
				scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, mapping.Slug,
			).First(&row).Error
			var existing *types.MasteryState
			if err == nil {
				existing = &row
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			next := fold(existing)
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "tenant_id"},
					{Name: "subject_id"},
					{Name: "knowledge_base_id"},
					{Name: "slug"},
				},
				DoUpdates: clause.AssignmentColumns([]string{
					"logit", "evidence_count", "positive_count", "negative_count",
					"stability", "last_evidence_at", "first_seen_at", "updated_at", "projection_version", "replay_hash",
				}),
			}).Create(&next).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *learningRepository) deleteLearningData(ctx context.Context, tables []string, where string, args ...interface{}) error {
	return r.database(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range tables {
			if err := tx.Table(table).Where(where, args...).Delete(nil).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *learningRepository) UpsertTopicMap(ctx context.Context, m *types.MemoryWikiMap) error {
	return r.database(ctx).Clauses(clause.OnConflict{
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

// ListMasteryByKB is the knowledge-health aggregate's one cross-subject
// read: every person's folded rows of one KB, ordered (subject, slug) so
// downstream aggregation is input-order independent. Unlike the scoped()
// queries it carries no subject predicate — the route above it is
// owner/admin gated and no handler parameter selects a subject at all,
// which keeps this the same containment story told from the other end.
func (r *learningRepository) ListMasteryByKB(
	ctx context.Context, tenantID uint64, kbID string,
) ([]types.MasteryState, error) {
	var states []types.MasteryState
	err := r.database(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Order("subject_id ASC, slug ASC").
		Find(&states).Error
	return states, err
}

// ListMaintenanceMarks feeds the health view's content-work queue: the
// KB's self-assessment events inside the visibility window, newest first.
// Tenant and KB predicates are explicit like every query here; the
// event-type IN-list is the maintenance vocabulary, not a filter a caller
// controls.
func (r *learningRepository) ListMaintenanceMarks(
	ctx context.Context, tenantID uint64, kbID string, since time.Time,
) ([]types.LearningEvent, error) {
	var events []types.LearningEvent
	err := r.database(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND occurred_at >= ? AND event_type IN ?",
			tenantID, kbID, since, selfAssessEventTypes,
		).
		Order("occurred_at DESC").
		Find(&events).Error
	return events, err
}

// AddSkip records one standing skip declaration. On conflict the row is
// kept as-is (a re-declare must not rewrite the original date); the
// createdAt parameter exists for the alias-reconcile move, which must
// preserve the declaration's age across a rename.
func (r *learningRepository) AddSkip(ctx context.Context, scope interfaces.LearningScope, slug string, createdAt time.Time) error {
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return r.database(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "subject_id"},
			{Name: "knowledge_base_id"},
			{Name: "slug"},
		},
		DoNothing: true,
	}).Create(&types.LearningSkip{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug:            slug,
		CreatedAt:       createdAt,
	}).Error
}

// RemoveSkip revokes one skip declaration; removing an absent row is the
// no-op the idempotent UI flow needs.
func (r *learningRepository) RemoveSkip(ctx context.Context, scope interfaces.LearningScope, slug string) error {
	return r.scoped(ctx, scope).Where("slug = ?", slug).
		Delete(&types.LearningSkip{}).Error
}

// ListSkips returns the scope's skip declarations as slug → declared at.
func (r *learningRepository) ListSkips(
	ctx context.Context, scope interfaces.LearningScope,
) (map[string]time.Time, error) {
	var rows []types.LearningSkip
	if err := r.scoped(ctx, scope).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		out[row.Slug] = row.CreatedAt
	}
	return out, nil
}

// ListSkipsBySubject is the export-side variant: subject-scoped across
// every workspace, same containment story as ListEventsBySubject.
func (r *learningRepository) ListSkipsBySubject(ctx context.Context, subjectID string) ([]types.LearningSkip, error) {
	var rows []types.LearningSkip
	// created_at ties (same-second declarations, bulk alias moves) break
	// deterministically so the export is reproducible.
	err := r.database(ctx).
		Where("subject_id = ?", subjectID).
		Order("created_at ASC, tenant_id ASC, knowledge_base_id ASC, slug ASC").
		Find(&rows).Error
	return rows, err
}

// ListAllSkips returns every skip row — the reconcile pass's skip-alias
// repair needs subjects that skipped nodes without ever folding mastery.
func (r *learningRepository) ListAllSkips(ctx context.Context) ([]types.LearningSkip, error) {
	var rows []types.LearningSkip
	err := r.database(ctx).
		Order("tenant_id ASC, knowledge_base_id ASC, subject_id ASC, slug ASC").
		Find(&rows).Error
	return rows, err
}
