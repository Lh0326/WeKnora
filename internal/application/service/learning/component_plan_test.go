package learning

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Independent small-graph oracle: enumerate whole subsets, not search paths.
func componentSubsetOracle(c []componentCandidate, budget, checkBudget int) int {
	best := 0
	for mask := uint64(0); mask < uint64(1)<<len(c); mask++ {
		if bits.OnesCount64(mask) > componentStepLimit {
			continue
		}
		cost, value, checks, valid := 0, 0, 0, true
		for i, a := range c {
			if mask&(1<<i) == 0 {
				continue
			}
			cost += a.step.Minutes
			value += a.step.Utility
			if a.step.Action == "check" {
				checks += a.step.Minutes
			}
			if a.blocked {
				valid = false
				break
			}
			for _, p := range a.requires {
				if mask&(1<<p) == 0 {
					valid = false
					break
				}
			}
		}
		if valid && cost <= budget && checks <= checkBudget && value > best {
			best = value
		}
	}
	return best
}

func syntheticComponentEntries(rng *rand.Rand, n int) []interfaces.ComponentEntry {
	out := make([]interfaces.ComponentEntry, n)
	for i := range out {
		key := fmt.Sprintf("goal-%04d", i)
		d := types.ComponentDefinition{Key: key, Title: key, Minutes: 1 + rng.Intn(7), Checks: []types.ComponentCheck{{ID: "check"}}}
		if i > 0 && rng.Float64() < .6 {
			d.Prerequisites = []string{out[rng.Intn(i)].Material.Key}
		}
		out[i] = interfaces.ComponentEntry{ID: key, Material: d, Available: true, Relevance: &interfaces.ComponentRelevance{Weight: rng.Float64()}}
		if rng.Float64() < .2 {
			out[i].State.Reads = 1
		}
	}
	return out
}

func assertComponentPlanFeasible(t *testing.T, c []componentCandidate, steps []interfaces.ComponentStep, used, budget, checkBudget int) {
	t.Helper()
	seen := map[string]bool{}
	cost, checks := 0, 0
	byID := map[string]componentCandidate{}
	for _, a := range c {
		byID[a.step.ID] = a
	}
	for _, step := range steps {
		a, ok := byID[step.ID]
		require.True(t, ok)
		require.False(t, seen[step.ID])
		require.False(t, a.blocked)
		for _, p := range a.requires {
			require.True(t, seen[c[p].step.ID], "dependency must precede the selected goal")
		}
		seen[step.ID] = true
		cost += step.Minutes
		if step.Action == "check" {
			checks += step.Minutes
		}
	}
	require.Equal(t, cost, used)
	require.LessOrEqual(t, cost, budget)
	require.LessOrEqual(t, checks, checkBudget)
	require.LessOrEqual(t, len(steps), componentStepLimit)
}

func TestComponentJointPlanAvoidsShortNodeTrap(t *testing.T) {
	entries := []interfaces.ComponentEntry{
		{ID: "root", Available: true, Material: types.ComponentDefinition{Key: "root", Title: "必要基础", Minutes: 3}},
		{ID: "target", Available: true, Material: types.ComponentDefinition{Key: "target", Title: "当前相关目标", Minutes: 3, Prerequisites: []string{"root"}}, Relevance: &interfaces.ComponentRelevance{Weight: 1}},
		{ID: "short", Available: true, Material: types.ComponentDefinition{Key: "short", Title: "独立短目标", Minutes: 2}},
	}
	c := componentCandidates(entries, "", time.Now())
	greedy := greedyComponentState(c, 6, 1)
	steps, used, info := searchComponentPlan(c, 6)
	require.Equal(t, 2000, greedy.value)
	require.Equal(t, 2500, info.Utility)
	require.Equal(t, []string{"root", "target"}, []string{steps[0].ID, steps[1].ID})
	require.Equal(t, 6, used)
	require.True(t, info.Exact)
	require.True(t, steps[1].Conditional)
}

func TestComponentDifficultReportCanProgressAfterReadingWithoutKnownDeclaration(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	now := time.Now()
	report := componentEvent(c, "difficulty", "difficult", now.Add(-time.Hour))
	read := componentEvent(c, "read", "read", now.Add(-time.Minute))
	state := projectComponent(c, []types.LearningEvent{report, read}, now)
	entry := interfaces.ComponentEntry{ID: c.ID, Available: true, Material: c.Definition, State: state}
	require.Equal(t, "review", state.Level, "remaining uncertainty is still visible")
	require.True(t, componentPrepared(entry), "review uncertainty must not prevent continuing after rereading")
	steps, _, _ := planComponentsDetailed([]interfaces.ComponentEntry{entry}, 5, "", now)
	require.Equal(t, "check", steps[0].Action, "offer a new action instead of an endless reread loop")
}

