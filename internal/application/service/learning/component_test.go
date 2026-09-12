package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func componentFixture(t *testing.T) ([]types.LearningComponent, []*types.WikiPage) {
	t.Helper()
	page := &types.WikiPage{TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/shared", PageType: "concept", Content: "来源说明：同一页面同时介绍记录事实与重算状态，两项能力应当分别观察。"}
	check := types.ComponentCheck{ID: "q1", Family: "f1", Question: "怎样恢复状态？", Options: map[string]string{"A": "重放事实", "B": "删除事实", "C": "猜一个分数", "D": "重写答案"}, Answer: "A", Explanation: "从原事实重放可重建派生状态。"}
	defs := []types.ComponentDefinition{}
	for _, key := range []string{"record", "replay"} {
		defs = append(defs, types.ComponentDefinition{Key: key, Title: key, Topic: "例子", Condition: "明确的学习条件", Goal: "独立检验的学习目标" + key, Explanation: "此组件解释记录与状态的关系，使用完整材料来学习。一次阅读只代表接触机会，需要把实际检查记录归属于对应的目标，不能推广到整个页面。", Example: "假设一页含两个目标，检查其中一个，另一个保持没有观察的状态。", Minutes: 2, Provenance: "测试作者样本", Sources: []types.ComponentSource{{Slug: page.Slug, Quote: "同一页面同时介绍记录事实与重算状态", Role: "依据"}}, Checks: []types.ComponentCheck{check}})
	}
	rows, err := ValidateComponentPack(1, testKB, defs, []*types.WikiPage{page})
	require.NoError(t, err)
	return rows, []*types.WikiPage{page}
}
func componentEvent(c types.LearningComponent, id, action string, at time.Time) types.LearningEvent {
	f := componentFact{ComponentID: c.ID, Title: c.Definition.Title, Action: action, OperationID: id, Family: "f1", CheckID: "q1", Correct: true, Eligible: true, ModelVersion: componentModelVersion}
	data, _ := json.Marshal(f)
	return types.LearningEvent{ID: id, TenantID: 1, KnowledgeBaseID: testKB, SubjectID: "web_user:alice", Slug: "kc:" + c.ID, Type: types.LearningEventComponent, ContentVersion: c.Version, OccurredAt: at, ReviewData: data}
}
func TestComponentGranularitySeparatesSamePageEvidence(t *testing.T) {
	rows, _ := componentFixture(t)
	now := time.Now()
	events := []types.LearningEvent{{ID: "old", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/shared", Type: types.LearningEventWikiDeepRead, OccurredAt: now.Add(-time.Hour)}, componentEvent(rows[0], "answer", "check", now)}
	first, other := projectComponent(rows[0], events, now), projectComponent(rows[1], events, now)
	require.Equal(t, 1, first.Checks)
	require.Equal(t, 1, first.Passes)
	require.Zero(t, other.Checks)
	require.Equal(t, "unseen", other.Level)
	require.Equal(t, .2, other.Familiarity)
	require.Equal(t, 1, other.LegacyTouches)
	t.Log("same-page counterexample: broadcasting page evidence would credit 2 goals; KC credits only the 1 observed goal; old source contact grants 0 checks")
}

// Audit the real projector, rather than reproducing its equation in a benchmark.
// Reading may advance navigation but cannot certify performance or narrow its
// evidence-based uncertainty in the absence of any independent response.
func TestComponentPassiveSignalsDoNotManufacturePerformance(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	baseline := projectComponent(c, nil, now)
	audit := []map[string]any{}
	for _, days := range []int{0, 1, 3, 30, 365} {
		events := []types.LearningEvent{}
		for d := 0; d < days; d++ {
			events = append(events, componentEvent(c, fmt.Sprintf("read-%d", d), "read", now.AddDate(0, 0, -d)))
		}
		state := projectComponent(c, events, now)
		audit = append(audit, map[string]any{"days": days, "level": state.Level, "index": state.Familiarity, "lower": state.Lower, "upper": state.Upper, "checks": state.Checks})
	}
	encoded, err := json.Marshal(audit)
	require.NoError(t, err)
	t.Log("PASSIVE_AUDIT " + string(encoded))
	for _, record := range audit {
		require.Equal(t, baseline.Familiarity, record["index"], "unobserved performance cannot be learned from passive time alone: %+v", record)
		require.Equal(t, baseline.Lower, record["lower"])
		require.Equal(t, baseline.Upper, record["upper"])
		require.Zero(t, record["checks"])
		if record["days"].(int) > 0 {
			require.Equal(t, "learning", record["level"])
		}
	}
}
func TestComponentVersionsAndFutureEvidenceDoNotLeak(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	now := time.Now()
	old := componentEvent(c, "old", "check", now.Add(-time.Hour))
	c.Version = "changed"
	future := componentEvent(c, "future", "check", now.Add(time.Hour))
	state := projectComponent(c, []types.LearningEvent{old, future}, now)
	require.Zero(t, state.Checks)
	require.Zero(t, state.Reads)
	require.False(t, state.PerformanceObserved)
	require.Equal(t, 1, state.LegacyComponentTouches)
	require.Equal(t, "touched", state.Level)
	require.Contains(t, state.Basis, "旧版")
}

func TestComponentSelfReportChangesNavigationNotPerformance(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	now := time.Now()
	baseline := projectComponent(c, nil, now)
	for _, action := range []string{"known", "difficult"} {
		state := projectComponent(c, []types.LearningEvent{componentEvent(c, action, action, now)}, now)
		require.Equal(t, baseline.Familiarity, state.Familiarity)
		require.False(t, state.PerformanceObserved)
		require.Zero(t, state.Passes)
		entry := interfaces.ComponentEntry{ID: c.ID, Available: true, Material: c.Definition, State: state}
		steps, _ := planComponents([]interfaces.ComponentEntry{entry}, 5, "", now)
		if action == "known" {
			require.Equal(t, "self_reported", state.Level)
			require.Empty(t, steps, "respect skip intent without asserting measured mastery")
		} else {
			require.Equal(t, "review", state.Level)
			require.Equal(t, "read", steps[0].Action)
		}
	}
}

func TestComponentSupportNeedsDifferentIndependentFamilies(t *testing.T) {
	rows, _ := componentFixture(t)
	c, now := rows[0], time.Now()
	first := componentEvent(c, "first", "check", now.Add(-time.Minute))
	second := componentEvent(c, "second", "check", now)
	var fact componentFact
	require.NoError(t, json.Unmarshal(second.ReviewData, &fact))
	fact.Family, fact.CheckID = "another-family", "q2"
	second.ReviewData, _ = json.Marshal(fact)
	state := projectComponent(c, []types.LearningEvent{first, second}, now)
	require.Equal(t, "familiar", state.Level)
	require.True(t, state.PerformanceObserved)
	fact.Correct = false
	second.ReviewData, _ = json.Marshal(fact)
	state = projectComponent(c, []types.LearningEvent{first, second}, now)
	require.Equal(t, "review", state.Level, "new contrary evidence replaces the support label")
}
func TestComponentDuplicateFamilyAndReportsCannotAccumulate(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	now := time.Now()
	one := componentEvent(c, "1", "known", now.Add(-time.Hour))
	two := componentEvent(c, "2", "known", now.Add(-time.Minute))
	require.Equal(t, projectComponent(c, []types.LearningEvent{one}, now).Familiarity, projectComponent(c, []types.LearningEvent{one, two}, now).Familiarity)
	a := componentEvent(c, "3", "check", now)
	b := componentEvent(c, "4", "check", now)
	state := projectComponent(c, []types.LearningEvent{a, b}, now)
	require.Equal(t, 1, state.Checks)
	require.Equal(t, 1, state.Practice)
}
func TestComponentIndependentEvidenceSupersedesOldDifficulty(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	now := time.Now()
	report := componentEvent(c, "a", "difficult", now.Add(-time.Hour))
	check := componentEvent(c, "b", "check", now)
	state := projectComponent(c, []types.LearningEvent{report, check}, now)
	require.NotEqual(t, "review", state.Level)
	require.Greater(t, state.Familiarity, .2)
}
func TestComponentPackRejectsWrongSourcesAndCycles(t *testing.T) {
	rows, pages := componentFixture(t)
	defs := []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	defs[0].Sources[0].Quote = "这个句子从未出现在被引用的材料之中"
	_, err := ValidateComponentPack(1, testKB, defs, pages)
	require.Error(t, err)
	rows, pages = componentFixture(t)
	defs = []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	defs[0].Prerequisites = []string{"replay"}
	defs[1].Prerequisites = []string{"record"}
	_, err = ValidateComponentPack(1, testKB, defs, pages)
	require.Error(t, err)
	_, err = ValidateComponentPack(2, testKB, []types.ComponentDefinition{rows[0].Definition}, pages)
	require.Error(t, err)
}
func TestComponentMultiSourceStableIdentityAndContextBoundary(t *testing.T) {
	rows, pages := componentFixture(t)
	def := rows[0].Definition
	second := *pages[0]
	second.Slug = "concept/other-document"
	pages = append(pages, &second)
	def.Sources = append(def.Sources, types.ComponentSource{Slug: second.Slug, Quote: def.Sources[0].Quote, Role: "另一个文档中的同一规则"})
	added, err := ValidateComponentPack(1, testKB, []types.ComponentDefinition{def}, pages)
	require.NoError(t, err)
	require.Equal(t, rows[0].ID, added[0].ID)
	require.NotEqual(t, rows[0].Version, added[0].Version)
	other := def
	other.Key = "different-role"
	other.Condition = "同一个术语的另一种使用条件"
	split, err := ValidateComponentPack(1, testKB, []types.ComponentDefinition{def, other}, pages)
	require.NoError(t, err)
	require.NotEqual(t, split[0].ID, split[1].ID)
}
func TestComponentPlannerBudgetPrerequisiteAndUnavailableSource(t *testing.T) {
	rows, _ := componentFixture(t)
	rows[1].Definition.Prerequisites = []string{rows[0].Definition.Key}
	entries := []interfaces.ComponentEntry{}
	for _, r := range rows {
		entries = append(entries, interfaces.ComponentEntry{ID: r.ID, Available: true, Material: r.Definition, State: interfaces.ComponentState{Level: "unseen", Familiarity: .2}})
	}
	steps, minutes := planComponents(entries, 4, rows[1].ID, time.Now())
	require.Len(t, steps, 2)
	require.Equal(t, rows[0].ID, steps[0].ID)
	require.Equal(t, 4, minutes)
	steps, minutes = planComponents(entries, 1, rows[1].ID, time.Now())
	require.Empty(t, steps)
	require.Zero(t, minutes)
	entries[0].Available = false
	entries[0].State.Reads = 1 // Old evidence cannot unlock a stale prerequisite.
	steps, _ = planComponents(entries, 8, rows[1].ID, time.Now())
	require.Empty(t, steps)
}

type componentStubRepo struct {
	*stubLearningRepo
	components []types.LearningComponent
}

func (r *componentStubRepo) ListComponents(_ context.Context, tenant uint64, kb string) ([]types.LearningComponent, error) {
	out := []types.LearningComponent{}
	for _, c := range r.components {
		if c.TenantID == tenant && c.KnowledgeBaseID == kb {
			out = append(out, c)
		}
	}
	return out, nil
}
func (r *componentStubRepo) SaveComponents(_ context.Context, rows []types.LearningComponent) error {
	r.components = rows
	return nil
}
func TestComponentServiceScopeSecrecyDedupAndConsent(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	rows, pages := componentFixture(t)
	repo := &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}
	svc := NewService(repo, wiki, nil, nil, nil, nil)
	ctx := collectorCtx(1, "alice")
	view, err := svc.ComponentView(ctx, testKB, 15, "")
	require.NoError(t, err)
	require.Len(t, view.Components, 2)
	require.Empty(t, view.Components[0].Material.Checks[0].Answer)
	require.Empty(t, view.Components[0].Material.Checks[0].Explanation)
	input := interfaces.ComponentAction{ID: rows[0].ID, Version: rows[0].Version, Action: "check", CheckID: "q1", Answer: "A", OperationID: uuid.NewString()}
	result, err := svc.RecordComponentAction(ctx, testKB, input)
	require.NoError(t, err)
	require.True(t, result.Eligible)
	require.True(t, *result.Correct)
	duplicate, err := svc.RecordComponentAction(ctx, testKB, input)
	require.NoError(t, err)
	require.True(t, duplicate.Duplicate)
	require.Len(t, repo.events, 1)
	input.Answer = "B"
	_, err = svc.RecordComponentAction(ctx, testKB, input)
	require.Error(t, err)
	input.OperationID = uuid.NewString()
	practice, err := svc.RecordComponentAction(ctx, testKB, input)
	require.NoError(t, err)
	require.False(t, practice.Eligible)
	other, err := svc.ComponentView(collectorCtx(1, "bob"), testKB, 15, "")
	require.NoError(t, err)
	for _, c := range other.Components {
		require.Zero(t, c.State.Checks)
	}
	input.OperationID = uuid.NewString()
	_, err = svc.RecordComponentAction(collectorCtx(2, "alice"), testKB, input)
	require.Error(t, err)
	require.NoError(t, repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{SubjectID: "web_user:alice", CollectDisabled: true}))
	disabled, err := svc.RecordComponentAction(ctx, testKB, input)
	require.NoError(t, err)
	require.False(t, disabled.Recorded)
}
func TestComponentReadNeedsServerObservedOpportunity(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	rows, pages := componentFixture(t)
	repo := &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}
	svc := NewService(repo, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	ctx := collectorCtx(1, "alice")
	in := interfaces.ComponentAction{ID: rows[0].ID, Version: rows[0].Version, Action: "read", OperationID: uuid.NewString(), SessionID: "session"}
	_, err := svc.RecordComponentAction(ctx, testKB, in)
	require.Error(t, err)
	in.Action = "open"
	_, err = svc.RecordComponentAction(ctx, testKB, in)
	require.NoError(t, err)
	in.Action = "read"
	in.OperationID = uuid.NewString()
	_, err = svc.RecordComponentAction(ctx, testKB, in)
	require.Error(t, err)
	repo.events[0].OccurredAt = time.Now().Add(-5 * time.Minute)
	result, err := svc.RecordComponentAction(ctx, testKB, in)
	require.NoError(t, err)
	require.True(t, result.Recorded)
	require.Equal(t, 1, result.State.Reads)
	in.OperationID = uuid.NewString()
	result, err = svc.RecordComponentAction(ctx, testKB, in)
	require.NoError(t, err)
	require.True(t, result.Duplicate)
}

func TestComponentReviewMovesFromExampleToFreshCheck(t *testing.T) {
	rows, _ := componentFixture(t)
	c := rows[0]
	c.Definition.Checks = append(c.Definition.Checks, types.ComponentCheck{ID: "q2", Family: "f2"})
	now := time.Now()
	failed := componentEvent(c, "failed", "check", now.Add(-time.Minute))
	var fact componentFact
	require.NoError(t, json.Unmarshal(failed.ReviewData, &fact))
	fact.Correct = false
	failed.ReviewData, _ = json.Marshal(fact)
	events := []types.LearningEvent{componentEvent(c, "first-read", "read", now.Add(-time.Hour)), failed}
	entry := interfaces.ComponentEntry{ID: c.ID, Available: true, Material: c.Definition, State: projectComponent(c, events, now)}
	steps, _ := planComponents([]interfaces.ComponentEntry{entry}, 5, "", now)
	require.Equal(t, "read", steps[0].Action)
	events = append(events, componentEvent(c, "revisit", "read", now))
	entry.State = projectComponent(c, events, now)
	require.Equal(t, 1, entry.State.Reads) // Same-day revisit doesn't farm the index.
	steps, _ = planComponents([]interfaces.ComponentEntry{entry}, 5, "", now)
	require.Equal(t, "check", steps[0].Action)
	events = append(events, componentEvent(c, "correction", "known", now.Add(time.Second)))
	state := projectComponent(c, events, now.Add(time.Second))
	require.Equal(t, "self_reported", state.Level)
	require.Zero(t, state.Passes) // A correction never manufactures a passing check.
}

func TestComponentRecallUsesOnlyExplicitRecallAndBlocksRapidRepeat(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	rows, pages := componentFixture(t)
	repo := &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}
	svc := NewService(repo, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	ctx := collectorCtx(1, "alice")
	input := interfaces.ComponentAction{ID: rows[0].ID, Version: rows[0].Version, Action: "recall", Rating: 3, OperationID: uuid.NewString()}
	result, err := svc.RecordComponentAction(ctx, testKB, input)
	require.NoError(t, err)
	require.NotNil(t, result.State.DueAt)
	require.Equal(t, .2, result.State.Familiarity)
	require.Zero(t, result.State.Checks)
	input.OperationID = uuid.NewString()
	_, err = svc.RecordComponentAction(ctx, testKB, input)
	require.Error(t, err)
	pages[0].Content += "来源后来发生变化。"
	view, err := svc.ComponentView(ctx, testKB, 15, "")
	require.NoError(t, err)
	for _, c := range view.Components {
		require.False(t, c.Available)
	}
	require.Empty(t, view.Steps)
	input.Action = "known"
	_, err = svc.RecordComponentAction(ctx, testKB, input)
	require.ErrorIs(t, err, interfaces.ErrLearningReviewConflict)
}
