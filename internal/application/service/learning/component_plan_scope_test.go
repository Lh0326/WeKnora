package learning

import (
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func progressionEntry(id, topic string, prerequisites ...string) interfaces.ComponentEntry {
	return interfaces.ComponentEntry{ID: id, Available: true, Material: types.ComponentDefinition{Key: id, Title: id, Topic: topic, Minutes: 3, Prerequisites: prerequisites}, State: interfaces.ComponentState{Level: "unseen"}}
}

func progressionIDs(steps []interfaces.ComponentStep) []string {
	ids := []string{}
	for _, step := range steps {
		ids = append(ids, step.ID)
	}
	return ids
}

func TestComponentDefaultFocusBuildsDeclaredProgressionInsteadOfModuleMixture(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "工具", "foundation"), progressionEntry("unrelated", "其他")}
	entries[2].Material.Minutes = 1
	entries[2].Relevance = &interfaces.ComponentRelevance{Weight: 1}
	steps, used, info := planComponentsDetailed(entries, 15, "", time.Now())
	require.Equal(t, "target", info.FocusGoalID)
	require.Equal(t, []string{"foundation", "target"}, progressionIDs(steps))
	require.Equal(t, 6, used)
	require.True(t, steps[1].Conditional)
	require.Contains(t, info.Progression, "foundation")
	require.Contains(t, info.Progression, "target")
	assertComponentPlanFeasible(t, componentCandidates(entries, "target", time.Now()), steps, used, 15, info.CheckBudget)
}

func TestComponentModuleScopeAllowsOnlyNeededCrossModulePrerequisites(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "工具", "foundation"), progressionEntry("unrelated", "其他")}
	steps, _, info := planComponentsWithTopic(entries, 15, "", "工具", time.Now())
	require.Equal(t, "工具", info.TopicScope)
	require.Equal(t, []string{"foundation", "target"}, progressionIDs(steps))
	steps, _, info = planComponentsWithTopic(entries, 15, "", "其他", time.Now())
	require.Equal(t, []string{"unrelated"}, progressionIDs(steps))
	require.Equal(t, "unrelated", info.FocusGoalID)
}

func TestComponentDefaultContinuesRecentlyStudiedModuleWhenNewReadingExists(t *testing.T) {
	now := time.Now()
	entries := []interfaces.ComponentEntry{progressionEntry("done", "当前"), progressionEntry("next", "当前"), progressionEntry("else-base", "别处"), progressionEntry("else-target", "别处", "else-base")}
	entries[0].State = interfaces.ComponentState{Level: "learning", Reads: 1, LastStudyAt: now.Add(-time.Minute)}
	steps, _, info := planComponentsDetailed(entries, 15, "", now)
	require.Equal(t, []string{"next"}, progressionIDs(steps))
	require.Contains(t, info.FocusReason, "最近")
	// Exhausting this module's actual read actions permits a new module; no
	// fabricated quiz or reread is required to keep the journey moving.
	entries[1].State = interfaces.ComponentState{Level: "learning", Reads: 1, LastStudyAt: now}
	_, _, info = planComponentsDetailed(entries, 15, "", now)
	require.Equal(t, "else-target", info.FocusGoalID)
}

func TestComponentOptionalPrerequisiteCheckNeverHidesUnreadSuccessor(t *testing.T) {
	for _, difficult := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_read", true: "reread_after_difficulty"}[difficult], func(t *testing.T) {
			now := time.Now()
			entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation")}
			entries[0].Material.Checks = []types.ComponentCheck{{ID: "optional-check"}}
			entries[0].State = interfaces.ComponentState{Level: "learning", Reads: 1, LastStudyAt: now.Add(-time.Minute), LastReadAt: now.Add(-time.Minute)}
			if difficult {
				entries[0].State.Level = "review"
				entries[0].State.SelfReport = "difficult"
				entries[0].State.LastDifficultyAt = now.Add(-time.Hour)
			}
			before := entries[0].State
			steps, _, info := planComponentsDetailed(entries, 15, "", now)
			require.Equal(t, "target", info.FocusGoalID)
			require.Equal(t, []string{"target"}, progressionIDs(steps))
			require.Equal(t, "read", info.Stage)
			require.False(t, steps[0].Conditional)
			require.Contains(t, steps[0].Reason, "不代表前置能力已通过")
			require.Equal(t, before, entries[0].State, "planning cannot turn exposure into ability evidence")
		})
	}
}

