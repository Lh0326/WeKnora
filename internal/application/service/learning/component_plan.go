package learning

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const componentPolicyVersion = "kc-dependency-budget-v1"
const componentExactLimit = 14
const componentBeamWidth = 128
const componentExpansionLimit = 250000
const componentStepLimit = 12

type componentCandidate struct {
	step     interfaces.ComponentStep
	requires []int
	blocked  bool
}

// Preparation means it is reasonable to continue reading, not that a skill
// has been certified. Overdue recall is a priority rather than a hard blockade.
func componentPrepared(e interfaces.ComponentEntry) bool {
	if !e.Available {
		return false
	}
	if e.State.Level == "self_reported" || e.State.Level == "familiar" {
		return true
	}
	if e.State.Reads == 0 && e.State.Passes == 0 {
		return false
	}
	if !e.State.LastDifficultyAt.IsZero() {
		return e.State.LastReadAt.After(e.State.LastDifficultyAt)
	}
	return e.State.SelfReport != "difficult"
}

func componentCandidateStep(e interfaces.ComponentEntry, goal string, now time.Time) (interfaces.ComponentStep, bool) {
	step := interfaces.ComponentStep{ID: e.ID, Title: e.Material.Title, Action: "read", Minutes: e.Material.Minutes, Utility: 1000, Reason: "先学习完整解释与例子，自动记录进度后可继续。"}
	if !e.Available || step.Minutes < 1 || step.Minutes > 10 {
		return step, false
	}
	s := e.State
	freshDifficulty := !s.LastDifficultyAt.IsZero() && !s.LastReadAt.After(s.LastDifficultyAt)
	if s.SelfReport == "difficult" && s.LastDifficultyAt.IsZero() {
		freshDifficulty = true
	}
	if s.DueAt != nil && !s.DueAt.After(now) {
		step.Action, step.Minutes, step.Utility = "recall", 1, 1250
		step.Reason = "真实回忆记录已到复习时间，先尝试回忆，再决定后续安排。"
	} else if s.Level == "familiar" || s.Level == "self_reported" {
		return step, false
	} else if freshDifficulty || (s.Level == "review" && s.Reads == 0) {
		step.Utility = 1200
		step.Reason = "最近反馈或检查显示困难，先回看完整例子；阅读后可继续，不必再声明已会。"
	} else if s.Reads > 0 || s.Passes > 0 {
		if len(s.ReadCheckIDs) >= len(e.Material.Checks) {
			return step, false
		}
		step.Action, step.Minutes, step.Utility = "check", 1, 350
		step.Reason = "可选一个新情境检查理解；本轮限制检查用时，留出时间继续学习。"
	}
	if e.Relevance != nil && !math.IsNaN(e.Relevance.Weight) && !math.IsInf(e.Relevance.Weight, 0) && e.Relevance.Weight > 0 {
		step.Utility += int(math.Round(500 * math.Min(1, e.Relevance.Weight)))
		if len(e.Relevance.Links) > 0 {
			step.Reason += " " + e.Relevance.Links[0].Reason
		}
	}
	if e.ID == goal {
		step.Utility += 2000
	}
	return step, true
}

func componentCandidates(entries []interfaces.ComponentEntry, goal string, now time.Time) []componentCandidate {
	byKey := map[string]interfaces.ComponentEntry{}
	for _, e := range entries {
		byKey[e.Material.Key] = e
	}
	allowed := map[string]bool{}
	var include func(string)
	include = func(key string) {
		if allowed[key] {
			return
		}
		e, ok := byKey[key]
		if !ok {
			return
		}
		allowed[key] = true
		for _, p := range e.Material.Prerequisites {
			include(p)
		}
	}
	if goal != "" {
		for _, e := range entries {
			if e.ID == goal {
				include(e.Material.Key)
			}
		}
	} else {
		for key := range byKey {
			allowed[key] = true
		}
	}
	ordered := append([]interfaces.ComponentEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Material.Key < ordered[j].Material.Key })
	out := []componentCandidate{}
	indices := map[string]int{}
	for _, e := range ordered {
		if !allowed[e.Material.Key] {
			continue
		}
		step, ok := componentCandidateStep(e, goal, now)
		if !ok {
			continue
		}
		indices[e.Material.Key] = len(out)
		out = append(out, componentCandidate{step: step})
	}
	for key, index := range indices {
		seen := map[int]bool{}
		for _, prerequisite := range byKey[key].Material.Prerequisites {
			p, exists := byKey[prerequisite]
			if exists && componentPrepared(p) {
				continue
			}
			pi, canPlan := indices[prerequisite]
			if !exists || !p.Available || !canPlan || pi == index {
				out[index].blocked = true
				continue
			}
			if !seen[pi] {
				out[index].requires = append(out[index].requires, pi)
				out[index].step.PrerequisiteIDs = append(out[index].step.PrerequisiteIDs, p.ID)
				seen[pi] = true
			}
		}
		sort.Ints(out[index].requires)
		sort.Strings(out[index].step.PrerequisiteIDs)
		out[index].step.Conditional = len(out[index].requires) > 0
		if out[index].step.Conditional {
			out[index].step.Reason += " 本轮先完成前置步骤；实际结果返回后会重新安排。"
		}
	}
	return out
}

