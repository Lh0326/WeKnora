package learning

import (
	"context"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/types"
)

var ErrInvalidLearningRequest = errors.New("learning: invalid request")

// Resolve the definition actually reviewed against the live material. Never
// stamp a newer objective version onto a question reviewed for an older one.
func (s *Service) verificationObjective(ctx context.Context, tenant uint64, kb, id, slug, contract string) (*types.LearningObjective, error) {
	rows, err := s.repo.ListObjectives(ctx, tenant, kb)
	if err != nil {
		return nil, err
	}
	for _, o := range rows {
		if o.ID != id {
			continue
		}
		if len(o.ContractParams) > 0 || o.Status != types.LearningObjectiveStatusPublished || o.Reviewer == "" || o.PublishedAt == nil || o.Slug != slug || o.ContractType != contract || o.ContractVersion != types.ObjectiveContractVersion || o.ContentVersion == "" {
			return nil, ErrReviewRejected
		}
		_, _, hash, err := s.currentQuizEvidence(ctx, tenant, kb, slug)
		if err != nil {
			return nil, err
		}
		if hash == "" || o.EvidenceHash == "" || o.EvidenceHash != hash {
			return nil, fmt.Errorf("%w: objective needs source review", ErrReviewRejected)
		}
		return &o, nil
	}
	return nil, ErrReviewRejected
}

// Material changes invalidate the applicability of evidence, not the historical
// trial itself. Source read failures must not masquerade as zero knowledge.
func (s *Service) currentObjectiveDefinitions(ctx context.Context, tenant uint64, kb string) ([]types.LearningObjective, error) {
	rows, err := s.repo.ListObjectives(ctx, tenant, kb)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	for i := range rows {
		o := &rows[i]
		if o.Status != types.LearningObjectiveStatusPublished {
			continue
		}
		h, ok := hashes[o.Slug]
		if !ok {
			_, _, h, err = s.currentQuizEvidence(ctx, tenant, kb, o.Slug)
			if err != nil {
				return nil, err
			}
			hashes[o.Slug] = h
		}
		if o.EvidenceHash == "" || o.Reviewer == "" || o.PublishedAt == nil {
			o.Status = types.LearningObjectiveStatusDraft
			continue
		}
		if h == "" || o.EvidenceHash != h {
			o.ContentVersion += ":source-changed"
		}
	}
	return rows, nil
}
