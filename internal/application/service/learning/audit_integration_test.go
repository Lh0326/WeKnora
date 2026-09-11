package learning

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"strings"
	"testing"
	"time"
)

func auditDatabaseFixture(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, _, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.Close() })
	if e = db.AutoMigrate(&types.LearningObjective{}, &types.LearningQuizItem{}, &types.LearningQuizAttempt{}, &types.LearningTask{}, &types.LearningTaskAttempt{}, &types.LearningSubjectPrefs{}, &types.LearningPlanPreference{}, &types.LearningBackfillMark{}, &types.LearningSubjectEpoch{}, &types.LearningEvent{}, &types.MasteryState{}, &types.MemoryWikiMap{}, &types.LearningSkip{}); e != nil {
		t.Fatal(e)
	}
	s.repo = repository.NewLearningRepository(db)
	return s, db
}
func auditPublishTask(t *testing.T, s *Service) {
	t.Helper()
	ctx := collectorCtx(1, "alice")
	o := objFixture("o", "concept/rag")
	o.ContractType = types.ObjectiveContractTaskChecks
	if e := s.repo.UpsertObjective(ctx, &o); e != nil {
		t.Fatal(e)
	}
	task := types.LearningTask{ID: "t", TenantID: 1, KnowledgeBaseID: testKB, Slug: o.Slug, ObjectiveID: o.ID, FamilyID: "f", Title: "task", Scenario: "scenario", Fields: types.JSONColumn(`[{"id":"choice","label":"choice","type":"select","options":["x","y"]}]`), AnswerKey: map[string]string{"choice": "x"}, CriticalChecks: types.JSONColumn(`[{"id":"choice","description":"must be x"}]`), ContentVersion: "v1"}
	if e := s.repo.UpsertTask(ctx, &task); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReviewTask(ctx, testKB, task.ID, interfaces.ReviewDecisionInput{Decision: "approve", Note: "test fixture review"}); e != nil {
		t.Fatal(e)
	}
}
func TestAuditTaskTransactionAndConsent(t *testing.T) {
	s, db := auditDatabaseFixture(t)
	auditPublishTask(t, s)
	ctx := collectorCtx(1, "alice")
	// Rollback must include inserts made through the context-aware repository, not just epoch updates.
	sentinel := errors.New("rollback")
	err := s.repo.WithSubject(ctx, "web_user:alice", 0, true, func(tx context.Context) error {
		_, e := s.submitTaskAnswer(tx, testKB, "t", map[string]string{"choice": "x"}, "", true)
		if e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var n int64
	db.Model(&types.LearningTaskAttempt{}).Count(&n)
	if n != 0 {
		t.Fatal("task escaped rollback")
	}
	r, e := s.SubmitTaskAnswer(ctx, testKB, "t", map[string]string{"choice": "x"}, "")
	if e != nil || !r.Eligible {
		t.Fatalf("positive control: %+v %v", r, e)
	}
	old := s.CaptureCollectionContext(ctx)
	if e = s.repo.DeleteProfileData(ctx, 1, "web_user:alice", true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubmitTaskAnswer(old, testKB, "t", map[string]string{"choice": "x"}, ""); !errors.Is(e, interfaces.ErrLearningEpochAdvanced) {
		t.Fatalf("old write survived epoch: %v", e)
	}
	r, e = s.SubmitTaskAnswer(ctx, testKB, "t", map[string]string{"choice": "x"}, "")
	if e != nil || r.Eligible || r.GradeReason != "collection_disabled" {
		t.Fatalf("opt-out: %+v %v", r, e)
	}
	db.Model(&types.LearningTaskAttempt{}).Count(&n)
	if n != 0 {
		t.Fatal("task resurrected after delete")
	}
}
func TestAuditTaskSourceChangeAndTakeJSON(t *testing.T) {
	s, _ := auditDatabaseFixture(t)
	auditPublishTask(t, s)
	ctx := collectorCtx(1, "alice")
	tasks, e := s.TakeTask(ctx, testKB, "o")
	if e != nil || len(tasks) != 1 {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(tasks)
	if !strings.Contains(string(raw), `"fields":[`) || strings.Contains(string(raw), "must be x") || strings.Contains(string(raw), "answer_key") {
		t.Fatalf("invalid learner task JSON: %s", raw)
	}
	if _, e = s.SubmitTaskAnswer(ctx, testKB, "t", map[string]string{"choice": "x"}, ""); e != nil {
		t.Fatal(e)
	}
	s.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "changed"
	view, e := s.ObjectiveView(ctx, testKB)
	if e != nil || view.Entries[0].State != types.ObjectiveStateStale {
		t.Fatalf("live source change did not invalidate: %+v %v", view, e)
	}
	if tasks, e = s.TakeTask(ctx, testKB, "o"); e != nil || len(tasks) != 0 {
		t.Fatal("stale task still offered")
	}
	if _, e = s.SubmitTaskAnswer(ctx, testKB, "t", map[string]string{"choice": "x"}, ""); !errors.Is(e, ErrTaskNotTakeable) {
		t.Fatalf("stale task graded: %v", e)
	}
	export, e := s.ExportProfile(ctx)
	if e != nil || len(export.Objectives) != 1 || export.Objectives[0].State != types.ObjectiveStateStale {
		t.Fatalf("task-only export inconsistent: %+v %v", export, e)
	}
}
func TestAuditContentUpsertsCannotCrossScope(t *testing.T) {
	s, db := auditDatabaseFixture(t)
	ctx := collectorCtx(1, "alice")
	o := objFixture("o", "concept/rag")
	if e := s.repo.UpsertObjective(ctx, &o); e != nil {
		t.Fatal(e)
	}
	other := o
	other.TenantID = 2
	other.KnowledgeBaseID = "other"
	other.Title = "attack"
	if e := s.repo.UpsertObjective(ctx, &other); e == nil {
		t.Fatal("cross-tenant objective overwrite allowed")
	}
	var got types.LearningObjective
	db.First(&got, "id = ?", o.ID)
	if got.Title != o.Title {
		t.Fatal("objective corrupted")
	}
	for _, kind := range []string{"quiz", "task"} {
		if kind == "quiz" {
			a := types.LearningQuizItem{ID: "q", TenantID: 1, KnowledgeBaseID: testKB}
			if e := s.repo.UpsertQuizItem(ctx, &a); e != nil {
				t.Fatal(e)
			}
			a.TenantID = 2
			if e := s.repo.UpsertQuizItem(ctx, &a); e == nil {
				t.Fatal("quiz scope bypass")
			}
		}
		if kind == "task" {
			a := types.LearningTask{ID: "t", TenantID: 1, KnowledgeBaseID: testKB}
			if e := s.repo.UpsertTask(ctx, &a); e != nil {
				t.Fatal(e)
			}
			a.KnowledgeBaseID = "other"
			if e := s.repo.UpsertTask(ctx, &a); e == nil {
				t.Fatal("task KB scope bypass")
			}
		}
	}
}
func TestAuditProgressAndFreezeUseFullLiveUniverse(t *testing.T) {
	s, repo, wiki := readFixture(t)
	ctx := collectorCtx(1, "alice")
	a := objFixture("a", "concept/rag")
	b := a
	b.ID = "b"
	draft := a
	draft.ID = "draft"
	draft.Status = types.LearningObjectiveStatusDraft
	for _, o := range []types.LearningObjective{a, b, draft} {
		if e := repo.UpsertObjective(ctx, &o); e != nil {
			t.Fatal(e)
		}
	}
	wiki.pages[testKB+"|concept"] = append(wiki.pages[testKB+"|concept"], testWikiPage("concept/no-goal", []string{"c2"}, nil))
	repo.AppendEvent(ctx, &types.LearningEvent{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/no-goal", Type: types.LearningEventWikiDeepRead, OccurredAt: time.Now()})
	p, e := s.ObjectiveProgress(ctx, testKB, "a")
	if e != nil {
		t.Fatal(e)
	}
	if p.TotalObjectives != 2 || p.GoalTotalObjectives != 1 || p.ContactNodes != 1 {
		t.Fatalf("denominator/contact error %+v", p)
	}
	p2, e := s.ObjectiveProgress(ctx, testKB, "b")
	if e != nil || p2.GoalSetVersion == p.GoalSetVersion {
		t.Fatal("goal version unchanged")
	}
	if _, e = s.ObjectiveProgress(ctx, testKB, "draft"); e == nil {
		t.Fatal("draft allowed as goal")
	}
	trial := attemptFixture("a1", "a", "exposed", false, time.Now().Add(-time.Hour))
	trial.Eligible = false
	repo.InsertAttempt(ctx, &trial)
	frozen, e := s.FreezeAssessment(ctx, testKB)
	if e != nil {
		t.Fatal(e)
	}
	if len(frozen.GoalStatesBefore) != 2 || len(frozen.EvidenceFamiliesBefore) != 1 || frozen.EvidenceFamiliesBefore[0] != "exposed" {
		t.Fatalf("freeze lost unseen objectives or practice exposure: %+v", frozen)
	}
	if frozen.LearningModelVersion != NodeEstimateVersion || len(frozen.NodeEstimates) == 0 {
		t.Fatal("pre-assessment snapshot omitted the learning model", frozen)
	}
	for _, estimate := range frozen.NodeEstimates {
		if estimate.ContentVersion == "" || estimate.ModelVersion != NodeEstimateVersion {
			t.Fatal("unversioned model snapshot", estimate)
		}
	}
	if _, e = s.FreezeAssessment(collectorCtx(1, "bob"), testKB); e != nil {
		t.Fatal(e)
	}
	bob, e := s.FreezeAssessment(collectorCtx(1, "bob"), testKB)
	if e != nil || len(bob.EvidenceFamiliesBefore) != 0 {
		t.Fatal("freeze leaked another participant")
	}
	for _, estimate := range bob.NodeEstimates {
		if estimate.Opportunities != 0 || estimate.Answers != 0 || estimate.Corrections != 0 {
			t.Fatal("model snapshot leaked another participant", estimate)
		}
	}
}

func TestAuditSemanticTaskCorrectionInvalidatesOldPass(t *testing.T) {
	s, _ := auditDatabaseFixture(t)
	auditPublishTask(t, s)
	ctx := collectorCtx(1, "alice")
	if _, e := s.SubmitTaskAnswer(ctx, testKB, "t", map[string]string{"choice": "x"}, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReviewTask(ctx, testKB, "t", interfaces.ReviewDecisionInput{Decision: "approve", ChangeKind: "semantic", Note: "corrected task meaning"}); e != nil {
		t.Fatal(e)
	}
	view, e := s.ObjectiveView(ctx, testKB)
	if e != nil || view.Entries[0].State != types.ObjectiveStateStale {
		t.Fatalf("semantic correction reused old certificate: %+v %v", view, e)
	}
}
func TestAuditResolvedEmptyDepthDoesNotExpandToAllGoals(t *testing.T) {
	o := objFixture("task", "concept/rag")
	in := planInput{GoalsResolved: true, Pages: map[string]string{o.Slug: "RAG"}, Objectives: map[string]types.LearningObjective{o.ID: o}, ObjBySlug: map[string][]string{o.Slug: {o.ID}}, Exposure: map[string]bool{o.Slug: true}, UserGoals: []string{}}
	for _, st := range planShortPath(in).Steps {
		if st.Objective != "" {
			t.Fatal("resolved empty goal scope expanded into unrelated objectives")
		}
	}
}
