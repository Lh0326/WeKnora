package learning

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Personal memory supplies candidates for relevance, not assertions about
// performance. An origin's influence is conserved across all matching goals.
func deriveComponentRelevance(scope interfaces.LearningScope, entries []interfaces.ComponentEntry, pages []*types.WikiPage, affinities []types.MemoryDocAffinity, mappings []types.MemoryWikiMap, now time.Time) map[string]*interfaces.ComponentRelevance {
	live := map[string]*types.WikiPage{}
	for _, p := range pages {
		if p != nil && p.TenantID == scope.TenantID && p.KnowledgeBaseID == scope.KnowledgeBaseID && p.Status != types.WikiPageStatusArchived {
			live[p.Slug] = p
		}
	}
	bySlug := map[string]map[string]bool{}
	for _, e := range entries {
		if !e.Available {
			continue
		}
		for _, src := range e.Material.Sources {
			if live[src.Slug] == nil {
				continue
			}
			if bySlug[src.Slug] == nil {
				bySlug[src.Slug] = map[string]bool{}
			}
			bySlug[src.Slug][e.ID] = true
		}
	}
	type origin struct {
		kind, label string
		targets     map[string]bool
	}
	origins := map[string]*origin{}
	add := func(key, kind, label, slug string) {
		if len(bySlug[slug]) == 0 {
			return
		}
		if label == "" {
			label = "相关资料"
		}
		o := origins[key]
		if o == nil {
			o = &origin{kind: kind, label: label, targets: map[string]bool{}}
			origins[key] = o
		} else if label < o.label {
			o.label = label
		}
		for id := range bySlug[slug] {
			o.targets[id] = true
		}
	}
	for _, m := range mappings {
		p := live[m.Slug]
		if p == nil || m.TenantID != scope.TenantID || m.SubjectID != scope.SubjectID || m.KnowledgeBaseID != scope.KnowledgeBaseID || strings.TrimSpace(m.NormalizedTopicKey) == "" || !(m.Confidence >= MapConfidenceMin && m.Confidence <= 1) || m.UpdatedAt.After(now) || m.CreatedAt.After(now) {
			continue
		}
		// A rewritten source invalidates an older topic adjudication. Legacy
		// rows without timestamps remain explicitly weak navigation candidates.
		if !m.UpdatedAt.IsZero() && p.UpdatedAt.After(m.UpdatedAt) {
			continue
		}
		label := m.TopicLabel
		if label == "" {
			label = m.NormalizedTopicKey
		}
		add("topic:"+m.NormalizedTopicKey, "memory_topic", label, m.Slug)
	}
	cutoff := now.Add(-90 * 24 * time.Hour)
	docSlugs := map[string][]string{}
	for slug, p := range live {
		if len(bySlug[slug]) == 0 {
			continue
		}
		for _, ref := range p.SourceRefs {
			doc := sourceRefDocID(ref)
			docSlugs[doc] = append(docSlugs[doc], slug)
		}
	}
	for _, a := range affinities {
		if a.TenantID != scope.TenantID || a.SubjectID != scope.SubjectID || a.KnowledgeBaseID != scope.KnowledgeBaseID || a.KnowledgeID == "" || a.Hits < types.MemoryDocAffinityMinHits || a.LastUsedAt.Before(cutoff) || a.LastUsedAt.After(now) {
			continue
		}
		for _, slug := range docSlugs[a.KnowledgeID] {
			add("document:"+a.KnowledgeID, "frequent_document", a.Title, slug)
		}
	}
	keys := make([]string, 0, len(origins))
	for key := range origins {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(origins[keys[i]].targets) != len(origins[keys[j]].targets) {
			return len(origins[keys[i]].targets) < len(origins[keys[j]].targets)
		}
		return keys[i] < keys[j]
	})
	out := map[string]*interfaces.ComponentRelevance{}
	for _, key := range keys {
		o := origins[key]
		n := len(o.targets)
		kind := "长期记忆主题"
		if o.kind == "frequent_document" {
			kind = "近期常用文档"
		}
		reason := fmt.Sprintf("%s「%s」经来源关联到 %d 个目标；仅影响推荐顺序，不代表已掌握。", kind, o.label, n)
		for id := range o.targets {
			if out[id] == nil {
				out[id] = &interfaces.ComponentRelevance{Links: []interfaces.ComponentRelevanceLink{}}
			}
			r := out[id]
			r.Weight = math.Min(1, r.Weight+1/float64(n))
			r.Sources++
			if len(r.Links) < 3 {
				r.Links = append(r.Links, interfaces.ComponentRelevanceLink{Kind: o.kind, Label: o.label, SharedTargets: n, Reason: reason})
			}
		}
	}
	return out
}

func (s *Service) componentRelevance(ctx context.Context, scope interfaces.LearningScope, entries []interfaces.ComponentEntry, pages []*types.WikiPage, now time.Time) (map[string]*interfaces.ComponentRelevance, string) {
	prefs, err := s.repo.GetSubjectPrefs(ctx, scope.SubjectID)
	if err != nil {
		return nil, "unavailable"
	}
	if prefs != nil && prefs.CollectDisabled {
		return nil, "disabled"
	}
	var affinities []types.MemoryDocAffinity
	var mappings []types.MemoryWikiMap
	if repo, ok := s.repo.(interfaces.LearningComponentMemoryRepository); ok {
		affinities, mappings, err = repo.ListComponentMemoryInputs(ctx, scope)
	} else {
		// Older/custom repositories cannot distinguish pre-reset memory; do
		// not reuse it after deletion until that boundary is implemented.
		epoch, eerr := s.repo.GetSubjectEpoch(ctx, scope.SubjectID)
		if eerr != nil {
			return nil, "unavailable"
		}
		if epoch > 0 {
			return nil, "no_signals"
		}
		affinities, err = s.repo.ListDocAffinityByScope(ctx, scope.TenantID, scope.SubjectID)
		if err == nil {
			mappings, err = s.repo.ListMapsBySubject(ctx, scope.TenantID, scope.SubjectID)
		}
	}
	if err != nil {
		return nil, "unavailable"
	}
	out := deriveComponentRelevance(scope, entries, pages, affinities, mappings, now)
	if len(out) == 0 {
		return out, "no_signals"
	}
	return out, "available"
}
