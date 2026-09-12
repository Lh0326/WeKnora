package learning

import (
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestModelCheckIsInfrequentOptionalAndBudgeted(t *testing.T) {
	now := time.Now()
	o := objFixture("o", "a")
	in := planInput{AsOf: now, GoalsResolved: true, IncludeExploration: true, TimeBudgetMinutes: 15,
		Pages: map[string]string{"a": "A", "b": "B"}, PageMinutes: map[string]int{"a": 1, "b": 1},
		Objectives: map[string]types.LearningObjective{"o": o}, HasVerification: map[string]bool{"o": true},
		Estimates: map[string]*interfaces.LearningEstimate{"a": {Level: "developing", Coverage: 1, InformationGain: 0.3}, "b": {Level: "unseen", ReadPriority: 1}}}
	plan := planShortPath(in)
	if len(plan.Steps) != 1 || plan.Steps[0].Slug != "b" || plan.Steps[0].Action != ActionRead {
		t.Fatalf("a voluntary check must not precede a new reading focus: %+v", plan)
	}
	in.Estimates["b"].ReadPriority = 0
	plan = planShortPath(in)
	if len(plan.Steps) != 1 || plan.Steps[0].Reason.Code != "plan_reason_model_check" {
		t.Fatalf("the optional check remains available after reading actions: %+v", plan)
	}
	for _, scenario := range []string{"short", "recent", "missing", "excluded", "prerequisite"} {
		next := in
		switch scenario {
		case "short":
			next.TimeBudgetMinutes = 8
		case "recent":
			next.Estimates = map[string]*interfaces.LearningEstimate{"a": {Coverage: 1, InformationGain: .3, LastAnswerAt: now.Add(-time.Hour)}}
		case "missing":
			next.HasVerification = nil
		case "excluded":
			next.ExcludedSlugs = map[string]bool{"a": true}
		case "prerequisite":
			next.StrictEdges = map[string][]string{"b": {"a"}}
		}
		for _, step := range planShortPath(next).Steps {
			if step.Reason.Code == "plan_reason_model_check" {
				t.Fatalf("%s did not suppress diagnostic", scenario)
			}
		}
	}
}

func TestModelExplorationIsStableAndPreservesUtilityLeaders(t *testing.T) {
	in := planInput{ExplorationSeed: "user|kb|day", Estimates: map[string]*interfaces.LearningEstimate{}}
	candidates := []string{"a", "b", "c", "d", "e", "f", "g"}
	for _, slug := range candidates {
		in.Estimates[slug] = &interfaces.LearningEstimate{Level: "unseen"}
	}
	a, b := append([]string{}, candidates...), append([]string{}, candidates...)
	chosen := promoteModelExploration(in, a)
	if chosen == "" || chosen != promoteModelExploration(in, b) || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a[:4], candidates[:4]) || a[4] != chosen {
		t.Fatalf("exploration was not stable or displaced utility leaders: %v %v", a, b)
	}
	in.ExplorationSeed = ""
	c := append([]string{}, candidates...)
	if promoteModelExploration(in, c) != "" || !reflect.DeepEqual(c, candidates) {
		t.Fatal("missing seed must preserve order")
	}
}
