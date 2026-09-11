package learning

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestObjectiveIndexPreservesFrozenIdentityAndLegacyEvidence(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	objectives := []types.LearningObjective{objFixture("a", "concept/a"), objFixture("b", "concept/b"), objFixture("task", "concept/task"), objFixture("empty", "concept/empty")}
	objectives[2].ContractType = types.ObjectiveContractTaskChecks
	attempts := []types.LearningQuizAttempt{
		attemptFixture("a1", "a", "one", true, now), attemptFixture("a2", "a", "two", true, now.Add(time.Hour)),
		attemptFixture("old", "b", "one", true, now), attemptFixture("practice", "b", "two", false, now),
		attemptFixture("legacy", "", "", true, now),
	}
	attempts[2].ContentVersion = "old"
	attempts[3].Eligible = false
	attempts[4].QuizItemID = "legacy-item"
	// A later bank edit must not relocate a frozen trial from a to b.
	legacy := map[string]string{attempts[0].QuizItemID: "b", "legacy-item": "b"}
	tasks := []types.LearningTaskAttempt{{ID: "t1", TaskID: "task-item", ObjectiveID: "task", TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, FamilyID: "task-one", ObjectiveVersion: "v1", ContractVersion: types.ObjectiveContractVersion, ScorerVersion: types.TaskScorerVersion, RubricVersion: "r1", Eligible: true, IsPassed: true, SubmittedAt: now}}
	idx := indexObjectiveFacts(attempts, legacy, tasks)
	for _, o := range objectives {
		want := DeriveObjectiveState(o, attempts, legacy, tasks)
		got := DeriveObjectiveState(o, idx.quiz[o.ID], legacy, idx.tasks[o.ID])
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("index changed %s: got %+v, want %+v", o.ID, got, want)
		}
	}
	if got := DeriveObjectiveState(objectives[0], idx.quiz["a"], legacy, nil); got.State != types.ObjectiveStateVerified {
		t.Fatalf("frozen identity was lost: %+v", got)
	}
	if got := DeriveObjectiveState(objectives[1], idx.quiz["b"], legacy, nil); got.Evidence.LegacyAttempts != 1 || got.State == types.ObjectiveStateVerified {
		t.Fatalf("legacy evidence was promoted or lost: %+v", got)
	}
	// Mixed subjects remain invalid; indexing must not accidentally hide one.
	foreign := attempts[0]
	foreign.ID = "foreign"
	foreign.SubjectID = "web_user:bob"
	attempts = append(attempts, foreign)
	idx = indexObjectiveFacts(attempts, legacy, tasks)
	if got := DeriveObjectiveState(objectives[0], idx.quiz["a"], legacy, nil); got.State == types.ObjectiveStateVerified {
		t.Fatal("index concealed mixed subjects")
	}
}

func TestObjectiveIndexKeepsFeedbackExposedItemsUnavailable(t *testing.T) {
	idx := indexObjectiveFacts([]types.LearningQuizAttempt{{QuizItemID: "existing", ObjectiveID: "a", FamilyID: "family", Eligible: false}}, nil, []types.LearningTaskAttempt{{TaskID: "task-existing", ObjectiveID: "b", FamilyID: "task-family", Eligible: false}})
	for _, item := range []types.LearningQuizItem{{ID: "existing", ObjectiveID: "moved", FamilyID: "new"}, {ID: "clone", ObjectiveID: "a", FamilyID: "family"}} {
		if !idx.quizSeen(item) {
			t.Fatal("feedback-exposed item can be counted as new")
		}
	}
	if idx.quizSeen(types.LearningQuizItem{ID: "fresh", ObjectiveID: "other", FamilyID: "family"}) {
		t.Fatal("family from another objective was consumed")
	}
	if !idx.taskSeen(types.LearningTask{ID: "clone", ObjectiveID: "b", FamilyID: "task-family"}) || !idx.taskSeen(types.LearningTask{ID: "task-existing"}) {
		t.Fatal("task feedback was forgotten")
	}
	if idx.taskSeen(types.LearningTask{ID: "fresh", ObjectiveID: "a", FamilyID: "task-family"}) {
		t.Fatal("independent task was suppressed")
	}
}

// CPU-only comparison including construction of the request-local index.
// It does not measure SQL queries, network transfer or browser rendering.
func BenchmarkObjectiveHistoryTenThousand(b *testing.B) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	objectives := make([]types.LearningObjective, 10000)
	attempts := make([]types.LearningQuizAttempt, 0, 60000)
	for i := range objectives {
		id := fmt.Sprintf("objective-%d", i)
		objectives[i] = types.LearningObjective{ID: id, TenantID: 1, KnowledgeBaseID: testKB, ContractType: types.ObjectiveContractConceptTwoFamily, ContractVersion: types.ObjectiveContractVersion, ContentVersion: "v1", Status: types.LearningObjectiveStatusPublished}
		for j := 0; j < 6; j++ {
			attempts = append(attempts, attemptFixture(fmt.Sprintf("%d-%d", i, j), id, fmt.Sprintf("family-%d", j), true, now))
		}
	}
	for _, indexed := range []bool{false, true} {
		name := "full_scan"
		if indexed {
			name = "request_index"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var idx objectiveFactIndex
				if indexed {
					idx = indexObjectiveFacts(attempts, nil, nil)
				}
				verified := 0
				for _, o := range objectives {
					facts := attempts
					if indexed {
						facts = idx.quiz[o.ID]
					}
					if DeriveObjectiveState(o, facts, nil, nil).State == types.ObjectiveStateVerified {
						verified++
					}
				}
				if verified != len(objectives) {
					b.Fatalf("wrong verified count %d", verified)
				}
			}
		})
	}
}
