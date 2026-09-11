package learning

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"testing"
)

func (s *stubLearningRepo) GetPlanPreference(_ context.Context, scope interfaces.LearningScope) (*types.LearningPlanPreference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.planPrefs[scope]
	if !ok {
		return nil, nil
	}
	row.GoalObjectives = append(types.RefList{}, row.GoalObjectives...)
	return &row, nil
}
func (s *stubLearningRepo) SavePlanPreference(_ context.Context, row *types.LearningPlanPreference, expected string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.planPrefs == nil {
		s.planPrefs = map[interfaces.LearningScope]types.LearningPlanPreference{}
	}
	scope := interfaces.LearningScope{TenantID: row.TenantID, SubjectID: row.SubjectID, KnowledgeBaseID: row.KnowledgeBaseID}
	old, exists := s.planPrefs[scope]
	if (exists && old.Revision != expected) || (!exists && expected != "") {
		return interfaces.ErrLearningPreferenceConflict
	}
	copy := *row
	copy.GoalObjectives = append(types.RefList{}, row.GoalObjectives...)
	s.planPrefs[scope] = copy
	return nil
}
func (s *stubLearningRepo) ListPlanPreferencesBySubject(_ context.Context, subject string) ([]types.LearningPlanPreference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []types.LearningPlanPreference{}
	for _, p := range s.planPrefs {
		if p.SubjectID == subject {
			p.GoalObjectives = append(types.RefList{}, p.GoalObjectives...)
			rows = append(rows, p)
		}
	}
	return rows, nil
}

