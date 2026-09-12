package learning

import (
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Topic is a scope/continuity hint only. Only authored Prerequisites can
// expand a target into a learning progression, including across topics.
func componentFocusDepth(key string, byKey map[string]interfaces.ComponentEntry, visiting map[string]bool, memo map[string]int) int {
	if depth, ok := memo[key]; ok {
		return depth
	}
	if visiting[key] {
		return 0
	}
	visiting[key] = true
	defer delete(visiting, key)
	depth := 0
	for _, parent := range byKey[key].Material.Prerequisites {
		if _, exists := byKey[parent]; exists {
			depth = max(depth, 1+componentFocusDepth(parent, byKey, visiting, memo))
		}
	}
	memo[key] = depth
	return depth
}

// A default focus must not repeatedly select a broken source chain while
// another goal can proceed. Preparation stops reading expansion, not source
// validation of that prerequisite itself. Existing target revisits need only
// their direct references; they never require relearning the whole ancestry.
func componentFocusSourcesReady(e interfaces.ComponentEntry, revisit bool, byKey map[string]interfaces.ComponentEntry, visiting map[string]bool, memo map[string]bool) bool {
	if !revisit {
		if ready, seen := memo[e.Material.Key]; seen {
			return ready
		}
	}
	if visiting[e.Material.Key] || !e.Available {
		return false
	}
	visiting[e.Material.Key] = true
	defer delete(visiting, e.Material.Key)
	ready := true
	for _, key := range e.Material.Prerequisites {
		p, exists := byKey[key]
		if !exists || !p.Available || visiting[key] {
			ready = false
			break
		}
		if !revisit && !componentPrepared(p) && !componentFocusSourcesReady(p, false, byKey, visiting, memo) {
			ready = false
			break
		}
	}
	if !revisit {
		memo[e.Material.Key] = ready
	}
	return ready
}

// Zero-step feasibility needs no plan search: every nonempty feasible plan
// starts with an unblocked, dependency-ready action that fits its limits.
// Test only this frontier, not the total cost of the prerequisite curriculum;
// a long curriculum can still make useful partial progress this round.
func componentFocusCanStart(entries []interfaces.ComponentEntry, goal string, budget int, now time.Time) bool {
	checkBudget := max(1, min(3, budget/4))
	for _, candidate := range componentCandidates(entries, goal, now) {
		if candidate.blocked || len(candidate.requires) > 0 || candidate.step.Minutes > budget {
			continue
		}
		if candidate.step.Action != "check" || candidate.step.Minutes <= checkBudget {
			return true
		}
	}
	return false
}

func selectComponentFocus(entries []interfaces.ComponentEntry, goal, topic string, budget int, now time.Time) (interfaces.ComponentEntry, string, bool) {
	byKey := map[string]interfaces.ComponentEntry{}
	for _, e := range entries {
		byKey[e.Material.Key] = e
		if goal != "" && e.ID == goal && (topic == "" || e.Material.Topic == topic) {
			return e, "围绕你选定的目标及其必要前置安排。", true
		}
	}
	if goal != "" {
		return interfaces.ComponentEntry{}, "目标不在当前学习范围。", false
	}
	type choice struct {
		entry        interfaces.ComponentEntry
		step         interfaces.ComponentStep
		depth        int
		priority     int
		sourcesReady bool
	}
	choices := []choice{}
	depths := map[string]int{}
	sourceReadiness := map[string]bool{}
	actionableTopics := map[string]bool{}
	for _, e := range entries {
		if topic != "" && e.Material.Topic != topic {
			continue
		}
		step, ok := componentCandidateStep(e, "", now)
		if !ok {
			continue
		}
		priority := 0
		if step.Action == "recall" && e.State.DueAt != nil && !e.State.DueAt.After(now) {
			priority = 2
		} else if step.Action != "check" && (e.State.Level == "review" || e.State.SelfReport == "difficult") {
			priority = 1
		}
		choices = append(choices, choice{entry: e, step: step, depth: componentFocusDepth(e.Material.Key, byKey, map[string]bool{}, depths), priority: priority,
			sourcesReady: componentFocusSourcesReady(e, step.Action != "read", byKey, map[string]bool{}, sourceReadiness)})
		actionableTopics[e.Material.Topic] = true
	}
	if len(choices) == 0 {
		return interfaces.ComponentEntry{}, "当前范围没有待执行的新动作。", false
	}
	latestTopic := ""
	var latest time.Time
	for _, e := range entries {
		at := e.State.LastStudyAt
		if actionableTopics[e.Material.Topic] && !at.After(now) && (at.After(latest) || (!at.IsZero() && at.Equal(latest) && e.Material.Topic < latestTopic)) {
			latest, latestTopic = at, e.Material.Topic
		}
	}
	sort.SliceStable(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		if a.sourcesReady != b.sourcesReady {
			return a.sourcesReady
		}
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		// Optional checks must not trap the learner in an already-read module
		// when a new reading goal (including a cross-module successor) exists.
		if (a.step.Action == "read") != (b.step.Action == "read") {
			return a.step.Action == "read"
		}
		nearA, nearB := !latest.IsZero() && a.entry.Material.Topic == latestTopic, !latest.IsZero() && b.entry.Material.Topic == latestTopic
		if nearA != nearB {
			return nearA
		}
		if a.depth != b.depth {
			return a.depth > b.depth
		}
		if a.step.Utility != b.step.Utility {
			return a.step.Utility > b.step.Utility
		}
		if a.entry.Material.Key != b.entry.Material.Key {
			return a.entry.Material.Key < b.entry.Material.Key
		}
		return a.entry.ID < b.entry.ID
	})
	best := choices[0]
	fallback := false
	for i, candidate := range choices {
		if componentFocusCanStart(entries, candidate.entry.ID, budget, now) {
			best, fallback = candidate, i > 0
			break
		}
	}
	reason := "按现有目标和已声明先修选择本轮推进目标。"
	if best.priority == 2 {
		reason = "该目标已到复习时间，先处理到期回忆。"
	} else if best.priority == 1 {
		reason = "该目标仍需处理困难，先回看或回忆，再决定后续学习。"
	} else if !latest.IsZero() && best.entry.Material.Topic == latestTopic {
		reason = "继续最近仍有待学目标的模块，避免跨无关模块来回跳转。"
	} else if topic != "" {
		reason = "在所选模块内选择目标，只将其必要先修扩展到其他模块。"
	}
	if best.depth > 0 && best.step.Action == "read" {
		reason += " 以有真实先修关系的下游目标为终点，先完成必要准备。"
	}
	if fallback {
		reason += " 优先目标暂时无法起步，先推进当前范围内可执行的目标。"
	}
	return best.entry, reason, true
}

