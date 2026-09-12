package learning

import (
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func wikiFocusFixture() planInput {
	return planInput{GoalsResolved: true, IncludeExploration: true, TimeBudgetMinutes: 15, AsOf: time.Now(),
		Pages:       map[string]string{"base": "必要背景", "target": "目标页面", "other": "其他页面"},
		PageMinutes: map[string]int{"base": 3, "target": 3, "other": 1},
		PageFolders: map[string]string{"base": "background", "target": "application", "other": "other"},
		Estimates:   map[string]*interfaces.LearningEstimate{"base": {Level: "unseen", ReadPriority: 1}, "target": {Level: "unseen", ReadPriority: 1}, "other": {Level: "unseen", ReadPriority: 1}},
		StrictEdges: map[string][]string{"base": {"target"}}, Exposure: map[string]bool{}, NodeReview: map[string]bool{},
	}
}

func wikiFocusSlugs(plan PathPlan) []string {
	out := []string{}
	for _, step := range plan.Steps {
		out = append(out, step.Slug)
	}
	return out
}

func TestWikiFocusDefaultUsesOnlyFocusAndManualPrerequisites(t *testing.T) {
	in := wikiFocusFixture()
	p := planShortPath(in)
	require.Equal(t, "target", p.FocusSlug)
	require.Equal(t, []string{"base", "target"}, wikiFocusSlugs(p))
	require.Equal(t, []string{p.Steps[0].ID}, p.Steps[1].Requires)
	require.Contains(t, p.Progression, "人工标注")
	in.PageScope = map[string]bool{"target": true}
	p = planShortPath(in)
	require.Equal(t, []string{"base", "target"}, wikiFocusSlugs(p), "only necessary manual prerequisites may cross the selected module")
	in.PageScope = map[string]bool{"other": true}
	p = planShortPath(in)
	require.Equal(t, []string{"other"}, wikiFocusSlugs(p))
	require.Contains(t, strings.Join(p.Limitations, " "), "不能证明教学先后")
	transport := planToTransport(p)
	require.Equal(t, p.FocusSlug, transport.FocusSlug)
	require.Equal(t, p.Progression, transport.Progression)
	require.Equal(t, p.Limitations, transport.Limitations)
}

func TestWikiFocusContinuesRecentModuleAfterDueAndDifficultActions(t *testing.T) {
	in := wikiFocusFixture()
	in.Recent = []RecentNode{{Slug: "other", At: in.AsOf.Add(-time.Minute)}}
	require.Equal(t, "other", planShortPath(in).FocusSlug)
	in.NodeReview["target"] = true
	p := planShortPath(in)
	require.Equal(t, "target", p.FocusSlug)
	require.Equal(t, ActionRead, p.Steps[0].Action)
	in.RecallDue = map[string]PathReason{"base": {Code: "plan_reason_scheduled_recall", Detail: "真实到期回忆"}}
	in.RecallDueAt = map[string]time.Time{"base": in.AsOf.Add(-time.Minute)}
	p = planShortPath(in)
	require.Equal(t, "base", p.FocusSlug)
	require.Equal(t, []string{"base"}, wikiFocusSlugs(p))
	require.Equal(t, ActionRecall, p.Steps[0].Action)
	in.PageScope = map[string]bool{"other": true}
	require.Equal(t, "other", planShortPath(in).FocusSlug)
}

func TestWikiFocusBudgetFallbackAndLongChainPartialProgress(t *testing.T) {
	in := wikiFocusFixture()
	in.PageMinutes["base"], in.TimeBudgetMinutes = 8, 3
	p := planShortPath(in)
	require.Equal(t, "other", p.FocusSlug)
	require.Equal(t, []string{"other"}, wikiFocusSlugs(p))
	require.Contains(t, p.Progression, "暂时无法起步")
	in.PageScope = map[string]bool{"target": true}
	p = planShortPath(in)
	require.Empty(t, p.Steps)
	require.Equal(t, "target", p.FocusSlug)
	in.TimeBudgetMinutes = 15
	in.Pages["middle"], in.PageMinutes["middle"], in.PageFolders["middle"] = "中间背景", 8, "background"
	in.Estimates["middle"] = &interfaces.LearningEstimate{ReadPriority: 1, Level: "unseen"}
	in.StrictEdges = map[string][]string{"base": {"middle"}, "middle": {"target"}}
	p = planShortPath(in)
	require.Equal(t, "target", p.FocusSlug)
	require.Equal(t, []string{"base"}, wikiFocusSlugs(p), "a long manual chain can begin without fitting its full cost")
}

func TestWikiFocusReadPredecessorDoesNotRequireOptionalExam(t *testing.T) {
	in := wikiFocusFixture()
	in.PageScope = map[string]bool{"base": true, "target": true}
	in.Estimates["base"] = &interfaces.LearningEstimate{Level: "developing", Coverage: 1}
	in.Exposure["base"] = true
	o := objFixture("check-base", "base")
	in.Objectives = map[string]types.LearningObjective{o.ID: o}
	in.ObjBySlug = map[string][]string{"base": {o.ID}}
	in.HasVerification = map[string]bool{o.ID: true}
	p := planShortPath(in)
	require.Equal(t, "target", p.FocusSlug)
	require.Equal(t, []string{"target"}, wikiFocusSlugs(p))
	require.Equal(t, ActionRead, p.Steps[0].Action)
	require.Empty(t, p.Steps[0].Requires)
	require.False(t, in.NodeVerified["base"], "planning cannot turn reading into verification")
}

func TestWikiFocusCannotInventMissingSourcesOrDependencyReadiness(t *testing.T) {
	in := wikiFocusFixture()
	in.PageScope = map[string]bool{"target": true}
	in.ExcludedSlugs = map[string]bool{"base": true}
	p := planShortPath(in)
	require.Empty(t, p.Steps)
	require.Equal(t, "target", p.FocusSlug)
	// Service-side filtering removes dangling endpoints. The planner never
	// manufactures a page or claims a remaining source-position order is a
	// manual prerequisite chain.
	in.ExcludedSlugs = nil
	in.StrictEdges = map[string][]string{"unknown-source": {"target"}}
	p = planShortPath(in)
	require.Equal(t, []string{"target"}, wikiFocusSlugs(p))
	require.Contains(t, strings.Join(p.Limitations, " "), "没有可用的人工先修")
}

func TestWikiFocusEmptyChoicesDoNotRefillFromLegacyUnestimatedPages(t *testing.T) {
	in := wikiFocusFixture()
	in.Estimates = map[string]*interfaces.LearningEstimate{}
	for _, scope := range []map[string]bool{nil, {"target": true}} {
		in.PageScope = scope
		p := planShortPath(in)
		require.Empty(t, p.Steps, "missing estimates must not re-enter the legacy whole-catalogue fill")
		require.Empty(t, p.FocusSlug)
		require.Contains(t, strings.Join(p.Limitations, " "), "缺少当前阅读估计")
	}
}

func TestWikiFocusServiceScopeAndExplicitObjectiveNeverSilentlyChange(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	ctx := collectorCtx(1, "alice")
	_, err := svc.ShortPath(ctx, testKB, interfaces.ColdStartRequestPayload{GoalSlugs: []string{"unknown-source"}})
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
	require.NoError(t, upsertObj(repo, "explicit-rag", "concept/rag"))
	_, err = svc.ShortPath(ctx, testKB, interfaces.ColdStartRequestPayload{GoalObjectives: []string{"explicit-rag"}, GoalSlugs: []string{"concept/decay"}})
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
	for _, pages := range wiki.pages {
		for _, page := range pages {
			if page.Slug == "concept/rag" {
				page.Content = strings.Repeat("有效的原始材料内容", 200)
			}
		}
	}
	plan, err := svc.ShortPath(ctx, testKB, interfaces.ColdStartRequestPayload{GoalObjectives: []string{"explicit-rag"}, TimeBudgetMin: 1})
	require.NoError(t, err)
	require.Empty(t, plan.Steps, "the unavailable explicit objective must not silently become another short Wiki page")
}
