package learning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// These are actual first model outputs corrected by a disclosed author review,
// not labels used to claim automatic semantic accuracy. Run the production
// importer/projector/planner over three topics, without a model or a database.
func TestReviewedCrossTopicMaterialsUseProductionAlgorithms(t *testing.T) {
	dir := "testdata"
	data, err := os.ReadFile(filepath.Join(dir, "material-boundary-sources-reviewed.json"))
	require.NoError(t, err)
	var pages []*types.WikiPage
	require.NoError(t, json.Unmarshal(data, &pages))
	for _, p := range pages {
		p.TenantID = 1
		p.KnowledgeBaseID = testKB
	}
	data, err = os.ReadFile(filepath.Join(dir, "material-boundary-reviewed-pack.json"))
	require.NoError(t, err)
	var pack struct {
		Components []types.ComponentDefinition `json:"components"`
	}
	require.NoError(t, json.Unmarshal(data, &pack))
	rows, err := ValidateComponentPack(1, testKB, pack.Components, pages)
	require.NoError(t, err)
	require.Len(t, rows, 11)
	entries := []interfaces.ComponentEntry{}
	now := time.Now()
	for _, c := range rows {
		require.Empty(t, c.Definition.Checks)
		require.Empty(t, c.Definition.Prerequisites, "model relations stay soft")
		entries = append(entries, interfaces.ComponentEntry{ID: c.ID, Material: c.Definition, Available: componentAvailable(c, pages), State: projectComponent(c, nil, now)})
	}
	steps, minutes := planComponents(entries, 15, "", now)
	require.NotEmpty(t, steps)
	require.LessOrEqual(t, minutes, 15)
	for _, s := range steps {
		require.Equal(t, "read", s.Action)
	}
	for i, c := range rows {
		entries[i].State = projectComponent(c, []types.LearningEvent{componentEvent(c, "read-"+c.ID, "read", now.Add(-time.Minute))}, now)
		require.False(t, entries[i].State.PerformanceObserved)
		require.Equal(t, "learning", entries[i].State.Level)
	}
	steps, _ = planComponents(entries, 15, "", now)
	require.Empty(t, steps, "read-only materials do not generate mandatory quizzes or reread loops")
	// The original source revision really is rejected after the source-level
	// numerical exception was corrected; matching an old quote is insufficient.
	oldData, err := os.ReadFile(filepath.Join(dir, "material-boundary-sources.json"))
	require.NoError(t, err)
	var oldPages []*types.WikiPage
	require.NoError(t, json.Unmarshal(oldData, &oldPages))
	for _, p := range oldPages {
		p.TenantID = 1
		p.KnowledgeBaseID = testKB
	}
	_, err = ValidateComponentPack(1, testKB, pack.Components, oldPages)
	require.Error(t, err)
	t.Log("3 topics / 11 reviewed synthetic components: production import, version checks, budget planning, and reading-only state agree; not a learning-effect study")
}

func TestComponentWithoutChecksSupportsLearningAndRealRecall(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	rows, pages := componentFixture(t)
	d := rows[0].Definition
	d.Checks = nil
	rows, err := ValidateComponentPack(1, testKB, []types.ComponentDefinition{d}, pages)
	require.NoError(t, err)
	require.NotNil(t, rows[0].Definition.Checks, "API must return an empty array, not null")
	c := rows[0]
	now := time.Now()
	state := projectComponent(c, []types.LearningEvent{componentEvent(c, "read-only", "read", now.Add(-time.Minute))}, now)
	require.Equal(t, "learning", state.Level)
	require.False(t, state.PerformanceObserved)
	entries := []interfaces.ComponentEntry{{ID: c.ID, Material: c.Definition, Available: true, State: state}}
	steps, _ := planComponents(entries, 15, "", now)
	require.Empty(t, steps, "do not invent checks or repeat the completed read")
	r := &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}
	s := NewService(r, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	result, err := s.RecordComponentAction(collectorCtx(1, "alice"), testKB, interfaces.ComponentAction{ID: c.ID, Version: c.Version, Action: "recall", Rating: 3, OperationID: uuid.NewString(), SessionID: uuid.NewString()})
	require.NoError(t, err)
	require.True(t, result.Recorded)
	require.NotNil(t, result.State.DueAt)
	require.Zero(t, result.State.Checks)
	require.False(t, result.State.PerformanceObserved)
}

func draftServiceFixture(t *testing.T, defs []types.ComponentDefinition) (*Service, *fakeChatModel, []*types.WikiPage) {
	_, pages := componentFixture(t)
	data, err := json.Marshal(map[string]any{"candidates": defs, "notes": []string{}})
	require.NoError(t, err)
	fake := &fakeChatModel{responses: []*types.ChatResponse{{Content: string(data), FinishReason: "stop"}}}
	s := NewService(nil, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, &stubModelService{model: fake}, &stubKBRepo{kbs: map[string]*types.KnowledgeBase{testKB: {ID: testKB, TenantID: 1, SummaryModelID: "model"}}}, nil)
	return s, fake, pages
}

