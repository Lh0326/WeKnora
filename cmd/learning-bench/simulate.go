package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
)

// ---- persona script ----

type ScriptStep struct {
	Day    int    `json:"day"`
	Action string `json:"action"` // cite | re_ask | quiz_correct | quiz_wrong | idle
	Slug   string `json:"slug"`
}

type PersonaScript struct {
	Steps []ScriptStep `json:"steps"`
}

// ---- assertion accumulator ----

type assertCollector struct {
	failures []string
	passes   int
}

func (a *assertCollector) check(name string, cond bool, detail string) {
	if cond {
		a.passes++
		return
	}
	a.failures = append(a.failures, fmt.Sprintf("  ✗ %s: %s", name, detail))
}

func (a *assertCollector) err() error {
	if len(a.failures) == 0 {
		return nil
	}
	msg := fmt.Sprintf("%d assertion(s) failed:\n", len(a.failures))
	for _, f := range a.failures {
		msg += f + "\n"
	}
	return fmt.Errorf("%s", msg)
}

// ---- simulate ----

func runSimulate(scriptPath string, verbose bool) error {
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		return err
	}
	var script PersonaScript
	if err := json.Unmarshal(raw, &script); err != nil {
		return fmt.Errorf("script parse: %w", err)
	}
	if len(script.Steps) == 0 {
		return fmt.Errorf("script has no steps")
	}

	asserts := &assertCollector{}

	// 1. Fold the full sequence and track per-slug p_eff at each checkpoint.
	states := map[string]learning.FoldState{}
	weights := map[string]float64{
		"cite":         learning.WeightAnswerCite,
		"re_ask":       learning.WeightReAsk,
		"quiz_correct": learning.WeightQuizCorrect,
		"quiz_wrong":   learning.WeightQuizWrong,
		"idle":         0,
	}
	eventTypes := map[string]string{
		"cite":         types.LearningEventAnswerCite,
		"re_ask":       types.LearningEventReAsk,
		"quiz_correct": types.LearningEventQuizCorrect,
		"quiz_wrong":   types.LearningEventQuizWrong,
		"idle":         "",
	}

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i, step := range script.Steps {
		now := baseTime.Add(time.Duration(step.Day) * 24 * time.Hour)
		if step.Action == "idle" {
			if verbose {
				fmt.Printf("  step %d: day=%d idle (skip)\n", i, step.Day)
			}
			continue
		}
		ev := learning.Event{
			Type:       eventTypes[step.Action],
			Weight:     weights[step.Action],
			OccurredAt: now,
		}
		state := states[step.Slug]
		newState := learning.FoldEvent(state, ev)
		states[step.Slug] = newState

		if verbose {
			fmt.Printf("  step %d: day=%d %s %s → logit=%.3f (was %.3f)\n",
				i, step.Day, step.Action, step.Slug, newState.Logit, state.Logit)
		}
	}

	// 2. Assert curve shapes per slug.
	simulateAssertShapes(asserts, script, states, baseTime, eventTypes, weights)

	// 3. Assert hysteresis absorbs boundary jitter.
	simulateAssertHysteresis(asserts, baseTime)

	// 4. Assert reconcile migration preserves p_eff.
	simulateAssertReconcile(asserts, baseTime)

	fmt.Printf("  assertions: %d passed, %d failed\n", asserts.passes, len(asserts.failures))
	return asserts.err()
}