func componentProgressionOrder(steps []interfaces.ComponentStep, entries []interfaces.ComponentEntry, topic string) []interfaces.ComponentStep {
	byID := map[string]interfaces.ComponentEntry{}
	selected := map[string]bool{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	for _, step := range steps {
		selected[step.ID] = true
	}
	out := make([]interfaces.ComponentStep, 0, len(steps))
	done := map[string]bool{}
	lastTopic := topic
	for len(out) < len(steps) {
		chosen := -1
		for i, step := range steps {
			if done[step.ID] {
				continue
			}
			ready := true
			for _, id := range step.PrerequisiteIDs {
				if selected[id] && !done[id] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if chosen < 0 || (byID[step.ID].Material.Topic == lastTopic && byID[steps[chosen].ID].Material.Topic != lastTopic) {
				chosen = i
			}
		}
		if chosen < 0 {
			return steps
		} // Preserve the solver's feasible path if an invariant is broken.
		step := steps[chosen]
		out = append(out, step)
		done[step.ID] = true
		lastTopic = byID[step.ID].Material.Topic
	}
	return out
}

func planComponentsWithTopic(entries []interfaces.ComponentEntry, budget int, goal, topic string, now time.Time) ([]interfaces.ComponentStep, int, interfaces.ComponentPlanInfo) {
	focus, reason, found := selectComponentFocus(entries, goal, topic, budget, now)
	info := interfaces.ComponentPlanInfo{PolicyVersion: componentPolicyVersion, Exact: true, CheckBudget: max(1, min(3, budget/4)), TopicScope: topic, Stage: "idle", FocusReason: reason}
	info.Limitations = []string{"阅读和自评只用于继续学习的准备；不会把前置已读转成能力已通过。"}
	if !found {
		info.Notice = "本范围暂无新的阅读或到期复习动作；可以自行回看来源。没有新动作不代表全部知识已掌握。"
		info.Progression = "现有动作已处理或仍在复习间隔内，等待新的学习需求或到期记录。"
		inScope, available, noChecks := 0, 0, 0
		for _, e := range entries {
			if topic != "" && e.Material.Topic != topic {
				continue
			}
			inScope++
			if e.Available {
				available++
			}
			if len(e.Material.Checks) == 0 {
				noChecks++
			}
		}
		if inScope > 0 && noChecks == inScope {
			info.Limitations = append(info.Limitations, "当前范围没有配置可执行检查；阅读完成不能证明会应用，不会自动制造测验。")
		}
		if inScope > 0 && available == 0 {
			info.Stage = "blocked"
			info.Notice = "当前范围的目标来源均待核对，核对前不安排学习动作。"
		}
		if goal != "" {
			info.Stage = "blocked"
			info.Notice = reason
		}
		return []interfaces.ComponentStep{}, 0, info
	}
	info.FocusGoalID, info.FocusGoalTitle = focus.ID, focus.Material.Title
	if len(focus.Material.Prerequisites) == 0 {
		info.Limitations = append(info.Limitations, "该目标没有已声明先修；模块分类和相关线不足以证明学习层级。")
	}
	if len(focus.Material.Checks) == 0 {
		info.Limitations = append(info.Limitations, "该目标未配置可执行检查；通读现有解释与例子不能证明会应用，不会虚构后续练习。")
	}
	if !focus.Available {
		info.Stage, info.Notice = "blocked", "目标材料来源已失效，核对材料后再安排该目标。"
		return []interfaces.ComponentStep{}, 0, info
	}
	if _, available := componentCandidateStep(focus, focus.ID, now); !available {
		info.Notice = "该目标暂无新的阅读或到期复习动作；自评熟悉会减少重复推荐，不补记独立检查证据。"
		info.Progression = "保留目前的阅读、自评和检查状态；可自行回看来源或等待到期复习。"
		return []interfaces.ComponentStep{}, 0, info
	}
	steps, used, search := planComponentGoal(entries, budget, focus.ID, now)
	info.Exact, info.Expanded, info.Candidates = search.Exact, search.Expanded, search.Candidates
	info.Utility, info.UpperBound, info.Notice = search.Utility, search.UpperBound, search.Notice
	steps = componentProgressionOrder(steps, entries, focus.Material.Topic)
	if len(steps) == 0 {
		info.Stage = "blocked"
		info.Progression = "当前目标的先修、来源或内部计算上限尚不允许安排可执行步骤；不会假定缺失的前置已经准备好。"
		return steps, used, info
	}
	info.Stage = steps[0].Action
	switch info.Stage {
	case "check":
		info.Progression = "已有阅读准备，本轮提供自愿情境检查；结果只归属本目标，不替代其先修能力证据。"
	case "recall":
		info.Progression = "先尝试回忆，再对照材料；实际反馈决定下一次复习，不必重做整条基础链。"
	default:
		if steps[0].ID != focus.ID {
			info.Progression = "先完成「" + steps[0].Title + "」的阅读准备，再推进「" + focus.Material.Title + "」；后续步骤按实际阅读与反馈重新安排。"
		} else {
			info.Progression = "先理解当前目标的适用条件、解释和完整例子；通读只更新接触状态，检查自愿参加。"
		}
	}
	return steps, used, info
}