func TestPlanPreferencesPersistAcrossReadsAndRejectLostUpdates(t *testing.T) {
	s, db := auditDatabaseFixture(t)
	exercisePlanPreferencePersistence(t, s, db)
}
func exercisePlanPreferencePersistence(t *testing.T, s *Service, db *gorm.DB) {
	ctx := collectorCtx(1, "alice")
	initial, err := s.GetPlanPreferences(ctx, testKB)
	if err != nil || initial.TimeBudgetMinutes != 15 || !initial.UseMemory || initial.Revision != "" {
		t.Fatalf("default: %+v %v", initial, err)
	}
	input := *initial
	input.TimeBudgetMinutes = 5
	input.Depth = "operate"
	input.UseMemory = false
	saved, err := s.UpdatePlanPreferences(ctx, testKB, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision == "" {
		t.Fatal("missing revision")
	}
	got, err := s.GetPlanPreferences(ctx, testKB)
	if err != nil || got.TimeBudgetMinutes != 5 || got.UseMemory || got.Depth != "operate" {
		t.Fatalf("not persisted: %+v %v", got, err)
	}
	if _, err = s.UpdatePlanPreferences(ctx, testKB, input); !errors.Is(err, interfaces.ErrLearningPreferenceConflict) {
		t.Fatalf("stale first reader overwrote scope: %v", err)
	}
	got.UseMemory = true
	got.TimeBudgetMinutes = 30
	if _, err = s.UpdatePlanPreferences(ctx, testKB, *got); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdatePlanPreferences(ctx, testKB, *saved); !errors.Is(err, interfaces.ErrLearningPreferenceConflict) {
		t.Fatalf("stale revision overwrote scope: %v", err)
	}
	var events int64
	db.Model(&types.LearningEvent{}).Count(&events)
	if events != 0 {
		t.Fatal("settings fabricated learning evidence")
	}
	current, _ := s.GetPlanPreferences(ctx, testKB)
	current.LimitToFolder = true
	current.FolderID = ""
	current.UseMemory = false
	ungrouped, err := s.UpdatePlanPreferences(ctx, testKB, *current)
	if err != nil || !ungrouped.LimitToFolder || ungrouped.FolderID != "" {
		t.Fatalf("ungrouped module became whole library: %+v %v", ungrouped, err)
	}
	reopened, _ := s.GetPlanPreferences(ctx, testKB)
	if !reopened.LimitToFolder || reopened.UseMemory || reopened.Revision != ungrouped.Revision {
		t.Fatalf("zero values or ungrouped scope not persisted: %+v", reopened)
	}
}

func TestPlanPreferencesIsolationExportDeletionAndStaleWrites(t *testing.T) {
	s, _ := auditDatabaseFixture(t)
	input := interfaces.LearningPlanSettings{Depth: "aware", TimeBudgetMinutes: 5, UseMemory: true}
	for _, entry := range []struct {
		tenant   uint64
		user, kb string
	}{{1, "alice", testKB}, {2, "alice", testKB}, {1, "bob", testKB}, {1, "alice", "second-kb"}} {
		if _, err := s.UpdatePlanPreferences(collectorCtx(entry.tenant, entry.user), entry.kb, input); err != nil {
			t.Fatal(err)
		}
	}
	ctx := collectorCtx(1, "alice")
	saved, _ := s.GetPlanPreferences(ctx, testKB)
	scopes, err := s.repo.ListEventScopes(ctx)
	if err != nil || len(scopes) != 4 {
		t.Fatalf("preference-only orphan scopes omitted: %v %+v", err, scopes)
	}
	if got, _ := s.GetPlanPreferences(collectorCtx(3, "alice"), testKB); got.Revision != "" {
		t.Fatal("tenant leak")
	}
	export, err := s.ExportProfile(ctx)
	if err != nil || len(export.PlanPreferences) != 3 {
		t.Fatalf("export missed shared workspace scope: %v %+v", err, export)
	}
	staleCtx := s.CaptureCollectionContext(ctx)
	if err := s.DeleteProfile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdatePlanPreferences(staleCtx, testKB, *saved); !errors.Is(err, interfaces.ErrLearningEpochAdvanced) {
		t.Fatalf("in-flight write restored deletion: %v", err)
	}
	if _, err := s.UpdatePlanPreferences(ctx, testKB, *saved); !errors.Is(err, interfaces.ErrLearningPreferenceConflict) {
		t.Fatalf("old browser restored deleted revision: %v", err)
	}
	export, err = s.ExportProfile(ctx)
	if err != nil || len(export.PlanPreferences) != 0 {
		t.Fatalf("delete incomplete: %v", err)
	}
	if got, _ := s.GetPlanPreferences(collectorCtx(1, "bob"), testKB); got.Revision == "" {
		t.Fatal("deleted another user")
	}
	// New explicit settings remain allowed with collection disabled.
	if _, err := s.UpdatePlanPreferences(ctx, testKB, input); err != nil {
		t.Fatalf("consent blocked explicit preference: %v", err)
	}
	if err := s.repo.DeleteLearningDataByKB(ctx, 1, testKB); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetPlanPreferences(ctx, testKB); got.Revision != "" {
		t.Fatal("KB sweep left settings")
	}
}

func TestPlanPreferencesValidationDoesNotReplaceSavedScope(t *testing.T) {
	s, _ := auditDatabaseFixture(t)
	ctx := collectorCtx(1, "alice")
	valid := interfaces.LearningPlanSettings{Depth: "aware", TimeBudgetMinutes: 15, UseMemory: true}
	saved, err := s.UpdatePlanPreferences(ctx, testKB, valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*interfaces.LearningPlanSettings){func(p *interfaces.LearningPlanSettings) { p.TimeBudgetMinutes = 0 }, func(p *interfaces.LearningPlanSettings) { p.TimeBudgetMinutes = 121 }, func(p *interfaces.LearningPlanSettings) { p.Depth = "admin" }, func(p *interfaces.LearningPlanSettings) { p.FolderID = "deleted-folder" }, func(p *interfaces.LearningPlanSettings) { p.GoalObjectives = []string{"foreign-goal"} }} {
		bad := *saved
		change(&bad)
		if _, err := s.UpdatePlanPreferences(ctx, testKB, bad); !errors.Is(err, ErrInvalidLearningRequest) {
			t.Fatalf("invalid setting accepted: %v", err)
		}
	}
	current, _ := s.GetPlanPreferences(ctx, testKB)
	if current.Revision != saved.Revision {
		t.Fatal("invalid input changed saved record")
	}
}