// simulateAssertShapes checks the direction of each action's effect.
func simulateAssertShapes(a *assertCollector, script PersonaScript, states map[string]learning.FoldState, baseTime time.Time, eventTypes map[string]string, weights map[string]float64) {
	// For each slug, replay step-by-step and check direction at each action.
	slugStates := map[string]learning.FoldState{}
	for _, step := range script.Steps {
		if step.Action == "idle" {
			continue
		}
		now := baseTime.Add(time.Duration(step.Day) * 24 * time.Hour)
		prev := slugStates[step.Slug]
		ev := learning.Event{Type: eventTypes[step.Action], Weight: weights[step.Action], OccurredAt: now}
		next := learning.FoldEvent(prev, ev)

		switch step.Action {
		case "cite", "quiz_correct", "re_ask":
			// cite and quiz_correct are positive; re_ask is negative.
			if step.Action == "re_ask" {
				a.check("re_ask_not_increase", next.Logit <= prev.Logit,
					fmt.Sprintf("slug %s: logit %.3f → %.3f (should decrease)", step.Slug, prev.Logit, next.Logit))
			} else {
				a.check("positive_not_decrease", next.Logit >= prev.Logit,
					fmt.Sprintf("slug %s: logit %.3f → %.3f (should increase)", step.Slug, prev.Logit, next.Logit))
			}
		case "quiz_wrong":
			a.check("quiz_wrong_not_increase", next.Logit <= prev.Logit,
				fmt.Sprintf("slug %s: logit %.3f → %.3f (should decrease)", step.Slug, prev.Logit, next.Logit))
		}

		slugStates[step.Slug] = next
	}

	// Assert quiz_correct is stronger than cite.
	if s, ok := slugStates["quiz-strong"]; ok {
		if c, ok2 := slugStates["cite-strong"]; ok2 {
			a.check("quiz_stronger_than_cite", s.Logit > c.Logit,
				fmt.Sprintf("quiz logit %.3f vs cite logit %.3f", s.Logit, c.Logit))
		}
	}

	// Assert decay: a slug with "idle" steps after activity should have lower p_eff.
	endTime := baseTime.Add(time.Duration(lastDay(script)+1) * 24 * time.Hour)
	for slug, state := range states {
		if state.EvidenceCount > 0 {
			pNow := learning.EffectiveP(state, state.LastEvidenceAt)
			pLater := learning.EffectiveP(state, endTime)
			if endTime.Sub(state.LastEvidenceAt) > 7*24*time.Hour {
				a.check("idle_decays", pLater < pNow,
					fmt.Sprintf("slug %s: p_eff now %.4f, later %.4f (should decay)", slug, pNow, pLater))
			}
		}
	}
}

func lastDay(script PersonaScript) int {
	max := 0
	for _, s := range script.Steps {
		if s.Day > max {
			max = s.Day
		}
	}
	return max
}

// simulateAssertHysteresis: p_eff oscillating inside the touched band
// [0.22, 0.30) must not change the level.
func simulateAssertHysteresis(a *assertCollector, baseTime time.Time) {
	for _, prev := range []learning.Level{learning.LevelUnseen, learning.LevelTouched} {
		for _, p := range []float64{0.22, 0.26, 0.299} {
			lr := learning.LevelOf(p, 5, prev)
			a.check("hysteresis_absorbs", lr.Level == prev,
				fmt.Sprintf("prev=%s p_eff=%.3f → %s (should stay %s)", prev, p, lr.Level, prev))
		}
	}
	// Crossing the band edges moves the level.
	if lr := learning.LevelOf(0.2199, 5, learning.LevelTouched); lr.Level != learning.LevelUnseen {
		a.check("hysteresis_below_demotes", false, fmt.Sprintf("got %s", lr.Level))
	} else {
		a.check("hysteresis_below_demotes", true, "")
	}
}

// simulateAssertReconcile: a pure slug move preserves p_eff.
func simulateAssertReconcile(a *assertCollector, baseTime time.Time) {
	now := baseTime
	events := []learning.Event{
		{Type: types.LearningEventAnswerCite, Weight: learning.WeightAnswerCite, OccurredAt: now},
		{Type: types.LearningEventQuizCorrect, Weight: learning.WeightQuizCorrect, OccurredAt: now.Add(time.Hour)},
	}
	state := learning.FoldAll(learning.FoldState{}, events)

	migrations := learning.ReconcileSlug(
		map[string]learning.FoldState{"concept/old-name": state},
		map[string][]learning.Event{"concept/old-name": events},
		map[string]string{"concept/old-name": "concept/new-name"},
	)
	if len(migrations) != 1 {
		a.check("reconcile_emits_migration", false, fmt.Sprintf("migrations=%d", len(migrations)))
		return
	}
	m := migrations[0]
	if m.ToSlug != "concept/new-name" {
		a.check("reconcile_target", false, fmt.Sprintf("to=%s", m.ToSlug))
		return
	}
	a.check("reconcile_preserves_logit", m.State.Logit == state.Logit,
		fmt.Sprintf("logit %.4f vs %.4f", m.State.Logit, state.Logit))
	a.check("reconcile_preserves_evidence", m.State.EvidenceCount == state.EvidenceCount,
		fmt.Sprintf("evidence %d vs %d", m.State.EvidenceCount, state.EvidenceCount))

	// p_eff invariance.
	later := now.Add(72 * time.Hour)
	pBefore := learning.EffectiveP(state, later)
	pAfter := learning.EffectiveP(m.State, later)
	a.check("reconcile_preserves_p_eff", math.Abs(pBefore-pAfter) < 1e-12,
		fmt.Sprintf("p_eff %.8f vs %.8f", pBefore, pAfter))
}
