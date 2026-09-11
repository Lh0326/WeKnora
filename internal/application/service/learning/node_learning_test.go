package learning

import (
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"reflect"
	"testing"
	"time"
)

func TestNodeLearningWithoutQuestionBankClosesAndPersists(t *testing.T) {
	svc, _, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")
	state := func() string {
		t.Helper()
		view, err := svc.ObjectiveView(ctx, testKB)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range view.Nodes {
			if n.Slug == "concept/rag" {
				return n.State
			}
		}
		t.Fatal("node missing")
		return ""
	}
	if got := state(); got != "unseen" {
		t.Fatal(got)
	}
	for _, step := range []struct{ write, want string }{{"read", "learning"}, {"known", "self_known"}, {"known", "self_known"}, {"review", "review"}, {"read", "learning"}} {
		if err := svc.SetNodeState(ctx, testKB, "concept/rag", step.write); err != nil {
			t.Fatal(err)
		}
		if got := state(); got != step.want {
			t.Fatalf("%s => %s, want %s", step.write, got, step.want)
		}
	}
	other, err := svc.ObjectiveView(collectorCtx(1, "bob"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range other.Nodes {
		if n.State != "unseen" {
			t.Fatalf("another person's declaration leaked: %+v", n)
		}
	}
	if err := svc.SetNodeState(ctx, testKB, "concept/not-present", "known"); err != ErrWikiReadTarget {
		t.Fatalf("invalid node accepted: %v", err)
	}
}

func TestNodeLearningContentUpdateAndEvidenceRemainSeparate(t *testing.T) {
	page := &types.WikiPage{Slug: "concept/a", Title: "A", Content: "original"}
	now := time.Now()
	event := types.LearningEvent{ID: "1", Slug: page.Slug, Type: types.LearningEventNodeKnown, ContentVersion: nodeContentVersion(page), OccurredAt: now}
	objectives := []interfaces.ObjectiveViewEntry{{Slug: page.Slug, ObjectiveStatus: types.LearningObjectiveStatusPublished, State: types.ObjectiveStateConflicting}}
	derive := func() interfaces.LearningNodeView {
		return deriveLearningNodes([]*types.WikiPage{page}, objectives, []types.LearningEvent{event}, nil)[0]
	}
	if n := derive(); n.State != "self_known" || n.ObjectiveVerified != 0 {
		t.Fatalf("declaration must work without certifying conflicting evidence: %+v", n)
	}
	page.Content = "new content"
	if n := derive(); n.State != "review" || !n.Updated {
		t.Fatalf("updated content must request confirmation: %+v", n)
	}
	event.ContentVersion = nodeContentVersion(page)
	if n := derive(); n.State != "self_known" || n.Updated {
		t.Fatalf("reconfirmation must settle personal queue: %+v", n)
	}
}

func TestNodeLearningIgnoresAgentReadsAndReplaysDeterministically(t *testing.T) {
	page := &types.WikiPage{Slug: "a", Content: "text"}
	events := []types.LearningEvent{{Slug: "a", Type: types.LearningEventAgentRead, OccurredAt: time.Now()}}
	if n := deriveLearningNodes([]*types.WikiPage{page}, nil, events, nil)[0]; n.State != "unseen" {
		t.Fatal(n)
	}
	at := time.Now()
	events = []types.LearningEvent{{ID: "b", Slug: "a", Type: types.LearningEventNodeReview, OccurredAt: at}, {ID: "a", Slug: "a", Type: types.LearningEventNodeKnown, OccurredAt: at}}
	first := deriveLearningNodes([]*types.WikiPage{page}, nil, events, nil)
	events[0], events[1] = events[1], events[0]
	if second := deriveLearningNodes([]*types.WikiPage{page}, nil, events, nil); !reflect.DeepEqual(first, second) || second[0].State != "review" {
		t.Fatal(second)
	}
}

func TestAllNodePathReadConfirmKnownReviewAndBudget(t *testing.T) {
	in := planInput{IncludeExploration: true, GoalsResolved: true, Pages: map[string]string{"a": "A", "b": "B", "c": "C"}, PageMinutes: map[string]int{"a": 1, "b": 2, "c": 8}, DocOrder: map[string]int{"a": 0, "b": 1, "c": 2}, Exposure: map[string]bool{}, Skips: map[string]bool{}, NodeReview: map[string]bool{}, NodeVerified: map[string]bool{}, TimeBudgetMinutes: 5}
	first := planShortPath(in)
	if len(first.Steps) != 2 || first.Steps[0].Slug != "a" || first.Steps[0].Action != ActionRead {
		t.Fatalf("fresh: %+v", first)
	}
	in.Exposure["a"] = true
	in.Recent = []RecentNode{{Slug: "a", At: time.Now()}}
	if next := planShortPath(in); next.Steps[0].Action != ActionConfirm {
		t.Fatalf("read must lead to confirmation: %+v", next)
	}
	in.Skips["a"] = true
	if next := planShortPath(in); next.Steps[0].Slug != "b" {
		t.Fatalf("known should advance: %+v", next)
	}
	in.Skips["a"] = false
	in.NodeReview["a"] = true
	if next := planShortPath(in); next.Steps[0].Slug != "a" || next.Steps[0].Action != ActionRead {
		t.Fatalf("review should return: %+v", next)
	}
	in.PageScope = map[string]bool{"c": true}
	if next := planShortPath(in); len(next.Steps) != 0 || next.Degrade != "plan_degrade_budget" {
		t.Fatalf("budget must not be exceeded: %+v", next)
	}
}

func TestAllNodePathPrerequisitesAndExclusions(t *testing.T) {
	in := planInput{IncludeExploration: true, GoalsResolved: true, Pages: map[string]string{"a": "A", "b": "B"}, PageScope: map[string]bool{"b": true}, StrictEdges: map[string][]string{"a": {"b"}}, PageMinutes: map[string]int{"a": 1, "b": 1}}
	p := planShortPath(in)
	if len(p.Steps) != 2 || p.Steps[0].Slug != "a" || p.Steps[1].Slug != "b" || len(p.Steps[1].Requires) == 0 {
		t.Fatalf("prerequisites: %+v", p)
	}
	in.ExcludedSlugs = map[string]bool{"a": true}
	if p = planShortPath(in); len(p.Steps) != 0 {
		t.Fatalf("excluded required predecessor must not be silently assumed: %+v", p)
	}
}
