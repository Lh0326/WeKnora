package learning

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubLearningRepo records everything the collector writes, in memory.
// Unimplemented interface methods panic loudly if a test strays into them,
// which is exactly what a test seam should do.
type stubLearningRepo struct {
	interfaces.LearningRepository

	txMu         sync.Mutex
	mu           sync.Mutex
	events       []types.LearningEvent
	mastery      map[string]*types.MasteryState // key: tenant|subject|kb|slug
	deleted      []string
	kbSweeps     [][2]interface{} // [tenantID, kbID] of orphan-sweep calls
	prefs        map[string]*types.LearningSubjectPrefs
	planPrefs    map[interfaces.LearningScope]types.LearningPlanPreference
	affinity     []types.MemoryDocAffinity
	topicStats   []types.MemoryTopicStat
	maps         map[string]*types.MemoryWikiMap
	edges        []types.LearningEdge
	quizItems    []types.LearningQuizItem
	quizAttempts []types.LearningQuizAttempt
	// backfillMarks mirrors learning_backfill_marks: subject-level deletes
	// leave it alone (the resurrection guard), the KB sweep clears it.
	backfillMarks map[string]bool
	// skips mirrors learning_skips, keyed like mastery: tenant|subject|kb|slug.
	skips      map[string]time.Time
	prefsHits  atomic.Int32
	appends    atomic.Int32
	applyCalls atomic.Int32
	// masteryUpserts counts UpsertMastery calls so no-op guarantees (the
	// fold-drift audit must not rewrite converged state) are assertable.
	masteryUpserts atomic.Int32
	// epoch mirrors learning_subject_epochs: subject → deletion generation.
	epoch map[string]int64
	// objectives mirrors learning_objectives (KB-shared definitions).
	objectives []types.LearningObjective
	// tasks/taskAttempts mirror learning_tasks / learning_task_attempts.
	tasks        []types.LearningTask
	taskAttempts []types.LearningTaskAttempt
	// deleteErr / applyErr are fault-injection hooks for the delete-atomicity
	// and epoch-fence tests; nil means "never fail".
	deleteErr func() error
	applyErr  func() error
}

func newStubRepo() *stubLearningRepo {
	return &stubLearningRepo{
		mastery:       map[string]*types.MasteryState{},
		prefs:         map[string]*types.LearningSubjectPrefs{},
		backfillMarks: map[string]bool{},
		epoch:         map[string]int64{},
	}
}

func (s *stubLearningRepo) AppendEvent(_ context.Context, e *types.LearningEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Respect a caller-provided id like the real repository does — the
	// keyset reader's tie-break assumes unique ids, and time-minted ids
	// can collide inside one test loop on coarse clocks.
	if e.ID == "" {
		e.ID = "ev-auto-" + strconv.Itoa(len(s.events)+1)
	}
	s.events = append(s.events, *e)
	s.appends.Add(1)
	return nil
}

