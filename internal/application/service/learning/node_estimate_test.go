package learning

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func modelPages() []*types.WikiPage {
	return []*types.WikiPage{{Slug: "a", Title: "A", Content: "一个简短的知识点", PageType: "concept"}, {Slug: "b", Title: "B", Content: "另一个简短的知识点", PageType: "concept"}}
}

func TestModelReadingAutomaticallyAdvancesPath(t *testing.T) {
	pages := modelPages()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	cold := deriveNodeEstimates(pages, nil, nil, nil, nil, now)
	event := types.LearningEvent{ID: "r", Slug: "a", Type: types.LearningEventWikiDeepRead, ContentVersion: nodeContentVersion(pages[0]), OccurredAt: now.Add(-time.Minute)}
	warm := deriveNodeEstimates(pages, []types.LearningEvent{event}, nil, nil, nil, now)
	if warm["a"].Familiarity != cold["a"].Familiarity || warm["a"].Level != "developing" || warm["a"].Corrections != 0 || warm["a"].Answers != 0 || warm["a"].Memory != nil {
		t.Fatalf("reading was not a distinct automatic opportunity: %+v", warm["a"])
	}
	in := planInput{IncludeExploration: true, GoalsResolved: true, Pages: map[string]string{"a": "A", "b": "B"}, PageMinutes: map[string]int{"a": 1, "b": 1}, DocOrder: map[string]int{"a": 0, "b": 1}, TimeBudgetMinutes: 2, Estimates: cold}
	if p := planShortPath(in); len(p.Steps) != 2 || p.Steps[0].Slug != "a" {
		t.Fatal(p)
	}
	in.Estimates = warm
	in.Exposure = map[string]bool{"a": true}
	in.Recent = []RecentNode{{Slug: "a"}}
	plan := planShortPath(in)
	if len(plan.Steps) == 0 || plan.Steps[0].Slug != "b" {
		t.Fatalf("reading did not advance automatically: %+v", plan)
	}
	for _, step := range plan.Steps {
		if step.Action == ActionConfirm {
			t.Fatal("model path requires an explicit completion declaration")
		}
	}
}

func TestModelCitationsAndRepeatedQuestionsAreNotAnswerLabels(t *testing.T) {
	pages := modelPages()
	now := time.Now()
	events := []types.LearningEvent{}
	for i := 0; i < 100; i++ {
		kind := types.LearningEventAnswerCite
		if i%2 == 0 {
			kind = types.LearningEventReAsk
		}
		events = append(events, types.LearningEvent{ID: fmt.Sprint(i), Slug: "a", Type: kind, OccurredAt: now.Add(-time.Minute)})
	}
	cold := deriveNodeEstimates(pages, nil, nil, nil, nil, now)["a"]
	got := deriveNodeEstimates(pages, events, nil, nil, nil, now)["a"]
	if got.Familiarity != cold.Familiarity || got.Answers != 0 || got.Memory != nil {
		t.Fatalf("citation/question history fabricated mastery: %+v", got)
	}
}

func TestModelCorrectionsAreBoundedAndRepeatedDeclarationsDoNotAccumulate(t *testing.T) {
	pages := modelPages()
	now := time.Now()
	version := nodeContentVersion(pages[0])
	e := types.LearningEvent{ID: "k", Slug: "a", Type: types.LearningEventNodeKnown, ContentVersion: version, OccurredAt: now.Add(-time.Hour)}
	want := deriveNodeEstimates(pages, []types.LearningEvent{e}, nil, nil, nil, now)["a"]
	events := []types.LearningEvent{e}
	for i := 0; i < 30; i++ {
		next := e
		next.ID = fmt.Sprint(i)
		next.OccurredAt = next.OccurredAt.Add(time.Duration(i+1) * time.Second)
		events = append(events, next)
	}
	got := deriveNodeEstimates(pages, events, nil, nil, nil, now)["a"]
	if got.Familiarity != want.Familiarity || got.Corrections != 1 || got.Answers != 0 || got.Memory != nil {
		t.Fatalf("repeated self-report became mastery evidence: %+v", got)
	}
	e.Type = types.LearningEventNodeReview
	difficult := deriveNodeEstimates(pages, []types.LearningEvent{e}, nil, nil, nil, now)["a"]
	if difficult.Familiarity != want.Familiarity || difficult.Level != "review" {
		t.Fatal(difficult)
	}
}

