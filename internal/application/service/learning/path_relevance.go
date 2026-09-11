package learning

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Relevance changes navigation order only. It never enters the evidence
// grader, lights a node, or satisfies a prerequisite.
type pathRelevance struct {
	Priority int
	Reason   PathReason
}

func derivePathRelevance(scope interfaces.LearningScope, pages []*types.WikiPage, affinities []types.MemoryDocAffinity, mappings []types.MemoryWikiMap, now time.Time) map[string]pathRelevance {
	out := map[string]pathRelevance{}
	live := map[string]bool{}
	for _, p := range pages {
		if p != nil {
			live[p.Slug] = true
		}
	}
	// Order before selecting an explanation; DB/map iteration order must
	// never make otherwise identical plans give different reasons.
	mappings = append([]types.MemoryWikiMap(nil), mappings...)
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].NormalizedTopicKey < mappings[j].NormalizedTopicKey })
	for _, m := range mappings {
		if m.TenantID != scope.TenantID || m.SubjectID != scope.SubjectID || m.KnowledgeBaseID != scope.KnowledgeBaseID || !live[m.Slug] || !(m.Confidence >= MapConfidenceMin && m.Confidence <= 1) {
			continue
		}
		if _, exists := out[m.Slug]; exists {
			continue
		}
		label := m.TopicLabel
		if label == "" {
			label = m.NormalizedTopicKey
		}
		out[m.Slug] = pathRelevance{Priority: 1, Reason: PathReason{Code: "plan_reason_memory_topic", Detail: fmt.Sprintf("与你的长期记忆主题「%s」相关，可优先了解；主题关联不代表已经掌握。", label), Evidence: []string{"memory_topic:" + m.NormalizedTopicKey}}}
	}
	docs := map[string]types.MemoryDocAffinity{}
	cutoff := now.Add(-90 * 24 * time.Hour)
	for _, a := range affinities {
		if a.TenantID != scope.TenantID || a.SubjectID != scope.SubjectID || a.KnowledgeBaseID != scope.KnowledgeBaseID || a.Hits < types.MemoryDocAffinityMinHits || a.LastUsedAt.Before(cutoff) || a.LastUsedAt.After(now) {
			continue
		}
		docs[a.KnowledgeID] = a
	}
	for _, p := range pages {
		if p == nil {
			continue
		}
		var chosen *types.MemoryDocAffinity
		for _, ref := range p.SourceRefs {
			a, ok := docs[sourceRefDocID(ref)]
			if !ok {
				continue
			}
			if chosen == nil || a.Hits > chosen.Hits || (a.Hits == chosen.Hits && a.KnowledgeID < chosen.KnowledgeID) {
				copy := a
				chosen = &copy
			}
		}
		if chosen != nil {
			label := chosen.Title
			if label == "" {
				label = "相关来源文档"
			}
			out[p.Slug] = pathRelevance{Priority: 2, Reason: PathReason{Code: "plan_reason_frequent_document", Detail: fmt.Sprintf("来自你近 90 天使用过的常用资料「%s」，与实际查阅需求更接近。", label), Evidence: []string{"document:" + chosen.KnowledgeID}}}
		}
	}
	return out
}

func (s *Service) pathRelevanceOf(ctx context.Context, scope interfaces.LearningScope, pages []*types.WikiPage, enabled *bool) (map[string]pathRelevance, string) {
	if enabled != nil && !*enabled {
		return nil, "disabled"
	}
	affinities, affinityErr := s.repo.ListDocAffinityByScope(ctx, scope.TenantID, scope.SubjectID)
	mappings, mappingErr := s.repo.ListMapsBySubject(ctx, scope.TenantID, scope.SubjectID)
	// The basic learning path remains available when optional memory data
	// fails; its status explicitly discloses that fallback to the user.
	if affinityErr != nil || mappingErr != nil {
		return nil, "unavailable"
	}
	relevance := derivePathRelevance(scope, pages, affinities, mappings, time.Now())
	if len(relevance) == 0 {
		return relevance, "no_signals"
	}
	return relevance, "available"
}
