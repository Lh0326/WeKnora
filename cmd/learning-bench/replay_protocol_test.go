package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
)

func replayTestEvent(id, slug string, minute int) types.LearningEvent {
	return types.LearningEvent{ID: id, TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb", Slug: slug, Type: types.LearningEventAnswerCite, Weight: 1, OccurredAt: time.Date(2026, 9, 1, 0, minute, 0, 0, time.UTC)}
}

func TestReplayQuizEligibilityCannotSeeFutureForKnownNode(t *testing.T) {
	events := []types.LearningEvent{replayTestEvent("1", "concept/b", 0), replayTestEvent("2", "concept/a", 1), replayTestEvent("3", "concept/b", 2)}
	events[2].Type = types.LearningEventQuizCorrect
	input := replayInput{Events: events}
	past := buildReplayState(input, events[:2], eventScope(events[0]), events[2].OccurredAt)
	if len(past.in.Pages) != 2 {
		t.Fatal("B must already be in the candidate universe; absence cannot mask a quiz leak")
	}
	if past.in.HasQuiz["concept/b"] {
		t.Fatal("future quiz eligibility leaked onto a known node")
	}
	after := buildReplayState(input, events, eventScope(events[0]), events[2].OccurredAt.Add(time.Minute))
	if !after.in.HasQuiz["concept/b"] {
		t.Fatal("positive control: observed quiz should update proxy eligibility")
	}
}

func TestReplayScoresEqualTimeCohortBeforeObservingAnyAnswer(t *testing.T) {
	events := []types.LearningEvent{replayTestEvent("1", "concept/a", 0), replayTestEvent("2", "concept/b", 1), replayTestEvent("3", "concept/b", 1)}
	events[1].Type = types.LearningEventQuizCorrect
	events[2].Type = types.LearningEventQuizWrong
	r := evaluateOnlineReplay(replayInput{}, events, 1, []int{5})
	for _, m := range r.metrics {
		if m.Predictions != 2 || m.HitsByK[5] != 0 {
			t.Fatalf("same-time answers leaked into each other: %+v", m)
		}
	}
	if split := replaySplit(events); split != 1 {
		t.Fatalf("split cut a same-time cohort: %d", split)
	}
}

func TestReplayScopesAndAgentEventsCannotContaminateInputs(t *testing.T) {
	a := replayTestEvent("1", "concept/same", 0)
	scope := eventScope(a)
	now := a.OccurredAt.Add(time.Hour)
	base := buildReplayState(replayInput{}, []types.LearningEvent{a}, scope, now)
	other := a
	other.ID = "other"
	other.KnowledgeBaseID = "other-kb"
	other.Weight = 100
	agent := a
	agent.ID = "agent"
	agent.Slug = "concept/agent-only"
	agent.Type = types.LearningEventAgentRead
	got := buildReplayState(replayInput{}, []types.LearningEvent{a, other, agent}, scope, now)
	if !reflect.DeepEqual(base, got) {
		t.Fatalf("foreign scope or agent event contaminated state: %+v vs %+v", base, got)
	}
	late := a
	late.ID = "late"
	late.CreatedAt = now.Add(time.Hour)
	got = buildReplayState(replayInput{}, []types.LearningEvent{a, late}, scope, now)
	if !reflect.DeepEqual(base, got) {
		t.Fatal("event not yet arrived was used as historical evidence")
	}
}

func TestReplayHistoricalSnapshotCarriesProductionInputs(t *testing.T) {
	e := replayTestEvent("1", "concept/a", 0)
	now := e.OccurredAt.Add(time.Hour)
	snap := replaySnapshot{TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb", At: e.OccurredAt.Add(time.Minute), Input: learning.BenchRecommendInput{
		Pages:   []*types.WikiPage{{Slug: "concept/a", PageType: "concept"}, {Slug: "concept/b", PageType: "concept"}},
		HasQuiz: map[string]bool{"concept/b": true}, Skips: map[string]bool{"concept/a": true}, DocOrder: map[string]int{"concept/b": 2},
		Material: map[string]learning.NodeMaterial{"concept/b": {DocID: "doc", Section: "chapter"}},
		Edges:    []types.LearningEdge{{TenantID: 1, KnowledgeBaseID: "kb", FromSlug: "concept/a", ToSlug: "concept/b", Relation: types.LearningEdgePrerequisite}},
	}}
	input := replayInput{Snapshots: []replaySnapshot{snap}, Attempts: []types.LearningQuizAttempt{{ID: "attempt", TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb", Slug: "concept/a", QuizItemID: "q", ChosenKey: "A", IsCorrect: true, AnsweredAt: e.OccurredAt.Add(time.Minute)}}}
	before := buildReplayState(input, []types.LearningEvent{e}, eventScope(e), snap.At)
	if before.historical || before.in.HasQuiz["concept/b"] || len(before.in.Edges) > 0 {
		t.Fatal("future or equal-time metadata leaked")
	}
	got := buildReplayState(input, []types.LearningEvent{e}, eventScope(e), now)
	if !got.historical || len(got.in.Pages) != 1 || got.in.Pages[0].Slug != "concept/b" || got.in.DocOrder["concept/b"] != 2 || got.in.Material["concept/b"].Section != "chapter" || len(got.in.DirectFacts["concept/a"]) != 1 || len(got.in.Edges) != 1 {
		t.Fatalf("production input lost at bench boundary: %+v", got.in)
	}
	lists := rankReplayState(got, now, 5, 42)
	for _, list := range lists {
		for _, slug := range list {
			if slug == "concept/a" {
				t.Fatal("baseline ignored shared skip eligibility")
			}
		}
	}
	// Old current-graph exports never authorize past graph knowledge.
	legacy := buildReplayState(replayInput{Edges: snap.Input.Edges}, []types.LearningEvent{e}, eventScope(e), now)
	if len(legacy.in.Edges) != 0 {
		t.Fatal("current exported graph was used as a historical snapshot")
	}
}

func TestReplayLoadsBothExportShapes(t *testing.T) {
	events := []types.LearningEvent{replayTestEvent("1", "a", 0)}
	for _, payload := range []any{replayInput{Events: events}, map[string]any{"data": replayInput{Events: events}}} {
		path := filepath.Join(t.TempDir(), "export.json")
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := loadReplayInput(path)
		if err != nil || len(got.Events) != 1 {
			t.Fatalf("export format rejected: %+v %v", got, err)
		}
	}
}

func TestStaticRankerIsInvokedOncePerExperiment(t *testing.T) {
	calls := 0
	metrics := evalOne("static", func() []string { calls++; return []string{"a", "b"} }, []string{"a", "b"}, []int{2})
	if calls != 1 || metrics.HitsByK[2] != 2 {
		t.Fatalf("static ranking was resampled per truth: calls=%d metrics=%+v", calls, metrics)
	}
}

func TestReplayRenamePreservesHistoricalAttemptAndTargetIdentity(t *testing.T) {
	e := replayTestEvent("old", "concept/new", 0)
	e.OriginalSlug = "concept/old"
	a := types.LearningQuizAttempt{ID: "attempt", TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb", Slug: "concept/new", OriginalSlug: "concept/old", QuizItemID: "q", ChosenKey: "A", IsCorrect: true, AnsweredAt: e.OccurredAt}
	input := replayInput{Attempts: []types.LearningQuizAttempt{a}}
	before := buildReplayState(input, []types.LearningEvent{e}, eventScope(e), e.OccurredAt.Add(time.Minute))
	if _, ok := before.in.DirectFacts["concept/old"]; !ok {
		t.Fatal("historical attempt lost its original node")
	}
	if replayTargetSlug(before, e) != "concept/old" {
		t.Fatal("future rename leaked into target")
	}
	input.Snapshots = []replaySnapshot{{TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb", At: e.OccurredAt.Add(time.Minute), Input: learning.BenchRecommendInput{Pages: []*types.WikiPage{{Slug: "concept/new", PageType: "concept", Aliases: []string{"concept/old"}}}}}}
	after := buildReplayState(input, []types.LearningEvent{e}, eventScope(e), e.OccurredAt.Add(2*time.Minute))
	if _, ok := after.in.DirectFacts["concept/new"]; !ok {
		t.Fatal("past snapshot alias was not applied to the attempt")
	}
	if replayTargetSlug(after, e) != "concept/new" {
		t.Fatal("target and candidate identity diverged after rename")
	}
}

func TestReplaySkippingCandidateCannotChangeAliasMeaning(t *testing.T) {
	e := replayTestEvent("old", "concept/old", 0)
	input := replayInput{Snapshots: []replaySnapshot{{TenantID: 1, SubjectID: "s", KnowledgeBaseID: "kb", At: e.OccurredAt, Input: learning.BenchRecommendInput{
		Pages: []*types.WikiPage{{Slug: "concept/a", PageType: "concept", Aliases: []string{"concept/old"}}, {Slug: "concept/b", PageType: "concept", Aliases: []string{"concept/old"}}},
		Skips: map[string]bool{"concept/b": true},
	}}}}
	state := buildReplayState(input, []types.LearningEvent{e}, eventScope(e), e.OccurredAt.Add(time.Minute))
	if len(state.in.Pages) != 1 {
		t.Fatal("positive control: skipped candidate was not removed")
	}
	if replayTargetSlug(state, e) != "concept/old" {
		t.Fatal("removing a candidate changed an ambiguous historical identity")
	}
}