func TestComponentDraftCannotCertifyItselfOrImposePrerequisites(t *testing.T) {
	rows, pages := componentFixture(t)
	defs := []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	defs[1].Prerequisites = []string{defs[0].Key}
	defs[0].MaterialStatus = "ready"
	defs[0].ReviewNote = "模型试图将自身的内容审核结果当作已完成真实复核，这个声明必须丢弃。"
	s, fake, _ := draftServiceFixture(t, defs)
	result, err := s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2})
	require.NoError(t, err)
	require.Len(t, result.Candidates, 2)
	require.True(t, result.ReviewRequired)
	require.Equal(t, int32(1), fake.calls.Load())
	for _, c := range result.Candidates {
		require.Equal(t, "draft", c.Material.MaterialStatus)
		require.Empty(t, c.Material.ReviewNote)
		require.Empty(t, c.Material.Checks)
		require.Empty(t, c.Material.Prerequisites)
		require.NotEmpty(t, c.Material.Sources[0].Hash)
		_, err := ValidateComponentPack(1, testKB, []types.ComponentDefinition{c.Material}, pages)
		require.Error(t, err, "a model candidate is not a publishable pack")
	}
	require.Equal(t, []string{defs[0].Key}, result.Candidates[1].Material.Related)
	input, ok := fake.lastUserUnder(componentDraftPrompt)
	require.True(t, ok)
	require.Contains(t, input, pages[0].Content)
	require.NotContains(t, input, "alice", "drafting must not disclose the requester or personal state")
}

func TestComponentDraftRejectsMissingForeignAndChangingSources(t *testing.T) {
	rows, pages := componentFixture(t)
	for _, change := range []func(*types.ComponentDefinition){
		func(d *types.ComponentDefinition) { d.Sources[0].Slug = "foreign-source" },
		func(d *types.ComponentDefinition) {
			d.Sources[0].Quote = "这是来源中并不存在的完整引文，不能通过检查。"
		},
	} {
		raw, _ := json.Marshal(rows[0].Definition)
		var d types.ComponentDefinition
		require.NoError(t, json.Unmarshal(raw, &d))
		change(&d)
		s, _, _ := draftServiceFixture(t, []types.ComponentDefinition{d})
		result, err := s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2})
		require.NoError(t, err)
		require.Empty(t, result.Candidates)
		require.Len(t, result.Rejected, 1)
	}
	s, fake, mutable := draftServiceFixture(t, []types.ComponentDefinition{rows[0].Definition})
	fake.onCall = func() { mutable[0].Content += "\n但这一结论有新的适用限制。" }
	result, err := s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, "sources_changed", result.Status)
	require.Empty(t, result.Candidates)
}

func TestComponentDraftScopeBudgetAndMalformedOutput(t *testing.T) {
	rows, pages := componentFixture(t)
	s, fake, _ := draftServiceFixture(t, []types.ComponentDefinition{rows[0].Definition})
	_, err := s.DraftComponents(collectorCtx(2, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2})
	require.Error(t, err)
	require.Zero(t, fake.calls.Load())
	_, err = s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2, ModelSource: "unknown-provider"})
	require.Error(t, err)
	require.Zero(t, fake.calls.Load())
	_, _, err = componentDraftSources(1, testKB, []string{pages[0].Slug}, []*types.WikiPage{{TenantID: 2, KnowledgeBaseID: testKB, Slug: pages[0].Slug, Content: pages[0].Content}})
	require.Error(t, err)
	pages[0].Content = strings.Repeat("文", 8001)
	_, _, err = componentDraftSources(1, testKB, []string{pages[0].Slug}, pages)
	require.Error(t, err, "do not silently cut the page")
	for _, output := range []string{"{}", "not JSON"} {
		s, fake, pages := draftServiceFixture(t, []types.ComponentDefinition{})
		fake.responses[0].Content = output
		_, err := s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2})
		require.ErrorIs(t, err, errMalformedLLM)
		require.Equal(t, int32(1), fake.calls.Load())
	}
}

func TestComponentDraftUsesOnlyExplicitlyConfiguredModelSource(t *testing.T) {
	rows, pages := componentFixture(t)
	s, _, _ := draftServiceFixture(t, []types.ComponentDefinition{rows[0].Definition})
	s.kbRepo.(*stubKBRepo).kbs[testKB].WikiConfig = &types.WikiConfig{SynthesisModelID: "wiki-model"}
	result, err := s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2, ModelSource: "summary"})
	require.NoError(t, err)
	require.Equal(t, "model", result.ModelID)
	require.Equal(t, []string{"model"}, s.modelService.(*stubModelService).idsSeen)
	s, _, _ = draftServiceFixture(t, []types.ComponentDefinition{rows[0].Definition})
	result, err = s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2, ModelID: "existing-tenant-model"})
	require.NoError(t, err)
	require.Equal(t, "existing-tenant-model", result.ModelID)
	require.Equal(t, []string{"existing-tenant-model"}, s.modelService.(*stubModelService).idsSeen)
	_, err = s.DraftComponents(collectorCtx(1, "alice"), testKB, interfaces.ComponentDraftRequest{Slugs: []string{pages[0].Slug}, Limit: 2, ModelID: "existing-tenant-model", ModelSource: "summary"})
	require.Error(t, err)
	require.Len(t, s.modelService.(*stubModelService).idsSeen, 1)
}

func TestComponentDraftDuplicateBoundaryAndReviewedSourceVersion(t *testing.T) {
	rows, pages := componentFixture(t)
	defs := []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	defs[1].Condition, defs[1].Goal = defs[0].Condition, defs[0].Goal
	accepted, rejected := screenComponentDrafts(1, testKB, defs, pages)
	require.Empty(t, accepted)
	require.Len(t, rejected, 2)
	accepted, _ = screenComponentDrafts(1, testKB, defs[:1], pages)
	d := accepted[0].Material
	d.MaterialStatus = "ready"
	d.ReviewNote = "测试作者核对了目标、条件和解释与来源的对应关系；这不是独立学习者审校。"
	_, err := ValidateComponentPack(1, testKB, []types.ComponentDefinition{d}, pages)
	require.NoError(t, err)
	pages[0].Content += "\n新版本修改了原说明的条件。"
	_, err = ValidateComponentPack(1, testKB, []types.ComponentDefinition{d}, pages)
	require.Error(t, err, "unchanged quote alone cannot validate changed surrounding context")
}