func TestModelProjectionIsCausalVersionedAndOrderIndependent(t *testing.T) {
	pages := modelPages()
	now := time.Now().UTC()
	version := nodeContentVersion(pages[0])
	old := types.LearningEvent{ID: "old", Slug: "a", Type: types.LearningEventWikiDeepRead, ContentVersion: "old-content", OccurredAt: now.Add(-time.Hour)}
	future := old
	future.ID = "future"
	future.ContentVersion = version
	future.OccurredAt = now.Add(time.Hour)
	if got := deriveNodeEstimates(pages, []types.LearningEvent{old, future}, nil, nil, nil, now)["a"]; got.Level != "unseen" || got.Opportunities != 0 {
		t.Fatal(got)
	}
	a := old
	a.ID = "a"
	a.ContentVersion = version
	a.OccurredAt = now.Add(-2 * time.Minute)
	b := a
	b.ID = "b"
	b.OccurredAt = now.Add(-time.Minute)
	forward := deriveNodeEstimates(pages, []types.LearningEvent{a, b}, nil, nil, nil, now)
	backward := deriveNodeEstimates(pages, []types.LearningEvent{b, a, a}, nil, nil, nil, now)
	if !reflect.DeepEqual(forward, backward) {
		t.Fatal("arrival order/duplicates changed the model")
	}
}

func TestModelIndependentAnswersCalibrateAndFailuresCannotPretendLearning(t *testing.T) {
	pages := modelPages()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	o := objFixture("o", "a")
	defs := []types.LearningObjective{o}
	base := deriveNodeEstimates(pages, nil, nil, nil, defs, now)["a"]
	a := attemptFixture("pass", "o", "f1", true, now.Add(-time.Hour))
	a.Slug = "a"
	passed := deriveNodeEstimates(pages, nil, []types.LearningQuizAttempt{a}, nil, defs, now)["a"]
	a.IsCorrect = false
	failed := deriveNodeEstimates(pages, nil, []types.LearningQuizAttempt{a}, nil, defs, now)["a"]
	if passed.Answers != 1 || passed.Familiarity <= base.Familiarity || failed.Answers != 1 || failed.Familiarity >= base.Familiarity || failed.Level != "review" || passed.Memory != nil {
		t.Fatalf("answer must calibrate without invented reading/recall: base=%+v pass=%+v fail=%+v", base, passed, failed)
	}
	for _, variant := range []string{"old", "assisted", "draft", "legacy", "future"} {
		b := a
		switch variant {
		case "old":
			b.ContentVersion = "old"
		case "assisted":
			b.AssistanceMode = types.AssistanceAssistantHelp
		case "draft":
			b.ItemStatus = types.LearningQuizStatusDraft
		case "legacy":
			b.ScorerVersion = ""
		case "future":
			b.AnsweredAt = now.Add(time.Hour)
		}
		if got := deriveNodeEstimates(pages, nil, []types.LearningQuizAttempt{b}, nil, defs, now)["a"]; got.Answers != 0 || got.Familiarity != base.Familiarity {
			t.Fatalf("%s answer leaked into estimate: %+v", variant, got)
		}
	}
	b := a
	b.ID, b.IsCorrect, b.AnsweredAt = "retry", true, now.Add(-time.Minute)
	if got := deriveNodeEstimates(pages, nil, []types.LearningQuizAttempt{a, b}, nil, defs, now)["a"]; got.Answers != 1 || got.Familiarity != failed.Familiarity {
		t.Fatal("feedback-exposed retry became an independent observation", got)
	}
}

func TestModelDifficultCorrectionDoesNotRequireAnotherDeclarationToContinue(t *testing.T) {
	pages := modelPages()
	now := time.Now()
	event := types.LearningEvent{ID: "difficulty", Slug: "a", Type: types.LearningEventNodeReview, ContentVersion: nodeContentVersion(pages[0]), OccurredAt: now.Add(-time.Hour)}
	before := deriveNodeEstimates(pages, []types.LearningEvent{event}, nil, nil, nil, now)["a"]
	read := event
	read.ID, read.Type, read.OccurredAt = "read", types.LearningEventWikiDeepRead, now.Add(-time.Minute)
	after := deriveNodeEstimates(pages, []types.LearningEvent{event, read}, nil, nil, nil, now)["a"]
	if before.Level != "review" || after.Level != "developing" || after.Corrections != 1 || after.Familiarity != before.Familiarity || after.Answers != 0 {
		t.Fatalf("reading after difficulty failed to advance: %+v -> %+v", before, after)
	}
	event.Type = types.LearningEventNodeKnown
	known := deriveNodeEstimates(pages, []types.LearningEvent{event}, nil, nil, nil, now)["a"]
	if known.Level != "self_reported" || known.Answers != 0 || known.Memory != nil {
		t.Fatalf("a familiarity correction must affect the estimate without verification: %+v", known)
	}
}

