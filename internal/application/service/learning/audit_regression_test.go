package learning

import (
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
	"time"
)

func TestAuditTaskOldVersionNeverVerifiesCurrentObjective(t *testing.T) {
	o := objFixture("o", "concept/rag")
	o.ContractType = types.ObjectiveContractTaskChecks
	o.ContentVersion = "v2"
	a := types.LearningTaskAttempt{ID: "a", TenantID: o.TenantID, KnowledgeBaseID: o.KnowledgeBaseID, ObjectiveID: o.ID, FamilyID: "f", SubjectID: "web_user:alice", ObjectiveVersion: "v1", ContractVersion: types.ObjectiveContractVersion, ScorerVersion: types.TaskScorerVersion, RubricVersion: "v1", ContentVersion: "v1", Eligible: true, IsPassed: true, SubmittedAt: time.Now()}
	if d := DeriveObjectiveState(o, nil, nil, []types.LearningTaskAttempt{a}); d.State != types.ObjectiveStateStale {
		t.Fatalf("old task evidence must remain stale, got %s", d.State)
	}
}

func TestAuditDraftObjectiveCannotVerifyInCore(t *testing.T) {
	o := objFixture("o", "concept/rag")
	o.Status = types.LearningObjectiveStatusDraft
	o.ContractType = types.ObjectiveContractTaskChecks
	a := types.LearningTaskAttempt{ID: "a", TenantID: o.TenantID, KnowledgeBaseID: o.KnowledgeBaseID, SubjectID: "web_user:alice", ObjectiveVersion: o.ContentVersion, ContractVersion: types.ObjectiveContractVersion, ScorerVersion: types.TaskScorerVersion, RubricVersion: "v1", ObjectiveID: o.ID, FamilyID: "f", ContentVersion: o.ContentVersion, Eligible: true, IsPassed: true, SubmittedAt: time.Now()}
	if d := DeriveObjectiveState(o, nil, nil, []types.LearningTaskAttempt{a}); d.State == types.ObjectiveStateVerified {
		t.Fatal("draft verifies in core projection")
	}
}

func TestAuditPlannerRespectsSmallBudget(t *testing.T) {
	in := planInput{Pages: map[string]string{"a": "A", "b": "B", "c": "C"}, Objectives: map[string]types.LearningObjective{}, States: map[string]ObjectiveDerivation{}, ObjBySlug: map[string][]string{}, TimeBudgetMinutes: 4}
	for _, id := range []string{"a", "b", "c"} {
		o := objFixture(id, id)
		in.Objectives[id] = o
		in.ObjBySlug[id] = []string{id}
		in.UserGoals = append(in.UserGoals, id)
	}
	p := planShortPath(in)
	spent := 0
	for _, s := range p.Steps {
		spent += s.Minutes
	}
	if spent > 4 {
		t.Fatalf("budget=4 actual=%d", spent)
	}
}

func TestAuditPlannerReadAdvancesToVerification(t *testing.T) {
	o := objFixture("o", "concept/rag")
	in := planInput{Pages: map[string]string{o.Slug: "RAG"}, Objectives: map[string]types.LearningObjective{o.ID: o}, States: map[string]ObjectiveDerivation{o.ID: {State: types.ObjectiveStateUnverified, Evidence: ObjectiveEvidence{Unknown: true}}}, ObjBySlug: map[string][]string{o.Slug: {o.ID}}, UserGoals: []string{o.ID}, Exposure: map[string]bool{o.Slug: true}, TimeBudgetMinutes: 15}
	p := planShortPath(in)
	if len(p.Steps) == 0 || p.Steps[0].Action == ActionRead {
		t.Fatal("reading cannot advance path; first step remains read")
	}
}