func (s *stubLearningRepo) ListEvents(_ context.Context, scope interfaces.LearningScope, since time.Time, _ int) ([]types.LearningEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningEvent
	for _, e := range s.events {
		if e.TenantID != scope.TenantID || e.SubjectID != scope.SubjectID || e.KnowledgeBaseID != scope.KnowledgeBaseID {
			continue
		}
		if !since.IsZero() && e.OccurredAt.Before(since) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// ListEventsPaged mirrors the keyset reader: canonical (occurred_at, id)
// order regardless of the in-memory slice's append order.
func (s *stubLearningRepo) ListEventsPaged(_ context.Context, scope interfaces.LearningScope, after time.Time, afterID string, limit int) ([]types.LearningEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 500
	}
	matches := func(e types.LearningEvent) bool {
		if e.TenantID != scope.TenantID || e.SubjectID != scope.SubjectID || e.KnowledgeBaseID != scope.KnowledgeBaseID {
			return false
		}
		if afterID == "" && after.IsZero() {
			return true
		}
		if !e.OccurredAt.Equal(after) {
			return e.OccurredAt.After(after)
		}
		return e.ID > afterID
	}
	var scoped []types.LearningEvent
	for _, e := range s.events {
		if matches(e) {
			scoped = append(scoped, e)
		}
	}
	sort.Slice(scoped, func(i, j int) bool {
		if !scoped[i].OccurredAt.Equal(scoped[j].OccurredAt) {
			return scoped[i].OccurredAt.Before(scoped[j].OccurredAt)
		}
		return scoped[i].ID < scoped[j].ID
	})
	if len(scoped) > limit {
		scoped = scoped[:limit]
	}
	return scoped, nil
}

// MigrateMastery mirrors the atomic migration: upsert target, re-tag
// attempts, retire source.
func (s *stubLearningRepo) MigrateMastery(_ context.Context, scope interfaces.LearningScope, fromSlug, toSlug string, state *types.MasteryState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := *state
	s.mastery[s.masteryKey(scope, toSlug)] = &stored
	s.masteryUpserts.Add(1)
	for i := range s.quizAttempts {
		if s.quizAttempts[i].SubjectID == scope.SubjectID &&
			s.quizAttempts[i].KnowledgeBaseID == scope.KnowledgeBaseID &&
			s.quizAttempts[i].Slug == fromSlug {
			if s.quizAttempts[i].OriginalSlug == "" {
				s.quizAttempts[i].OriginalSlug = s.quizAttempts[i].Slug
			}
			s.quizAttempts[i].Slug = toSlug
		}
	}
	for i := range s.events {
		e := &s.events[i]
		if e.TenantID == scope.TenantID && e.SubjectID == scope.SubjectID && e.KnowledgeBaseID == scope.KnowledgeBaseID && e.Slug == fromSlug {
			if e.OriginalSlug == "" {
				e.OriginalSlug = e.Slug
			}
			e.Slug = toSlug
		}
	}
	delete(s.mastery, s.masteryKey(scope, fromSlug))
	s.deleted = append(s.deleted, fromSlug) // same trace DeleteMastery leaves
	return nil
}

// MoveSkip mirrors the atomic skip relocation, preserving the age.
func (s *stubLearningRepo) MoveSkip(_ context.Context, scope interfaces.LearningScope, fromSlug, toSlug string, createdAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.masteryKey(scope, fromSlug)
	if _, ok := s.skips[key]; !ok {
		return nil // nothing to move
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if _, exists := s.skips[s.masteryKey(scope, toSlug)]; !exists {
		s.skips[s.masteryKey(scope, toSlug)] = createdAt
	}
	delete(s.skips, key)
	return nil
}

func (s *stubLearningRepo) masteryKey(scope interfaces.LearningScope, slug string) string {
	return strconv.FormatUint(scope.TenantID, 10) + "|" + scope.SubjectID + "|" + scope.KnowledgeBaseID + "|" + slug
}

func (s *stubLearningRepo) GetMastery(_ context.Context, scope interfaces.LearningScope, slug string) (*types.MasteryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.mastery[s.masteryKey(scope, slug)]; ok {
		clone := *row
		return &clone, nil
	}
	return nil, nil
}

func (s *stubLearningRepo) UpsertMastery(_ context.Context, state *types.MasteryState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.masteryUpserts.Add(1)
	clone := *state
	s.mastery[s.masteryKey(interfaces.LearningScope{
		TenantID: state.TenantID, SubjectID: state.SubjectID, KnowledgeBaseID: state.KnowledgeBaseID,
	}, state.Slug)] = &clone
	return nil
}

func (s *stubLearningRepo) DeleteMastery(_ context.Context, scope interfaces.LearningScope, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mastery, s.masteryKey(scope, slug))
	s.deleted = append(s.deleted, slug)
	return nil
}

func (s *stubLearningRepo) ListAllMastery(_ context.Context) ([]types.MasteryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.MasteryState, 0, len(s.mastery))
	for _, row := range s.mastery {
		out = append(out, *row)
	}
	return out, nil
}

func (s *stubLearningRepo) scopeKey(scope interfaces.LearningScope) string {
	return strconv.FormatUint(scope.TenantID, 10) + "|" + scope.SubjectID + "|" + scope.KnowledgeBaseID
}

func (s *stubLearningRepo) ListActiveDays(_ context.Context, scope interfaces.LearningScope, since time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, e := range s.events {
		if !types.IsHumanLearningEvent(e.Type) {
			continue
		}
		if e.TenantID != scope.TenantID || e.SubjectID != scope.SubjectID || e.KnowledgeBaseID != scope.KnowledgeBaseID {
			continue
		}
		if !since.IsZero() && e.OccurredAt.Before(since) {
			continue
		}
		d := e.OccurredAt.Format("2006-01-02")
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *stubLearningRepo) BackfillDone(_ context.Context, scope interfaces.LearningScope) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backfillMarks[s.scopeKey(scope)], nil
}

func (s *stubLearningRepo) MarkBackfillDone(_ context.Context, scope interfaces.LearningScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backfillMarks[s.scopeKey(scope)] = true
	return nil
}