func TestLaterDeepReadCannotRewriteAnEarlierAnswerPrediction(t *testing.T) {
	pages := modelPages()
	now := time.Now().UTC()
	o := objFixture("o", "a")
	read := types.LearningEvent{ID: "normal", Slug: "a", Type: types.LearningEventWikiToolRead, ContentVersion: nodeContentVersion(pages[0]), OccurredAt: now.Add(-3 * time.Minute)}
	a := attemptFixture("answer", "o", "f", true, now.Add(-2*time.Minute))
	a.Slug = "a"
	before := deriveNodeEstimates(pages, []types.LearningEvent{read}, []types.LearningQuizAttempt{a}, nil, []types.LearningObjective{o}, now)["a"]
	deep := read
	deep.ID, deep.Type, deep.OccurredAt = "deep", types.LearningEventWikiDeepRead, now.Add(-time.Minute)
	after := deriveNodeEstimates(pages, []types.LearningEvent{read, deep}, []types.LearningQuizAttempt{a}, nil, []types.LearningObjective{o}, now)["a"]
	if before.LastAnswerPrediction == 0 || before.LastAnswerPrediction != after.LastAnswerPrediction || after.Opportunities != 1 || after.Coverage != 1 || after.Familiarity != before.Familiarity {
		t.Fatalf("later read changed the earlier forecast or double-counted coverage: %+v -> %+v", before, after)
	}
}

func TestModelStructuredTaskEvidenceIsIndependentCurrentAndUnassisted(t *testing.T) {
	pages := modelPages()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	o := objFixture("task", "a")
	o.ContractType = types.ObjectiveContractTaskChecks
	defs := []types.LearningObjective{o}
	base := deriveNodeEstimates(pages, nil, nil, nil, defs, now)["a"]
	a := types.LearningTaskAttempt{ID: "task-answer", TenantID: o.TenantID, KnowledgeBaseID: o.KnowledgeBaseID, SubjectID: "web_user:alice", ObjectiveID: o.ID, FamilyID: "task-family", ObjectiveVersion: o.ContentVersion, ContractVersion: o.ContractVersion, ScorerVersion: types.TaskScorerVersion, RubricVersion: "v1", ContentVersion: "v1", Eligible: true, IsPassed: true, SubmittedAt: now.Add(-time.Hour)}
	passed := deriveNodeEstimates(pages, nil, nil, []types.LearningTaskAttempt{a}, defs, now)["a"]
	if passed.Answers != 1 || passed.Familiarity <= base.Familiarity || passed.Memory != nil || passed.Opportunities != 0 {
		t.Fatalf("valid task did not calibrate without inventing reading/recall: %+v", passed)
	}
	a.IsPassed = false
	failed := deriveNodeEstimates(pages, nil, nil, []types.LearningTaskAttempt{a}, defs, now)["a"]
	if failed.Answers != 1 || failed.Familiarity >= base.Familiarity || failed.Level != "review" {
		t.Fatalf("task failure did not lower the estimate: %+v", failed)
	}
	for _, variant := range []string{"old", "assisted", "legacy", "future", "ineligible", "foreign"} {
		b := a
		switch variant {
		case "old":
			b.ObjectiveVersion = "old"
		case "assisted":
			b.AssistanceMode = types.AssistanceAssistantHelp
		case "legacy":
			b.ScorerVersion = ""
		case "future":
			b.SubmittedAt = now.Add(time.Hour)
		case "ineligible":
			b.Eligible = false
		case "foreign":
			b.TenantID++
		}
		if got := deriveNodeEstimates(pages, nil, nil, []types.LearningTaskAttempt{b}, defs, now)["a"]; got.Answers != 0 || got.Familiarity != base.Familiarity {
			t.Fatalf("%s task leaked into model: %+v", variant, got)
		}
	}
	b := a
	b.ID, b.IsPassed, b.SubmittedAt = "retry", true, now.Add(-time.Minute)
	if got := deriveNodeEstimates(pages, nil, nil, []types.LearningTaskAttempt{b, a, a}, defs, now)["a"]; got.Answers != 1 || got.Familiarity != failed.Familiarity {
		t.Fatalf("duplicate or feedback-exposed task retry counted independently: %+v", got)
	}
}

func TestPageReadingYearCannotInventPerformanceOrRepeatFinishedMaterial(t *testing.T) {
	pages := modelPages()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	baseline := deriveNodeEstimates(pages, nil, nil, nil, nil, now)
	events := []types.LearningEvent{}
	for day := 365; day > 0; day-- {
		for _, p := range pages {
			events = append(events, types.LearningEvent{ID: fmt.Sprintf("%s-%d", p.Slug, day), Slug: p.Slug, Type: types.LearningEventWikiDeepRead, ContentVersion: nodeContentVersion(p), OccurredAt: now.Add(-time.Duration(day) * 24 * time.Hour)})
		}
	}
	result := deriveNodeEstimates(pages, events, nil, nil, nil, now)
	for slug, e := range result {
		if e.Familiarity != baseline[slug].Familiarity || e.PerformanceObserved || e.Answers != 0 || e.ExpectedGain != 0 || e.ReadPriority != 0 || e.Level != "developing" {
			t.Fatalf("passive reads invented performance: %+v", e)
		}
	}
	plan := planShortPath(planInput{Estimates: result, Pages: map[string]string{"a": "A", "b": "B"}, GoalsResolved: true, IncludeExploration: true, TimeBudgetMinutes: 15})
	if len(plan.Steps) != 0 {
		t.Fatalf("finished reads repeatedly recommended without new evidence: %+v", plan)
	}
}

