package learning

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// backfillMaxHits caps the saturation term min(hits,8)/8 of the backfill
// weight: affinity rows are document-grained counts, and beyond eight
// distinct documents touching one node the extra certainty is noise.
const backfillMaxHits = 8

// backfillAggregate is one pending backfill event, accumulated per
// (scope, slug) before appending.
type backfillAggregate struct {
	hits     int // distinct documents whose pages touched this slug
	lastUsed time.Time
}

// RunBackfill replays the memory subsystem's historical doc affinity into
// learning events, so a KB that was used before the feature shipped starts
// with a half-lit map instead of an empty one (§2.9 of the design doc).
//
// Idempotency is per (subject, KB) scope and lives in learning_backfill_marks
// — a tombstone that survives profile deletion. Reading it from the
// backfill_cite events themselves (the original design) made
// DeleteLearningDataBySubject erasable: the next startup re-derived the same
// history from the still-present affinity rows and resurrected the deleted
// profile. The mark is written BEFORE any event is appended, so a crash
// between the two can only under-light a scope (safe) and never duplicate
// folded weights. The weight is WeightAnswerCite × BackfillDiscount ×
// min(hits,8)/8: document-grained history is coarser than the chunk-grained
// live signal, so it is both discounted and saturating.
func (s *Service) RunBackfill(ctx context.Context) error {
	if !learningEnabled() {
		return nil
	}

	affinities, err := s.repo.ListDocAffinity(ctx)
	if err != nil {
		return err
	}

	// Group affinity rows by the learning scope they can light up.
	type scopeKey struct {
		tenant  uint64
		subject string
		kb      string
	}
	byScope := map[scopeKey][]types.MemoryDocAffinity{}
	for _, row := range affinities {
		if row.TenantID == 0 || row.SubjectID == "" || row.KnowledgeBaseID == "" || row.KnowledgeID == "" {
			continue
		}
		key := scopeKey{row.TenantID, row.SubjectID, row.KnowledgeBaseID}
		byScope[key] = append(byScope[key], row)
	}

	var failures []error
	appended := 0
	for key, rows := range byScope {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		scope := interfaces.LearningScope{TenantID: key.tenant, SubjectID: key.subject, KnowledgeBaseID: key.kb}

		epoch, err := s.repo.GetSubjectEpoch(ctx, key.subject)
		if err != nil {
			return err
		}
		// Historical memory is never replayed after this person deleted their profile.
		if epoch > 0 {
			continue
		}
		appendedBefore := appended
		err = s.repo.WithSubject(ctx, key.subject, epoch, true, func(ctx context.Context) error {

			// Orphan guard: a deleted KB's affinity rows linger (memory
			// subsystem scope) and its wiki_pages may too, so without this
			// check the backfill would resurrect the learning data the orphan
			// sweep just removed. kbRepo is nil only in narrow test fixtures.
			if s.kbRepo != nil {
				if kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, key.kb); err != nil || kb == nil {
					return nil
				}
			}

			index, err := s.pages.index(ctx, s.wikiRepo, key.kb)
			if err != nil {
				logger.Warnf(ctx, "learning: backfill page index failed (kb %s): %v", key.kb, err)
				return nil
			}
			if index.empty() {
				return nil
			}

			done, err := s.repo.BackfillDone(ctx, scope)
			if err != nil {
				logger.Warnf(ctx, "learning: backfill idempotency lookup failed (kb %s): %v", key.kb, err)
				return nil
			}
			if done {
				return nil
			}
			// Mark first: a mark without events can only under-light the scope,
			// while events without a mark would fold twice on the next run.
			if err := s.repo.MarkBackfillDone(ctx, scope); err != nil {
				logger.Warnf(ctx, "learning: backfill mark write failed (kb %s): %v", key.kb, err)
				return nil
			}

			// Aggregate: one doc's affinity lights every node its pages cite.
			agg := map[string]*backfillAggregate{}
			for _, row := range rows {
				cites := kbCitations{docIDs: map[string]bool{row.KnowledgeID: true}}
				for _, slug := range index.touchedSlugs(cites) {
					a := agg[slug]
					if a == nil {
						a = &backfillAggregate{}
						agg[slug] = a
					}
					a.hits++
					if row.LastUsedAt.After(a.lastUsed) {
						a.lastUsed = row.LastUsedAt
					}
				}
			}

			slugs := make([]string, 0, len(agg))
			for slug := range agg {
				slugs = append(slugs, slug)
			}
			sort.Strings(slugs)

			for _, slug := range slugs {
				a := agg[slug]
				occurredAt := a.lastUsed
				if occurredAt.IsZero() {
					occurredAt = time.Now()
				}
				weight := WeightAnswerCite * BackfillDiscount * (math.Min(float64(a.hits), backfillMaxHits) / backfillMaxHits)
				event := &types.LearningEvent{
					TenantID:        key.tenant,
					SubjectID:       key.subject,
					KnowledgeBaseID: key.kb,
					Slug:            slug,
					Type:            types.LearningEventBackfillCite,
					Weight:          weight,
					OccurredAt:      occurredAt,
				}
				if err := s.repo.AppendEvent(ctx, event); err != nil {
					logger.Warnf(ctx, "learning: backfill append failed (slug %s): %v", slug, err)
					return err
				}
				if err := s.foldOne(ctx, scope, slug, Event{Type: types.LearningEventBackfillCite, Weight: weight, OccurredAt: occurredAt}); err != nil {
					return err
				}
				appended++
			}
			return nil
		})
		if err != nil {
			appended = appendedBefore
			logger.Warnf(ctx, "learning: backfill transaction aborted: %v", err)
			if !errors.Is(err, interfaces.ErrLearningEpochAdvanced) && !errors.Is(err, interfaces.ErrLearningCollectionDisabled) {
				failures = append(failures, err)
			}
		}

	}
	if appended > 0 {
		logger.Infof(ctx, "learning: backfill appended %d events across %d scopes", appended, len(byScope))
	}
	return errors.Join(failures...)
}
