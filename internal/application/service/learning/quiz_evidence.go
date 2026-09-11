package learning

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Never use the page-index cache for grading: its TTL is not a content version.
func (s *Service) currentQuizEvidence(ctx context.Context, tenant uint64, kb, slug string) (*types.WikiPage, map[string]string, string, error) {
	if s.wikiRepo == nil || s.chunkRepo == nil {
		return nil, nil, "", errors.New("learning: quiz evidence unavailable")
	}
	pages, err := s.wikiRepo.ListAll(ctx, kb)
	if err != nil {
		return nil, nil, "", err
	}
	for _, page := range pages {
		if page != nil && page.Slug == slug {
			excerpts, hash, err := s.pageQuizEvidence(ctx, tenant, page)
			return page, excerpts, hash, err
		}
	}
	return nil, nil, "", nil
}

func (s *Service) pageQuizEvidence(ctx context.Context, tenant uint64, page *types.WikiPage) (map[string]string, string, error) {
	if page == nil || (page.PageType != "entity" && page.PageType != "concept") || len(page.ChunkRefs) == 0 {
		return nil, "", nil
	}
	if s.chunkRepo == nil {
		return nil, "", errors.New("learning: quiz evidence unavailable")
	}
	ids := append([]string{}, page.ChunkRefs...)
	if len(ids) > quizEvidenceChunkCap {
		ids = ids[:quizEvidenceChunkCap]
	}
	chunks, err := s.chunkRepo.ListChunksByID(ctx, tenant, ids)
	if err != nil {
		return nil, "", err
	}
	excerpts := map[string]string{}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for _, c := range chunks {
		if c != nil && allowed[c.ID] && strings.TrimSpace(c.Content) != "" {
			excerpts[c.ID] = c.Content
		}
	}
	if len(excerpts) != len(allowed) || len(excerpts) == 0 {
		return nil, "", nil
	}
	return excerpts, quizEvidenceHash(page, excerpts), nil
}

func (s *Service) quizEvidenceMatches(ctx context.Context, item *types.LearningQuizItem) bool {
	if item == nil || item.EvidenceHash == "" {
		return false
	}
	// Serving/scoring statuses: legacy active and human-reviewed
	// published; drafts can be scored as practice, never strict evidence.
	if item.Status != types.LearningQuizStatusActive && item.Status != types.LearningQuizStatusPublished && item.Status != types.LearningQuizStatusDraft {
		return false
	}
	_, _, hash, err := s.currentQuizEvidence(ctx, item.TenantID, item.KnowledgeBaseID, item.Slug)
	return err == nil && hash != "" && hash == item.EvidenceHash
}

// SeedQuizEvidence exposes the live-evidence digest to operator tooling
// (cmd/learning-seed) so seeded drafts bind to the same hash the grader
// validates. Read-only; no HTTP route.
func SeedQuizEvidence(ctx context.Context, s *Service, tenant uint64, kb, slug string) (*types.WikiPage, map[string]string, string, error) {
	return s.currentQuizEvidence(ctx, tenant, kb, slug)
}