func TestComponentPlanCheckBudgetAndInvalidSources(t *testing.T) {
	rows, pages := componentFixture(t)
	pages[0].Status = types.WikiPageStatusArchived
	require.False(t, componentAvailable(rows[0], pages))
	_, err := ValidateComponentPack(1, testKB, []types.ComponentDefinition{rows[0].Definition}, pages)
	require.Error(t, err)
	entries := []interfaces.ComponentEntry{}
	for i := 0; i < 10; i++ {
		entries = append(entries, interfaces.ComponentEntry{ID: fmt.Sprintf("check-%d", i), Available: true, Material: types.ComponentDefinition{Key: fmt.Sprintf("check-%d", i), Minutes: 2, Checks: []types.ComponentCheck{{ID: "q1"}}}, State: interfaces.ComponentState{Reads: 1}})
	}
	steps, used, info := planComponentsDetailed(entries, 5, "", time.Now())
	require.Len(t, steps, 1)
	require.Equal(t, 1, used)
	require.Equal(t, 1, info.CheckBudget)
	entries[0].Material.Prerequisites = []string{"unknown"}
	steps, _, _ = planComponentsDetailed(entries, 5, entries[0].ID, time.Now())
	require.Empty(t, steps, "an unknown prerequisite must not be invented as ready")
}

func TestComponentGoalBudgetPartialIsDisclosed(t *testing.T) {
	entries := []interfaces.ComponentEntry{}
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("goal-%d", i)
		e := interfaces.ComponentEntry{ID: key, Available: true, Material: types.ComponentDefinition{Key: key, Minutes: 2}}
		if i > 0 {
			e.Material.Prerequisites = []string{entries[i-1].Material.Key}
		}
		entries = append(entries, e)
	}
	steps, used, info := planComponentsDetailed(entries, 5, entries[2].ID, time.Now())
	require.Len(t, steps, 2)
	require.Equal(t, 4, used)
	require.Contains(t, info.Notice, "尚未覆盖目标本身")
	entries[2].Available = false
	steps, _, info = planComponentsDetailed(entries, 15, entries[2].ID, time.Now())
	require.Empty(t, steps)
	require.Contains(t, info.Notice, "来源已失效")
}

func TestComponentPlannerOfflineEvaluation(t *testing.T) {
	rng := rand.New(rand.NewSource(20260912))
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	trials := []map[string]any{}
	improved, greedyRegret := 0, 0
	for trial := 0; trial < 100; trial++ {
		entries := syntheticComponentEntries(rng, 8+rng.Intn(3))
		budget := 4 + rng.Intn(17)
		c := componentCandidates(entries, "", now)
		steps, used, info := searchComponentPlan(c, budget)
		oracle := componentSubsetOracle(c, budget, info.CheckBudget)
		greedy := greedyComponentState(c, budget, info.CheckBudget)
		require.True(t, info.Exact)
		require.Equal(t, oracle, info.Utility)
		assertComponentPlanFeasible(t, c, steps, used, budget, info.CheckBudget)
		if info.Utility > greedy.value {
			improved++
		}
		greedyRegret += oracle - greedy.value
		trials = append(trials, map[string]any{"trial": trial, "nodes": len(entries), "budget": budget, "oracle": oracle, "search": info.Utility, "greedy": greedy.value})
	}
	scale := []map[string]any{}
	for _, n := range []int{20, 40, 100, 500} {
		entries := syntheticComponentEntries(rng, n)
		c := componentCandidates(entries, "", now)
		start := time.Now()
		steps, used, info := searchComponentPlan(c, 30)
		elapsed := time.Since(start).Seconds()
		assertComponentPlanFeasible(t, c, steps, used, 30, info.CheckBudget)
		greedy := greedyComponentState(c, 30, info.CheckBudget)
		require.GreaterOrEqual(t, info.Utility, greedy.value)
		require.GreaterOrEqual(t, info.UpperBound, info.Utility)
		require.LessOrEqual(t, info.Expanded, componentExpansionLimit)
		require.False(t, math.IsNaN(elapsed))
		scale = append(scale, map[string]any{"nodes": n, "seconds": elapsed, "plan": info, "greedy": greedy.value, "steps": len(steps)})
	}
	hashes := map[string]string{}
	for _, file := range []string{"component_plan.go", "component_plan_test.go", filepath.Join("..", "..", "..", "..", "docs", "research", "learning-framework", "关联与路径实施协议.md")} {
		content, err := os.ReadFile(file)
		require.NoError(t, err)
		hash := sha256.Sum256(content)
		hashes[filepath.Base(file)] = fmt.Sprintf("%x", hash)
	}
	report := map[string]any{"protocol": "关联与路径实施协议.md", "sha256": hashes, "seed": 20260912, "small_trials": trials, "strict_improvements": improved, "greedy_total_regret": greedyRegret, "search_total_regret": 0, "scale": scale, "scope": "algorithmic synthetic evaluation under declared utility; not measured learning gains"}
	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	if path := os.Getenv("WEKNORA_PLAN_AUDIT_PATH"); path != "" {
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	t.Logf("100 oracle comparisons: search regret 0; %d strict improvements, greedy total regret %d", improved, greedyRegret)
	for _, s := range scale {
		t.Logf("scale: %+v", s)
	}
}
