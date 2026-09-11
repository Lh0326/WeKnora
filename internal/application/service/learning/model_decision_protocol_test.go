package learning

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Comparative decision evidence, not a simulated claim of learning gains.
// Run with LEARNING_MODEL_REPORT to save the full reproducible comparison.
func TestModelDecisionProtocol(t *testing.T) {
	var protocol struct {
		Version string `json:"version"`
		Cases   []struct {
			ID      string   `json:"id"`
			Events  []string `json:"events"`
			Allowed []string `json:"allowed_first"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../../../docs/research/learning-model/decision-protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &protocol); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		Case     string            `json:"case"`
		Allowed  []string          `json:"allowed_first"`
		First    map[string]string `json:"first_action"`
		Accepted map[string]bool   `json:"accepted"`
	}
	report := struct {
		Protocol string    `json:"protocol"`
		Model    string    `json:"model"`
		Path     string    `json:"path"`
		Kind     string    `json:"kind"`
		Outcomes []outcome `json:"outcomes"`
	}{Protocol: protocol.Version, Model: NodeEstimateVersion, Path: PathPolicyVersion, Kind: "engineering_scenarios_not_learning_outcomes"}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, c := range protocol.Cases {
		pages := modelPages()
		events := []types.LearningEvent{}
		legacy := planInput{GoalsResolved: true, IncludeExploration: true, Pages: map[string]string{"a": "A", "b": "B"}, PageMinutes: map[string]int{"a": 1, "b": 1}, DocOrder: map[string]int{"a": 0, "b": 1}, TimeBudgetMinutes: 2, Skips: map[string]bool{}, Exposure: map[string]bool{}, NodeReview: map[string]bool{}}
		for i, action := range c.Events {
			slug := action[len(action)-1:]
			kind := map[string]string{"read": types.LearningEventWikiDeepRead, "known": types.LearningEventNodeKnown, "difficult": types.LearningEventNodeReview, "cite": types.LearningEventAnswerCite}[action[:len(action)-2]]
			page := pages[0]
			if slug == "b" {
				page = pages[1]
			}
			e := types.LearningEvent{ID: action, Slug: slug, Type: kind, ContentVersion: nodeContentVersion(page), OccurredAt: now.Add(time.Duration(-10+i) * time.Minute)}
			events = append(events, e)
			legacy.Recent = []RecentNode{{Slug: slug, At: e.OccurredAt}}
			if kind == types.LearningEventWikiDeepRead || kind == types.LearningEventAnswerCite {
				legacy.Exposure[slug] = true
			}
			if kind == types.LearningEventNodeKnown {
				legacy.Skips[slug] = true
			}
			if kind == types.LearningEventNodeReview {
				legacy.NodeReview[slug] = true
			}
		}
		model := legacy
		model.Estimates = deriveNodeEstimates(pages, events, nil, nil, nil, now)
		model.Skips = map[string]bool{}
		model.NodeReview = map[string]bool{}
		for slug, e := range model.Estimates {
			model.Skips[slug] = (e.Level == "familiar" || e.Level == "self_reported")
			model.NodeReview[slug] = e.Level == "review"
		}
		first := func(p PathPlan) string {
			if len(p.Steps) == 0 {
				return "none"
			}
			return p.Steps[0].Slug + ":" + p.Steps[0].Action
		}
		row := outcome{Case: c.ID, Allowed: c.Allowed, First: map[string]string{"directory": "a:read", "user_state": first(planShortPath(legacy)), "model": first(planShortPath(model))}, Accepted: map[string]bool{}}
		for name, action := range row.First {
			row.Accepted[name] = false
			for _, allowed := range c.Allowed {
				if action == allowed {
					row.Accepted[name] = true
				}
			}
		}
		if !row.Accepted["model"] {
			t.Errorf("%s: expected one of %v, got %s", c.ID, c.Allowed, row.First["model"])
		}
		report.Outcomes = append(report.Outcomes, row)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
	if path := os.Getenv("LEARNING_MODEL_REPORT"); path != "" {
		if err = os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