type componentSearchState struct {
	mask                string
	path                []int
	cost, checks, value int
	bound               float64
}

func componentMaskHas(mask string, i int) bool { return mask[i/8]&(1<<uint(i%8)) != 0 }
func componentMaskAdd(mask string, i int) string {
	b := []byte(mask)
	b[i/8] |= 1 << uint(i%8)
	return string(b)
}

// A fractional-knapsack relaxation ignores dependencies and diagnostic limits,
// so it is an upper bound, not an estimate of achievable learning benefit.
func componentBound(s componentSearchState, c []componentCandidate, ratioOrder []int, budget int) float64 {
	remaining, bound := budget-s.cost, float64(s.value)
	for _, i := range ratioOrder {
		if remaining == 0 {
			break
		}
		if c[i].blocked || componentMaskHas(s.mask, i) {
			continue
		}
		cost, value := c[i].step.Minutes, c[i].step.Utility
		if cost <= remaining {
			bound += float64(value)
			remaining -= cost
		} else {
			bound += float64(value) * float64(remaining) / float64(cost)
			break
		}
	}
	return bound
}

func componentBetter(a, b componentSearchState) bool {
	if a.value != b.value {
		return a.value > b.value
	}
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	return a.mask < b.mask
}

// Keep a cheap feasible incumbent, so truncating the larger search cannot
// produce a worse objective than the same-snapshot greedy baseline.
func greedyComponentState(c []componentCandidate, budget, checkBudget int) componentSearchState {
	s := componentSearchState{mask: strings.Repeat("\x00", (len(c)+7)/8)}
	for len(s.path) < componentStepLimit {
		best := -1
		for i, a := range c {
			if a.blocked || componentMaskHas(s.mask, i) || s.cost+a.step.Minutes > budget {
				continue
			}
			if a.step.Action == "check" && s.checks+a.step.Minutes > checkBudget {
				continue
			}
			ready := true
			for _, p := range a.requires {
				if !componentMaskHas(s.mask, p) {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if best < 0 || a.step.Utility*c[best].step.Minutes > c[best].step.Utility*a.step.Minutes {
				best = i
			}
		}
		if best < 0 {
			break
		}
		a := c[best].step
		s.mask = componentMaskAdd(s.mask, best)
		s.path = append(s.path, best)
		s.cost += a.Minutes
		s.value += a.Utility
		if a.Action == "check" {
			s.checks += a.Minutes
		}
	}
	return s
}

func searchComponentPlan(c []componentCandidate, budget int) ([]interfaces.ComponentStep, int, interfaces.ComponentPlanInfo) {
	info := interfaces.ComponentPlanInfo{PolicyVersion: componentPolicyVersion, Exact: true, Candidates: len(c), CheckBudget: max(1, min(3, budget/4))}
	initial := componentSearchState{mask: strings.Repeat("\x00", (len(c)+7)/8)}
	ratioOrder := make([]int, len(c))
	for i := range c {
		ratioOrder[i] = i
	}
	sort.Slice(ratioOrder, func(i, j int) bool {
		a, b := c[ratioOrder[i]].step, c[ratioOrder[j]].step
		left, right := a.Utility*b.Minutes, b.Utility*a.Minutes
		if left != right {
			return left > right
		}
		return ratioOrder[i] < ratioOrder[j]
	})
	initial.bound = componentBound(initial, c, ratioOrder, budget)
	best, frontier := greedyComponentState(c, budget, info.CheckBudget), []componentSearchState{initial}
	limitHit := false
	for depth := 0; depth < componentStepLimit && len(frontier) > 0; depth++ {
		next := map[string]componentSearchState{}
		for _, state := range frontier {
			for i, candidate := range c {
				if candidate.blocked || componentMaskHas(state.mask, i) || state.cost+candidate.step.Minutes > budget {
					continue
				}
				checks := state.checks
				if candidate.step.Action == "check" {
					checks += candidate.step.Minutes
				}
				if checks > info.CheckBudget {
					continue
				}
				ready := true
				for _, p := range candidate.requires {
					if !componentMaskHas(state.mask, p) {
						ready = false
						break
					}
				}
				if !ready {
					continue
				}
				if info.Expanded >= componentExpansionLimit {
					info.Exact = false
					limitHit = true
					break
				}
				info.Expanded++
				mask := componentMaskAdd(state.mask, i)
				if _, exists := next[mask]; exists {
					continue
				}
				n := componentSearchState{mask: mask, cost: state.cost + candidate.step.Minutes, checks: checks, value: state.value + candidate.step.Utility, path: append(append([]int(nil), state.path...), i)}
				if componentBetter(n, best) {
					best = n
				}
				n.bound = componentBound(n, c, ratioOrder, budget)
				next[mask] = n
			}
			if limitHit {
				break
			}
		}
		if limitHit {
			break
		}
		frontier = make([]componentSearchState, 0, len(next))
		for _, state := range next {
			frontier = append(frontier, state)
		}
		sort.Slice(frontier, func(i, j int) bool {
			if frontier[i].bound != frontier[j].bound {
				return frontier[i].bound > frontier[j].bound
			}
			return componentBetter(frontier[i], frontier[j])
		})
		if len(c) > componentExactLimit && len(frontier) > componentBeamWidth {
			frontier = frontier[:componentBeamWidth]
			info.Exact = false
		}
	}
	steps := make([]interfaces.ComponentStep, 0, len(best.path))
	for _, index := range best.path {
		steps = append(steps, c[index].step)
	}
	info.Utility = best.value
	info.UpperBound = best.value
	if !info.Exact {
		info.UpperBound = int(math.Ceil(initial.bound))
		info.Notice = "本轮采用有界搜索；顺序满足当前约束，但不保证全局最优。"
	}
	if len(steps) == 0 {
		info.Notice = "当前预算与准备条件下暂无可执行步骤；可以调整目标或时间，已阅读目标仍可自行检查。"
	}
	return steps, best.cost, info
}

func planComponentsDetailed(entries []interfaces.ComponentEntry, budget int, goal string, now time.Time) ([]interfaces.ComponentStep, int, interfaces.ComponentPlanInfo) {
	if goal != "" {
		for _, e := range entries {
			if e.ID == goal && !e.Available {
				return []interfaces.ComponentStep{}, 0, interfaces.ComponentPlanInfo{PolicyVersion: componentPolicyVersion, Exact: true, CheckBudget: max(1, min(3, budget/4)), Notice: "目标材料来源已失效，核对材料后再安排该目标。"}
			}
		}
	}
	steps, used, info := searchComponentPlan(componentCandidates(entries, goal, now), budget)
	if goal != "" && len(steps) > 0 {
		included, prepared := false, false
		for _, step := range steps {
			if step.ID == goal {
				included = true
			}
		}
		for _, e := range entries {
			if e.ID == goal {
				prepared = componentPrepared(e)
			}
		}
		if !included && !prepared {
			info.Notice = "本轮只安排能完成的前置步骤，尚未覆盖目标本身；完成后可继续下一轮或增加时间。"
		}
	}
	return steps, used, info
}

func planComponents(entries []interfaces.ComponentEntry, budget int, goal string, now time.Time) ([]interfaces.ComponentStep, int) {
	steps, used, _ := planComponentsDetailed(entries, budget, goal, now)
	return steps, used
}
