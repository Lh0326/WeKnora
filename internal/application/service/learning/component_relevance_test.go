package learning

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func componentRelevanceFixture(t *testing.T) (interfaces.LearningScope, []interfaces.ComponentEntry, []*types.WikiPage, types.MemoryWikiMap, time.Time) {
	rows, pages := componentFixture(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	scope := interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB}
	entries := []interfaces.ComponentEntry{}
	for _, c := range rows {
		entries = append(entries, interfaces.ComponentEntry{ID: c.ID, Material: c.Definition, Available: true, State: projectComponent(c, nil, now)})
	}
	pages[0].UpdatedAt = now.Add(-time.Hour)
	m := types.MemoryWikiMap{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, NormalizedTopicKey: "events", TopicLabel: "事件", Slug: pages[0].Slug, Confidence: .8, UpdatedAt: now.Add(-time.Minute)}
	return scope, entries, pages, m, now
}

func TestComponentMemoryConservesOriginWeightAndNeverCreditsPerformance(t *testing.T) {
	scope, entries, pages, m, now := componentRelevanceFixture(t)
	before := append([]interfaces.ComponentEntry(nil), entries...)
	relevance := deriveComponentRelevance(scope, entries, pages, nil, []types.MemoryWikiMap{m, m, m}, now)
	require.Len(t, relevance, 2)
	for _, e := range entries {
		r := relevance[e.ID]
		require.Equal(t, .5, r.Weight)
		require.Equal(t, 1, r.Sources)
		require.Equal(t, 2, r.Links[0].SharedTargets)
		require.Contains(t, r.Links[0].Reason, "不代表已掌握")
	}
	require.Equal(t, before, entries, "association must not mutate performance, identity or preparation")
	entries[1].Available = false
	relevance = deriveComponentRelevance(scope, entries, pages, nil, []types.MemoryWikiMap{m}, now)
	require.Len(t, relevance, 1)
	require.Equal(t, 1.0, relevance[entries[0].ID].Weight)
}

func TestComponentMemoryFanoutMassDoesNotGrowWithCatalogueSize(t *testing.T) {
	scope, base, pages, m, now := componentRelevanceFixture(t)
	for _, n := range []int{1, 2, 10} {
		entries := make([]interfaces.ComponentEntry, n)
		for i := range entries {
			entries[i] = base[0]
			entries[i].ID = fmt.Sprintf("goal-%d", i)
		}
		r := deriveComponentRelevance(scope, entries, pages, nil, []types.MemoryWikiMap{m}, now)
		mass := 0.0
		for _, value := range r {
			mass += value.Weight
			require.Equal(t, n, value.Links[0].SharedTargets)
		}
		require.InDelta(t, 1.0, mass, 1e-12)
	}
}

func TestComponentMemoryRejectsWrongScopeStaleAndNonFiniteMappings(t *testing.T) {
	scope, entries, pages, valid, now := componentRelevanceFixture(t)
	for _, change := range []func(*types.MemoryWikiMap){
		func(m *types.MemoryWikiMap) { m.SubjectID = "web_user:bob" },
		func(m *types.MemoryWikiMap) { m.TenantID = 2 },
		func(m *types.MemoryWikiMap) { m.KnowledgeBaseID = "another-kb" },
		func(m *types.MemoryWikiMap) { m.Confidence = math.NaN() },
		func(m *types.MemoryWikiMap) { m.Confidence = math.Inf(1) },
		func(m *types.MemoryWikiMap) { m.UpdatedAt = now.Add(time.Minute) },
		func(m *types.MemoryWikiMap) { m.UpdatedAt = now.Add(-2 * time.Hour) },
		func(m *types.MemoryWikiMap) { m.Slug = "unknown" },
	} {
		m := valid
		change(&m)
		require.Empty(t, deriveComponentRelevance(scope, entries, pages, nil, []types.MemoryWikiMap{m}, now))
	}
	pages[0].TenantID = 2
	require.Empty(t, deriveComponentRelevance(scope, entries, pages, nil, []types.MemoryWikiMap{valid}, now))
}

func TestComponentDocumentFanoutDeduplicatesPathsAndCapsMultipleOrigins(t *testing.T) {
	scope, entries, pages, m, now := componentRelevanceFixture(t)
	pages[0].SourceRefs = []string{"doc-1|资料", "doc-1|重复路径"}
	a := types.MemoryDocAffinity{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, KnowledgeID: "doc-1", Title: "资料", Hits: 10, LastUsedAt: now}
	r := deriveComponentRelevance(scope, entries, pages, []types.MemoryDocAffinity{a, a}, nil, now)
	for _, v := range r {
		require.Equal(t, .5, v.Weight)
		require.Equal(t, 1, v.Sources)
	}
	r = deriveComponentRelevance(scope, entries, pages, []types.MemoryDocAffinity{a}, []types.MemoryWikiMap{m}, now)
	for _, v := range r {
		require.Equal(t, 1.0, v.Weight)
		require.Equal(t, 2, v.Sources)
	}
	for _, stamp := range []time.Time{now.Add(-91 * 24 * time.Hour), now.Add(time.Hour)} {
		a.LastUsedAt = stamp
		require.Empty(t, deriveComponentRelevance(scope, entries, pages, []types.MemoryDocAffinity{a}, nil, now))
	}
}

type componentMemoryErrorRepo struct{ *componentStubRepo }

func (r *componentMemoryErrorRepo) ListMapsBySubject(context.Context, uint64, string) ([]types.MemoryWikiMap, error) {
	return nil, errors.New("optional memory store unavailable")
}

func TestComponentMemoryFailureRetainsLearningAndDisclosesFallback(t *testing.T) {
	rows, pages := componentFixture(t)
	r := &componentMemoryErrorRepo{&componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}}
	s := NewService(r, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	v, err := s.ComponentView(collectorCtx(1, "alice"), testKB, 5, "")
	require.NoError(t, err)
	require.Equal(t, "unavailable", v.RelevanceStatus)
	require.NotEmpty(t, v.Steps)
	_, err = s.ComponentView(collectorCtx(1, "alice"), testKB, 5, "foreign-id")
	require.ErrorIs(t, err, ErrInvalidLearningRequest)
}

func TestComponentMemoryStopsWhenPersonalCollectionIsDisabled(t *testing.T) {
	rows, pages := componentFixture(t)
	r := &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}
	r.prefs["web_user:alice"] = &types.LearningSubjectPrefs{CollectDisabled: true}
	s := NewService(r, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	v, err := s.ComponentView(collectorCtx(1, "alice"), testKB, 5, "")
	require.NoError(t, err)
	require.Equal(t, "disabled", v.RelevanceStatus)
	require.NotEmpty(t, v.Steps)
	for _, c := range v.Components {
		require.Nil(t, c.Relevance)
	}
}