func (s *stubLearningRepo) ListDocAffinityByScope(_ context.Context, tenantID uint64, subjectID string) ([]types.MemoryDocAffinity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.MemoryDocAffinity
	for _, a := range s.affinity {
		if a.TenantID == tenantID && a.SubjectID == subjectID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) GetQuizItemByID(_ context.Context, tenantID uint64, itemID string) (*types.LearningQuizItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.quizItems {
		if s.quizItems[i].TenantID == tenantID && s.quizItems[i].ID == itemID {
			clone := s.quizItems[i]
			return &clone, nil
		}
	}
	return nil, nil
}

func (s *stubLearningRepo) ListQuizItemsByKB(_ context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningQuizItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningQuizItem
	for _, it := range s.quizItems {
		if it.TenantID == tenantID && it.KnowledgeBaseID == knowledgeBaseID {
			clone := it
			out = append(out, clone)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListDocAffinity(_ context.Context) ([]types.MemoryDocAffinity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.MemoryDocAffinity{}, s.affinity...), nil
}

func (s *stubLearningRepo) ListTopicStats(_ context.Context) ([]types.MemoryTopicStat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.MemoryTopicStat{}, s.topicStats...), nil
}

func (s *stubLearningRepo) UpsertTopicMap(_ context.Context, m *types.MemoryWikiMap) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maps == nil {
		s.maps = map[string]*types.MemoryWikiMap{}
	}
	clone := *m
	s.maps[m.NormalizedTopicKey+"→"+m.Slug] = &clone
	return nil
}

func (s *stubLearningRepo) UpsertQuizItem(_ context.Context, item *types.LearningQuizItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ID == "" {
		item.ID = "quiz-" + strconv.Itoa(len(s.quizItems)+1)
	}
	// Real-repository upsert semantics: replace by id, append when new.
	// (A stub that only appends makes review-lifecycle tests see stale
	// duplicate rows.)
	for i := range s.quizItems {
		if s.quizItems[i].ID == item.ID {
			s.quizItems[i] = *item
			return nil
		}
	}
	clone := *item
	s.quizItems = append(s.quizItems, clone)
	return nil
}

// objectives mirrors learning_objectives (KB-shared inventory).
func (s *stubLearningRepo) ListObjectives(_ context.Context, tenantID uint64, kbID string) ([]types.LearningObjective, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningObjective
	for _, o := range s.objectives {
		if o.TenantID == tenantID && o.KnowledgeBaseID == kbID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slug != out[j].Slug {
			return out[i].Slug < out[j].Slug
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *stubLearningRepo) UpsertObjective(_ context.Context, o *types.LearningObjective) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.ID == "" {
		return errors.New("learning: objective id required")
	}
	if o.Status == "" {
		o.Status = types.LearningObjectiveStatusDraft
	}
	if o.ContractType == "" {
		o.ContractType = types.ObjectiveContractConceptTwoFamily
	}
	if o.ContractVersion == "" {
		o.ContractVersion = types.ObjectiveContractVersion
	}
	replaced := false
	for i := range s.objectives {
		if s.objectives[i].ID == o.ID {
			s.objectives[i] = *o
			replaced = true
			break
		}
	}
	if !replaced {
		s.objectives = append(s.objectives, *o)
	}
	return nil
}

// tasks/taskAttempts mirror learning_tasks / learning_task_attempts.
func (s *stubLearningRepo) GetTaskByID(_ context.Context, tenantID uint64, taskID string) (*types.LearningTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tasks {
		if s.tasks[i].ID == taskID && s.tasks[i].TenantID == tenantID {
			clone := s.tasks[i]
			return &clone, nil
		}
	}
	return nil, nil
}

func (s *stubLearningRepo) ListTasks(_ context.Context, tenantID uint64, kbID string) ([]types.LearningTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningTask
	for _, t := range s.tasks {
		if t.TenantID == tenantID && t.KnowledgeBaseID == kbID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slug != out[j].Slug {
			return out[i].Slug < out[j].Slug
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *stubLearningRepo) UpsertTask(_ context.Context, t *types.LearningTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		return errors.New("learning: task id required")
	}
	if t.Status == "" {
		t.Status = types.LearningQuizStatusDraft
	}
	replaced := false
	for i := range s.tasks {
		if s.tasks[i].ID == t.ID {
			s.tasks[i] = *t
			replaced = true
			break
		}
	}
	if !replaced {
		s.tasks = append(s.tasks, *t)
	}
	return nil
}

func (s *stubLearningRepo) InsertTaskAttempt(_ context.Context, a *types.LearningTaskAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = "ta-" + strconv.Itoa(len(s.taskAttempts)+1)
	}
	s.taskAttempts = append(s.taskAttempts, *a)
	return nil
}

func (s *stubLearningRepo) ListTaskAttempts(_ context.Context, scope interfaces.LearningScope) ([]types.LearningTaskAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningTaskAttempt
	for _, a := range s.taskAttempts {
		if a.TenantID == scope.TenantID && a.SubjectID == scope.SubjectID && a.KnowledgeBaseID == scope.KnowledgeBaseID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SubmittedAt.Before(out[j].SubmittedAt) })
	return out, nil
}

func (s *stubLearningRepo) ListTaskAttemptsBySubject(_ context.Context, subjectID string) ([]types.LearningTaskAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningTaskAttempt
	for _, a := range s.taskAttempts {
		if a.SubjectID == subjectID {
			out = append(out, a)
		}
	}
	return out, nil
}

// StaleQuizItemsByEvidence mirrors the real repository: active items of the
// slug whose frozen evidence hash differs from the current digest flip to
// the stale status; disabled items are never touched.
func (s *stubLearningRepo) StaleQuizItemsByEvidence(
	_ context.Context, tenantID uint64, knowledgeBaseID, slug, currentHash string,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var staled int64
	for i := range s.quizItems {
		it := &s.quizItems[i]
		if it.TenantID != tenantID || it.KnowledgeBaseID != knowledgeBaseID || it.Slug != slug {
			continue
		}
		if it.Status != types.LearningQuizStatusActive {
			continue
		}
		if it.EvidenceHash != currentHash {
			it.Status = types.LearningQuizStatusStale
			staled++
		}
	}
	return staled, nil
}

func (s *stubLearningRepo) ListAttempts(_ context.Context, scope interfaces.LearningScope, slug string) ([]types.LearningQuizAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningQuizAttempt
	for _, a := range s.quizAttempts {
		if a.TenantID == scope.TenantID && a.SubjectID == scope.SubjectID &&
			a.KnowledgeBaseID == scope.KnowledgeBaseID && (slug == "" || a.Slug == slug) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListCorrectAttempts(_ context.Context, scope interfaces.LearningScope) ([]types.LearningQuizAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningQuizAttempt
	for _, a := range s.quizAttempts {
		if a.TenantID == scope.TenantID && a.SubjectID == scope.SubjectID &&
			a.KnowledgeBaseID == scope.KnowledgeBaseID && a.IsCorrect {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) InsertAttempt(_ context.Context, a *types.LearningQuizAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		a.ID = "qa-" + strconv.Itoa(len(s.quizAttempts)+1)
	}
	clone := *a
	s.quizAttempts = append(s.quizAttempts, clone)
	return nil
}

func (s *stubLearningRepo) ListQuizItems(_ context.Context, tenantID uint64, knowledgeBaseID, slug string) ([]types.LearningQuizItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningQuizItem
	for _, it := range s.quizItems {
		if it.TenantID == tenantID && it.KnowledgeBaseID == knowledgeBaseID && it.Slug == slug {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) UpsertEdge(_ context.Context, edge *types.LearningEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.edges {
		if s.edges[i].TenantID == edge.TenantID && s.edges[i].KnowledgeBaseID == edge.KnowledgeBaseID &&
			s.edges[i].FromSlug == edge.FromSlug && s.edges[i].ToSlug == edge.ToSlug {
			clone := *edge
			s.edges[i] = clone
			return nil
		}
	}
	clone := *edge
	s.edges = append(s.edges, clone)
	return nil
}

func (s *stubLearningRepo) DeleteEdge(_ context.Context, tenantID uint64, knowledgeBaseID, fromSlug, toSlug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.edges[:0]
	for _, e := range s.edges {
		if e.TenantID == tenantID && e.KnowledgeBaseID == knowledgeBaseID &&
			e.FromSlug == fromSlug && e.ToSlug == toSlug {
			continue
		}
		kept = append(kept, e)
	}
	s.edges = kept
	return nil
}

func (s *stubLearningRepo) ListAllEdges(_ context.Context) ([]types.LearningEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]types.LearningEdge{}, s.edges...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].KnowledgeBaseID != out[j].KnowledgeBaseID {
			return out[i].KnowledgeBaseID < out[j].KnowledgeBaseID
		}
		if out[i].FromSlug != out[j].FromSlug {
			return out[i].FromSlug < out[j].FromSlug
		}
		return out[i].ToSlug < out[j].ToSlug
	})
	return out, nil
}

func (s *stubLearningRepo) ListEdges(_ context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningEdge
	for _, e := range s.edges {
		if e.TenantID == tenantID && e.KnowledgeBaseID == knowledgeBaseID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListMapsBySubject(_ context.Context, tenantID uint64, subjectID string) ([]types.MemoryWikiMap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.MemoryWikiMap
	for _, m := range s.maps {
		if m.TenantID == tenantID && m.SubjectID == subjectID {
			clone := *m
			out = append(out, clone)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListRecentEvents(_ context.Context, scope interfaces.LearningScope, limit, offset int) ([]types.LearningEvent, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var scoped []types.LearningEvent
	for _, e := range s.events {
		if e.TenantID == scope.TenantID && e.SubjectID == scope.SubjectID && e.KnowledgeBaseID == scope.KnowledgeBaseID {
			scoped = append(scoped, e)
		}
	}
	// events are appended chronologically; newest-first = reverse.
	var reversed []types.LearningEvent
	for i := len(scoped) - 1; i >= 0; i-- {
		reversed = append(reversed, scoped[i])
	}
	if offset > len(reversed) {
		return nil, int64(len(reversed)), nil
	}
	page := reversed[offset:]
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	return page, int64(len(reversed)), nil
}

func (s *stubLearningRepo) ListEventsBySubject(_ context.Context, subjectID string) ([]types.LearningEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningEvent
	for _, e := range s.events {
		if e.SubjectID == subjectID {
			out = append(out, e)
		}
	}
	return out, nil
}

// ListAllMapsBySubject is the export's subject-scoped map read (every
// tenant, like the real repository).
func (s *stubLearningRepo) ListAllMapsBySubject(_ context.Context, subjectID string) ([]types.MemoryWikiMap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.MemoryWikiMap
	for _, m := range s.maps {
		if m.SubjectID == subjectID {
			clone := *m
			out = append(out, clone)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListMastery(_ context.Context, scope interfaces.LearningScope) ([]types.MasteryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.MasteryState
	for _, row := range s.mastery {
		if row.TenantID == scope.TenantID && row.SubjectID == scope.SubjectID && row.KnowledgeBaseID == scope.KnowledgeBaseID {
			out = append(out, *row)
		}
	}
	return out, nil
}

// ListMasteryByKB filters strictly by the caller's tenant+kb arguments and
// mirrors the repository's (subject, slug) ordering, so tests prove the
// aggregation sees only rows of THIS scope even with foreign rows stored.
func (s *stubLearningRepo) ListMasteryByKB(_ context.Context, tenantID uint64, kbID string) ([]types.MasteryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.MasteryState
	for _, row := range s.mastery {
		if row.TenantID == tenantID && row.KnowledgeBaseID == kbID {
			out = append(out, *row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubjectID != out[j].SubjectID {
			return out[i].SubjectID < out[j].SubjectID
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// ListMaintenanceMarks filters by tenant+kb, the self-assess vocabulary and
// the since cutoff, newest first — the repository contract in miniature.
func (s *stubLearningRepo) ListMaintenanceMarks(_ context.Context, tenantID uint64, kbID string, since time.Time) ([]types.LearningEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningEvent
	for _, e := range s.events {
		if e.TenantID != tenantID || e.KnowledgeBaseID != kbID {
			continue
		}
		if !strings.HasPrefix(e.Type, "self_assess_") || e.OccurredAt.Before(since) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OccurredAt.After(out[j].OccurredAt) })
	return out, nil
}

func (s *stubLearningRepo) ListSelfAssess(_ context.Context, scope interfaces.LearningScope) (map[string]interfaces.SelfAssessMark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]interfaces.SelfAssessMark{}
	cutoff := time.Now().Add(-interfaces.SelfAssessVisibleWindow)
	for i := len(s.events) - 1; i >= 0; i-- { // newest first
		ev := s.events[i]
		if ev.TenantID != scope.TenantID || ev.SubjectID != scope.SubjectID || ev.KnowledgeBaseID != scope.KnowledgeBaseID {
			continue
		}
		if !strings.HasPrefix(ev.Type, "self_assess_") || ev.OccurredAt.Before(cutoff) {
			continue
		}
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

func (s *stubLearningRepo) ListLastActivity(_ context.Context, scope interfaces.LearningScope) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]time.Time{}
	for _, ev := range s.events {
		if !types.IsHumanLearningEvent(ev.Type) {
			continue
		}
		if ev.TenantID == scope.TenantID && ev.SubjectID == scope.SubjectID && ev.KnowledgeBaseID == scope.KnowledgeBaseID {
			if at := ev.OccurredAt; at.After(out[ev.Slug]) {
				out[ev.Slug] = at
			}
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListMasteryBySubject(_ context.Context, subjectID string) ([]types.MasteryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.MasteryState
	for _, row := range s.mastery {
		if row.SubjectID == subjectID {
			out = append(out, *row)
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListAttemptsBySubject(_ context.Context, subjectID string) ([]types.LearningQuizAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningQuizAttempt
	for _, a := range s.quizAttempts {
		if a.SubjectID == subjectID {
			out = append(out, a)
		}
	}
	return out, nil
}

// DeleteLearningDataBySubject is subject-scoped like the real repository:
// every workspace's rows go, including shared-KB rows filed under a
// foreign effective tenant. (Attempts of other subjects survive.)
func (s *stubLearningRepo) DeleteLearningDataBySubject(_ context.Context, subjectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLearningDataBySubjectLocked(subjectID)
}

// deleteLearningDataBySubjectLocked is the sweep body shared with
// DeleteProfileData; callers hold s.mu.
func (s *stubLearningRepo) deleteLearningDataBySubjectLocked(subjectID string) error {
	for scope := range s.planPrefs {
		if scope.SubjectID == subjectID {
			delete(s.planPrefs, scope)
		}
	}
	s.events = filterEventsBySubject(s.events, subjectID)
	for key, row := range s.mastery {
		if row.SubjectID == subjectID {
			delete(s.mastery, key)
		}
	}
	for key, m := range s.maps {
		if m.SubjectID == subjectID {
			delete(s.maps, key)
		}
	}
	var kept []types.LearningQuizAttempt
	for _, a := range s.quizAttempts {
		if a.SubjectID != subjectID {
			kept = append(kept, a)
		}
	}
	s.quizAttempts = kept
	// Task attempts are personal data: the profile delete removes them.
	var keptTasks []types.LearningTaskAttempt
	for _, a := range s.taskAttempts {
		if a.SubjectID != subjectID {
			keptTasks = append(keptTasks, a)
		}
	}
	s.taskAttempts = keptTasks
	// Skips are personal data: the profile delete removes them too.
	for key := range s.skips {
		if parts := strings.Split(key, "|"); len(parts) == 4 && parts[1] == subjectID {
			delete(s.skips, key)
		}
	}
	return nil
}

func filterEventsBySubject(events []types.LearningEvent, subjectID string) []types.LearningEvent {
	var out []types.LearningEvent
	for _, e := range events {
		if e.SubjectID != subjectID {
			out = append(out, e)
		}
	}
	return out
}

// DeleteLearningDataByKB is the orphan sweep seam: it records the call so
// tests can assert the sweep fired for deleted KBs.
func (s *stubLearningRepo) DeleteLearningDataByKB(_ context.Context, tenantID uint64, kbID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for scope := range s.planPrefs {
		if scope.TenantID == tenantID && scope.KnowledgeBaseID == kbID {
			delete(s.planPrefs, scope)
		}
	}
	s.kbSweeps = append(s.kbSweeps, [2]interface{}{tenantID, kbID})
	for key, row := range s.mastery {
		if row.TenantID == tenantID && row.KnowledgeBaseID == kbID {
			delete(s.mastery, key)
		}
	}
	s.events = filterEventsByKB(s.events, tenantID, kbID)
	for key := range s.backfillMarks {
		// Marks of a swept KB go with it, like the real repository.
		if strings.HasSuffix(key, "|"+kbID) && strings.HasPrefix(key, strconv.FormatUint(tenantID, 10)+"|") {
			delete(s.backfillMarks, key)
		}
	}
	for key := range s.skips {
		// Skips of a swept KB go with it, like the real repository.
		if strings.HasSuffix(key, "|"+kbID) && strings.HasPrefix(key, strconv.FormatUint(tenantID, 10)+"|") {
			delete(s.skips, key)
		}
	}
	return nil
}

func filterEventsByKB(events []types.LearningEvent, tenantID uint64, kbID string) []types.LearningEvent {
	var out []types.LearningEvent
	for _, e := range events {
		if e.TenantID != tenantID || e.KnowledgeBaseID != kbID {
			out = append(out, e)
		}
	}
	return out
}

func (s *stubLearningRepo) UpsertSubjectPrefs(_ context.Context, prefs *types.LearningSubjectPrefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := *prefs
	s.prefs[prefs.SubjectID] = &clone
	return nil
}

// GetSubjectEpoch mirrors the real repository: absent row = epoch 0.
func (s *stubLearningRepo) GetSubjectEpoch(_ context.Context, subjectID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch[subjectID], nil
}

// DeleteProfileData mirrors the atomic delete: sweep + optional opt-out +
// epoch bump under one lock. The deleteErr hook lets fault-injection tests
// fail the whole operation mid-way and assert nothing was half-applied.
func (s *stubLearningRepo) DeleteProfileData(_ context.Context, tenantID uint64, subjectID string, optOut bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		if err := s.deleteErr(); err != nil {
			return err
		}
	}
	if err := s.deleteLearningDataBySubjectLocked(subjectID); err != nil {
		return err
	}
	if optOut {
		clone := types.LearningSubjectPrefs{
			TenantID: tenantID, SubjectID: subjectID, CollectDisabled: true,
		}
		s.prefs[subjectID] = &clone
	}
	s.epoch[subjectID]++
	return nil
}

// ApplyTopicMapping mirrors the epoch-fenced transaction: a stale epoch
// discards the whole write with ErrLearningEpochAdvanced; otherwise the
// mapping upsert, the optional event append and the optional fold land as
// one unit. The applyErr hook lets tests inject a generic write failure.
func (s *stubLearningRepo) ApplyTopicMapping(
	_ context.Context, expectedEpoch int64, scope interfaces.LearningScope,
	mapping *types.MemoryWikiMap, event *types.LearningEvent,
	fold func(*types.MasteryState) types.MasteryState,
) error {
	s.applyCalls.Add(1)
	if mapping == nil {
		return errors.New("learning: ApplyTopicMapping requires a mapping")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.applyErr != nil {
		if err := s.applyErr(); err != nil {
			return err
		}
	}
	if s.epoch[scope.SubjectID] != expectedEpoch {
		return interfaces.ErrLearningEpochAdvanced
	}
	if s.maps == nil {
		s.maps = map[string]*types.MemoryWikiMap{}
	}
	key := mapping.NormalizedTopicKey + "→" + mapping.Slug
	clone := *mapping
	s.maps[key] = &clone
	if event != nil {
		ev := *event
		ev.ID = "ev-auto-" + strconv.Itoa(len(s.events)+1)
		s.events = append(s.events, ev)
		s.appends.Add(1)
	}
	if fold != nil {
		mkey := s.masteryKey(scope, mapping.Slug)
		var existing *types.MasteryState
		if row, ok := s.mastery[mkey]; ok {
			clone := *row
			existing = &clone
		}
		next := fold(existing)
		stored := next
		s.mastery[mkey] = &stored
		s.masteryUpserts.Add(1)
	}
	return nil
}

func (s *stubLearningRepo) GetSubjectPrefs(_ context.Context, subjectID string) (*types.LearningSubjectPrefs, error) {
	s.prefsHits.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.prefs[subjectID]; ok {
		clone := *p
		return &clone, nil
	}
	return nil, nil
}

func (s *stubLearningRepo) snapshotEvents() []types.LearningEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.LearningEvent{}, s.events...)
}

// stubWikiRepo serves fixed pages per (kb, type).
type stubWikiRepo struct {
	interfaces.WikiPageRepository
	pages   map[string][]*types.WikiPage // key: kb|type
	folders map[string][]*types.WikiFolder
}

func (s *stubWikiRepo) ListByType(_ context.Context, kbID, pageType string) ([]*types.WikiPage, error) {
	return s.pages[kbID+"|"+pageType], nil
}

func (s *stubWikiRepo) ListAll(_ context.Context, kbID string) ([]*types.WikiPage, error) {
	// Concatenate every page type the stub holds, in deterministic key
	// order — the real repository's ListAll also returns non-node types
	// (summary/index/synthesis/comparison), and callers must filter.
	keys := make([]string, 0, len(s.pages))
	for key := range s.pages {
		if len(key) > len(kbID)+1 && key[:len(kbID)+1] == kbID+"|" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var all []*types.WikiPage
	for _, key := range keys {
		all = append(all, s.pages[key]...)
	}
	return all, nil
}

func (s *stubWikiRepo) addPage(kbID string, p *types.WikiPage) {
	key := kbID + "|" + p.PageType
	s.pages[key] = append(s.pages[key], p)
}

func (s *stubWikiRepo) ListAllFolders(_ context.Context, kbID string) ([]*types.WikiFolder, error) {
	return s.folders[kbID], nil
}

func (s *stubWikiRepo) GetBySlug(_ context.Context, kb string, slug string) (*types.WikiPage, error) {
	for key, pages := range s.pages {
		if len(key) <= len(kb) || key[:len(kb)+1] != kb+"|" {
			continue
		}
		for _, p := range pages {
			if p.Slug == slug {
				return p, nil
			}
		}
	}
	return nil, nil
}

func (s *stubWikiRepo) ListBySlugs(_ context.Context, _ string, slugs []string) (map[string]*types.WikiPageLite, error) {
	// The stub buckets pages under kb|type keys, so bucket membership already
	// carries the KB scope (fixture pages carry no KnowledgeBaseID value).
	wanted := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		wanted[slug] = true
	}
	out := map[string]*types.WikiPageLite{}
	for _, pages := range s.pages {
		for _, p := range pages {
			if wanted[p.Slug] {
				out[p.Slug] = &types.WikiPageLite{Slug: p.Slug, Title: p.Title, PageType: p.PageType}
			}
		}
	}
	return out, nil
}

// collectorCtx builds the context shape the QA post-answer path carries.
func collectorCtx(tenantID uint64, subjectID string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: subjectID})
}

// stubKBRepo serves fixed knowledge bases.
type stubKBRepo struct {
	interfaces.KnowledgeBaseRepository
	kbs map[string]*types.KnowledgeBase
}

func (s *stubKBRepo) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return s.kbs[id], nil
}

func (s *stubKBRepo) ListKnowledgeBases(_ context.Context) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(s.kbs))
	keys := make([]string, 0, len(s.kbs))
	for k := range s.kbs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, s.kbs[k])
	}
	return out, nil
}

// stubChunkRepo serves fixed chunks by id.
type stubChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (s *stubChunkRepo) ListChunksByID(_ context.Context, _ uint64, ids []string) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		if c, ok := s.chunks[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// maintenanceService wires all five dependencies for stage-3 tests; the
// model-service stub is returned so tests can probe what the model channel
// saw.
func maintenanceService(repo *stubLearningRepo, wiki *stubWikiRepo, chunks *stubChunkRepo, model chat.Chat, kbs *stubKBRepo) (*Service, *stubModelService) {
	ms := &stubModelService{model: model}
	return NewService(repo, wiki, chunks, ms, kbs, nil), ms
}

// stubKnowledgeRepo serves fixed documents by id (quiz source-doc titles
// and the document-order channel's creation times).
type stubKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	docs map[string]*types.Knowledge
}

func (s *stubKnowledgeRepo) GetKnowledgeByID(_ context.Context, _ uint64, id string) (*types.Knowledge, error) {
	return s.docs[id], nil
}

func (s *stubKnowledgeRepo) GetKnowledgeBatch(_ context.Context, _ uint64, ids []string) ([]*types.Knowledge, error) {
	var out []*types.Knowledge
	for _, id := range ids {
		if k, ok := s.docs[id]; ok {
			out = append(out, k)
		}
	}
	return out, nil
}

// ---- skip list (learning_skips) ----

func (s *stubLearningRepo) AddSkip(_ context.Context, scope interfaces.LearningScope, slug string, createdAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skips == nil {
		s.skips = map[string]time.Time{}
	}
	key := s.masteryKey(scope, slug)
	if _, ok := s.skips[key]; !ok {
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		s.skips[key] = createdAt
	}
	return nil
}

func (s *stubLearningRepo) RemoveSkip(_ context.Context, scope interfaces.LearningScope, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.skips, s.masteryKey(scope, slug))
	return nil
}

func (s *stubLearningRepo) ListSkips(_ context.Context, scope interfaces.LearningScope) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := strconv.FormatUint(scope.TenantID, 10) + "|" + scope.SubjectID + "|" + scope.KnowledgeBaseID + "|"
	out := map[string]time.Time{}
	for key, at := range s.skips {
		if strings.HasPrefix(key, prefix) {
			out[key[len(prefix):]] = at
		}
	}
	return out, nil
}

func (s *stubLearningRepo) ListSkipsBySubject(_ context.Context, subjectID string) ([]types.LearningSkip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningSkip
	for key, at := range s.skips {
		parts := strings.Split(key, "|")
		if len(parts) != 4 || parts[1] != subjectID {
			continue
		}
		tenant, _ := strconv.ParseUint(parts[0], 10, 64)
		out = append(out, types.LearningSkip{
			TenantID: tenant, SubjectID: subjectID,
			KnowledgeBaseID: parts[2], Slug: parts[3], CreatedAt: at,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *stubLearningRepo) ListAllSkips(_ context.Context) ([]types.LearningSkip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []types.LearningSkip
	for key, at := range s.skips {
		parts := strings.Split(key, "|")
		if len(parts) != 4 {
			continue
		}
		tenant, _ := strconv.ParseUint(parts[0], 10, 64)
		out = append(out, types.LearningSkip{
			TenantID: tenant, SubjectID: parts[1],
			KnowledgeBaseID: parts[2], Slug: parts[3], CreatedAt: at,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].KnowledgeBaseID != out[j].KnowledgeBaseID {
			return out[i].KnowledgeBaseID < out[j].KnowledgeBaseID
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

func (s *stubLearningRepo) WithSubject(ctx context.Context, subject string, epoch int64, collect bool, fn func(context.Context) error) error {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	current, err := s.GetSubjectEpoch(ctx, subject)
	if err != nil {
		return err
	}
	if current != epoch {
		return interfaces.ErrLearningEpochAdvanced
	}
	if collect {
		s.prefsHits.Add(1)
		s.mu.Lock()
		p := s.prefs[subject]
		disabled := p != nil && p.CollectDisabled
		s.mu.Unlock()
		if disabled {
			return interfaces.ErrLearningCollectionDisabled
		}
	}
	return fn(ctx)
}
func (s *stubLearningRepo) ListEventScopes(ctx context.Context) ([]interfaces.LearningScope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[interfaces.LearningScope]bool{}
	var out []interfaces.LearningScope
	for scope := range s.planPrefs {
		seen[scope] = true
		out = append(out, scope)
	}
	for _, e := range s.events {
		scope := interfaces.LearningScope{TenantID: e.TenantID, SubjectID: e.SubjectID, KnowledgeBaseID: e.KnowledgeBaseID}
		if !seen[scope] {
			out = append(out, scope)
			seen[scope] = true
		}
	}
	return out, nil
}