func TestPageAnswersStayWithinObjectiveAndNeedEveryContract(t *testing.T) {
	pages := modelPages()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	defs := []types.LearningObjective{objFixture("one", "a"), objFixture("two", "a")}
	one := []types.LearningQuizAttempt{attemptFixture("1", "one", "f1", true, now.Add(-4*time.Hour)), attemptFixture("2", "one", "f2", true, now.Add(-3*time.Hour))}
	partial := deriveNodeEstimates(pages, nil, one, nil, defs, now)["a"]
	if partial.Level == "familiar" || !partial.PerformanceObserved {
		t.Fatalf("one objective promoted whole page: %+v", partial)
	}
	firstOther := attemptFixture("3", "two", "f3", true, now.Add(-2*time.Hour))
	fresh := deriveNodeEstimates(pages, nil, []types.LearningQuizAttempt{firstOther}, nil, defs, now)["a"]
	mixed := deriveNodeEstimates(pages, nil, append(one, firstOther), nil, defs, now)["a"]
	if mixed.LastAnswerPrediction != fresh.LastAnswerPrediction || mixed.Level == "familiar" {
		t.Fatalf("another objective's evidence leaked into prediction: fresh=%+v mixed=%+v", fresh, mixed)
	}
	complete := append(append([]types.LearningQuizAttempt{}, one...), firstOther, attemptFixture("4", "two", "f4", true, now.Add(-time.Hour)))
	verified := deriveNodeEstimates(pages, nil, complete, nil, defs, now)["a"]
	if verified.Level != "familiar" || verified.Answers != 4 {
		t.Fatalf("all independent contracts were not reflected: %+v", verified)
	}
	repeat := one[0]
	repeat.ID = "repeat"
	repeat.AnsweredAt = now.Add(-time.Minute)
	original := one[0]
	original.AnsweredAt = now.Add(-7 * 24 * time.Hour)
	repeated := deriveNodeEstimates(pages, nil, []types.LearningQuizAttempt{original, repeat}, nil, defs, now)["a"]
	if repeated.Answers != 1 {
		t.Fatalf("known family became fresh evidence after cooldown: %+v", repeated)
	}
}

func TestPageFeedbackChangesIntentAndNewEvidenceSupersedesIt(t *testing.T) {
	pages := modelPages()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	known := types.LearningEvent{ID: "known", Slug: "a", Type: types.LearningEventNodeKnown, ContentVersion: nodeContentVersion(pages[0]), OccurredAt: now.Add(-3 * time.Hour)}
	base := deriveNodeEstimates(pages, nil, nil, nil, nil, now)["a"]
	reported := deriveNodeEstimates(pages, []types.LearningEvent{known}, nil, nil, nil, now)["a"]
	if reported.Familiarity != base.Familiarity || reported.PerformanceObserved || reported.Level != "self_reported" || reported.ReadPriority != 0 {
		t.Fatalf("self-report became ability: %+v", reported)
	}
	fail := attemptFixture("failure", "o", "f", false, now.Add(-2*time.Hour))
	result := deriveNodeEstimates(pages, []types.LearningEvent{known}, []types.LearningQuizAttempt{fail}, nil, []types.LearningObjective{objFixture("o", "a")}, now)["a"]
	if result.SelfReport != "" || result.Level != "review" || !result.PerformanceObserved {
		t.Fatalf("new evidence did not supersede report: %+v", result)
	}
	difficulty := known
	difficulty.ID = "difficulty"
	difficulty.Type = types.LearningEventNodeReview
	read := known
	read.ID = "read"
	read.Type = types.LearningEventWikiDeepRead
	read.OccurredAt = now.Add(-2 * time.Hour)
	again := difficulty
	again.ID = "difficulty-again"
	again.OccurredAt = now.Add(-time.Hour)
	renewed := deriveNodeEstimates(pages, []types.LearningEvent{difficulty, read, again}, nil, nil, nil, now)["a"]
	if renewed.Level != "review" || renewed.ReadPriority != 1.2 || renewed.PerformanceObserved {
		t.Fatalf("new difficulty after reread was ignored: %+v", renewed)
	}
}