func TestComponentOldVersionTouchesDoNotPrepareCurrentPrerequisite(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation")}
	entries[0].State = interfaces.ComponentState{Level: "touched", LegacyComponentTouches: 9, LegacyTouches: 20}
	steps, _, _ := planComponentsDetailed(entries, 15, "target", time.Now())
	require.Equal(t, []string{"foundation", "target"}, progressionIDs(steps))
	require.True(t, steps[1].Conditional)
	require.Zero(t, entries[0].State.Reads)
	require.Zero(t, entries[0].State.Passes)
}

func TestComponentSelfReportedGoalDoesNotForceUnreadAncestry(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation")}
	entries[1].State.Level = "self_reported"
	entries[1].State.SelfReport = "known"
	steps, _, info := planComponentsDetailed(entries, 15, "target", time.Now())
	require.Empty(t, steps)
	require.Equal(t, "idle", info.Stage)
	require.Contains(t, info.Notice, "不补记独立检查证据")
	require.Zero(t, entries[0].State.Reads)
}

func TestComponentRecallRevisitsGoalWithoutReopeningWholePrerequisiteChain(t *testing.T) {
	now := time.Now()
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation")}
	entries[1].State = interfaces.ComponentState{Level: "review", Reads: 1, SelfReport: "difficult", LastReadAt: now.Add(-time.Minute), LastDifficultyAt: now.Add(-time.Hour)}
	steps, _, info := planComponentsDetailed(entries, 15, "", now)
	require.Equal(t, []string{"target"}, progressionIDs(steps))
	require.Equal(t, "recall", info.Stage)
	require.Empty(t, steps[0].PrerequisiteIDs)
	require.Zero(t, entries[0].State.Reads)
}

func TestComponentRevisitKeepsMissingAndUnavailablePrerequisiteGuards(t *testing.T) {
	for _, action := range []string{"check", "recall"} {
		for _, invalid := range []string{"missing", "unavailable"} {
			t.Run(action+"_"+invalid, func(t *testing.T) {
				now := time.Now()
				entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation")}
				entries[1].State = interfaces.ComponentState{Level: "learning", Reads: 1, LastReadAt: now.Add(-time.Minute)}
				if action == "check" {
					entries[1].Material.Checks = []types.ComponentCheck{{ID: "optional-check"}}
				} else {
					entries[1].State.Level = "review"
					entries[1].State.LastDifficultyAt = now.Add(-time.Hour)
				}
				steps, _, info := planComponentsDetailed(entries, 15, "target", now)
				require.Equal(t, []string{"target"}, progressionIDs(steps), "a known but unread prerequisite need not be reopened for a voluntary revisit")
				require.Equal(t, action, info.Stage)
				if invalid == "missing" {
					entries[1].Material.Prerequisites = []string{"unknown"}
				} else {
					entries[0].Available = false
				}
				steps, _, info = planComponentsDetailed(entries, 15, "target", now)
				require.Empty(t, steps)
				require.Equal(t, "blocked", info.Stage)
			})
		}
	}
}

func TestComponentDueRecallCanInterruptContinuityButCannotEscapeExplicitModule(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute)
	entries := []interfaces.ComponentEntry{progressionEntry("done", "当前"), progressionEntry("next", "当前"), progressionEntry("due", "其他")}
	entries[0].State = interfaces.ComponentState{Level: "learning", Reads: 1, LastStudyAt: now.Add(-time.Second)}
	entries[2].State = interfaces.ComponentState{Level: "review", Reads: 1, LastRecallAt: now.Add(-time.Hour), DueAt: &due}
	steps, _, info := planComponentsDetailed(entries, 15, "", now)
	require.Equal(t, []string{"due"}, progressionIDs(steps))
	require.Contains(t, info.FocusReason, "复习时间")
	steps, _, info = planComponentsWithTopic(entries, 15, "", "当前", now)
	require.Equal(t, []string{"next"}, progressionIDs(steps))
	require.Equal(t, "当前", info.TopicScope)
}

func TestComponentDefaultSkipsBrokenSourceChainButExplicitScopeRemainsHonest(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation"), progressionEntry("other", "其他")}
	entries[0].Available = false
	steps, _, info := planComponentsDetailed(entries, 15, "", time.Now())
	require.Equal(t, []string{"other"}, progressionIDs(steps), "a stale dependency must not indefinitely hide another executable goal")
	require.Equal(t, "other", info.FocusGoalID)
	steps, _, info = planComponentsWithTopic(entries, 15, "", "应用", time.Now())
	require.Empty(t, steps, "an explicit module cannot silently expand to an unrelated module")
	require.Equal(t, "target", info.FocusGoalID)
	require.Equal(t, "blocked", info.Stage)
	require.Contains(t, info.Progression, "不会假定缺失的前置已经准备好")
}

func TestComponentDefaultFocusFallsBackWhenItsFirstActionExceedsBudget(t *testing.T) {
	now := time.Now()
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("target", "应用", "foundation"), progressionEntry("other", "应用")}
	entries[0].Material.Minutes = 10
	for _, topic := range []string{"", "应用"} {
		steps, used, info := planComponentsWithTopic(entries, 5, "", topic, now)
		require.Equal(t, "other", info.FocusGoalID)
		require.Equal(t, []string{"other"}, progressionIDs(steps))
		require.Equal(t, 3, used)
		require.Contains(t, info.FocusReason, "暂时无法起步")
		steps, _, info = planComponentsWithTopic(entries, 5, "target", topic, now)
		require.Empty(t, steps, "an explicit target must not silently turn into a different learning goal")
		require.Equal(t, "target", info.FocusGoalID)
		require.Equal(t, "blocked", info.Stage)
	}
	entries[2].Material.Topic = "别处"
	steps, _, info := planComponentsWithTopic(entries, 5, "", "应用", now)
	require.Empty(t, steps, "fallback must respect module scope even when another module has a cheap action")
	require.Equal(t, "target", info.FocusGoalID)
	require.Equal(t, "blocked", info.Stage)
}

func TestComponentDefaultFocusKeepsPartialProgressWhenPrerequisitesExceedFixedBudget(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("foundation", "基础"), progressionEntry("middle", "基础", "foundation"), progressionEntry("target", "应用", "middle"), progressionEntry("other", "别处")}
	entries[0].Material.Minutes, entries[1].Material.Minutes = 10, 10
	steps, used, info := planComponentsDetailed(entries, 15, "", time.Now())
	require.Equal(t, "target", info.FocusGoalID, "a 20-minute prerequisite chain is still worth starting within the fixed 15-minute limit")
	require.Equal(t, []string{"foundation"}, progressionIDs(steps))
	require.Equal(t, 10, used)
	require.Equal(t, "read", info.Stage)
	require.Contains(t, info.Notice, "尚未覆盖目标本身")
	require.NotContains(t, info.FocusReason, "暂时无法起步")
}

func TestComponentRelatedAndTopicDoNotInventPrerequisitesOrApplicationTasks(t *testing.T) {
	entries := []interfaces.ComponentEntry{progressionEntry("first", "主题"), progressionEntry("second", "主题")}
	entries[1].Material.Related = []string{"first"}
	steps, _, info := planComponentsDetailed(entries, 15, "", time.Now())
	require.Equal(t, []string{"first"}, progressionIDs(steps))
	require.False(t, steps[0].Conditional)
	require.Contains(t, strings.Join(info.Limitations, " "), "没有已声明先修")
	require.Contains(t, strings.Join(info.Limitations, " "), "未配置可执行检查")
	entries[0], entries[1] = entries[1], entries[0]
	reversed, _, again := planComponentsDetailed(entries, 15, "", time.Now())
	require.Equal(t, progressionIDs(steps), progressionIDs(reversed))
	require.Equal(t, info.FocusGoalID, again.FocusGoalID)
	for i := range entries {
		entries[i].State = interfaces.ComponentState{Level: "learning", Reads: 1}
	}
	steps, _, info = planComponentsDetailed(entries, 15, "", time.Now())
	require.Empty(t, steps)
	require.Equal(t, "idle", info.Stage)
	require.Contains(t, info.Notice, "不代表全部知识已掌握")
	require.Contains(t, strings.Join(info.Limitations, " "), "没有配置可执行检查")
}

func TestComponentScopeServicePreservesCatalogueAndRejectsForeignModuleGoal(t *testing.T) {
	rows, pages := componentFixture(t)
	defs := []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	defs[0].Topic, defs[1].Topic = "基础", "应用"
	defs[1].Prerequisites = []string{defs[0].Key}
	rows, err := ValidateComponentPack(1, testKB, defs, pages)
	require.NoError(t, err)
	repo := &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}
	svc := NewService(repo, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	ctx := collectorCtx(1, "alice")
	view, err := svc.ComponentViewWithTopic(ctx, testKB, 15, "", "应用")
	require.NoError(t, err)
	require.Len(t, view.Components, 2, "scope filters the plan, not the graph catalogue")
	require.Equal(t, []string{rows[0].ID, rows[1].ID}, progressionIDs(view.Steps))
	require.Equal(t, "应用", view.Plan.TopicScope)
	_, err = svc.ComponentViewWithTopic(ctx, testKB, 15, rows[0].ID, "应用")
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
	_, err = svc.ComponentViewWithTopic(ctx, testKB, 15, "", "不存在的模块")
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
	_, err = svc.ComponentViewWithTopic(collectorCtx(2, "alice"), testKB, 15, "", "应用")
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
}
