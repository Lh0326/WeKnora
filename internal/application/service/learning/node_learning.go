package learning

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Content-based preference identity: renaming/moving a page does not erase a
// person's declaration, while a material body change requests confirmation.
func nodeContentVersion(p *types.WikiPage) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(p.Content)))
}

// A five-minute navigation cache cannot authorize a declaration against old
// material. Read the addressed source, and let the next overview see it too.
func (s *Service) currentLearningPage(ctx context.Context, kb, slug string) (*types.WikiPage, error) {
	p, err := s.wikiRepo.GetBySlug(ctx, kb, slug)
	s.pages.m.Delete(kb)
	if err != nil {
		return nil, err
	}
	if p == nil || p.Slug != slug || (p.KnowledgeBaseID != "" && p.KnowledgeBaseID != kb) || (p.PageType != "entity" && p.PageType != "concept") {
		return nil, ErrWikiReadTarget
	}
	return p, nil
}

func (s *Service) SetNodeState(ctx context.Context, kbID, slug, state string) error {
	kind := map[string]string{"read": types.LearningEventNodeRead, "known": types.LearningEventNodeKnown, "review": types.LearningEventNodeReview}[state]
	if kind == "" {
		return fmt.Errorf("%w: invalid node state", ErrInvalidLearningRequest)
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	return s.runSubject(ctx, scope.SubjectID, false, func(tx context.Context) error {
		page, err := s.currentLearningPage(tx, kbID, slug)
		if err != nil {
			return err
		}
		events, err := listEventWindow(tx, s.repo, scope, time.Time{})
		if err != nil {
			return err
		}
		var last *types.LearningEvent
		for i := range events {
			e := &events[i]
			if e.Slug == slug && isNodePreference(e.Type) && (last == nil || e.OccurredAt.After(last.OccurredAt)) {
				last = e
			}
		}
		version := nodeContentVersion(page)
		if last != nil && last.Type == kind && last.ContentVersion == version {
			return nil
		}
		// Update queue preference and immutable explicit event in one subject transaction.
		if err := s.repo.RemoveSkip(tx, scope, slug); err != nil {
			return err
		}
		now := time.Now()
		if state == "known" {
			if err := s.repo.AddSkip(tx, scope, slug, now); err != nil {
				return err
			}
		}
		return s.repo.AppendEvent(tx, &types.LearningEvent{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: kbID, Slug: slug, Type: kind, ContentVersion: version, OccurredAt: now, Weight: 0})
	})
}
func isNodePreference(kind string) bool {
	return kind == types.LearningEventNodeRead || kind == types.LearningEventNodeKnown || kind == types.LearningEventNodeReview || isRecallRating(kind)
}

func isRecallRating(kind string) bool {
	return kind == types.LearningEventReviewAgain || kind == types.LearningEventReviewHard || kind == types.LearningEventReviewGood || kind == types.LearningEventReviewEasy
}

// Every live page has a state, including pages without a question bank. These
// workflow states never feed the objective grader or masquerade as probabilities.
func deriveLearningNodes(pages []*types.WikiPage, objectives []interfaces.ObjectiveViewEntry, events []types.LearningEvent, skips map[string]time.Time) []interfaces.LearningNodeView {
	bySlug := map[string]*interfaces.LearningNodeView{}
	versions := map[string]string{}
	for _, p := range pages {
		if p == nil {
			continue
		}
		bySlug[p.Slug] = &interfaces.LearningNodeView{Slug: p.Slug, Title: p.Title, FolderID: p.FolderID, FolderName: strings.Join(p.CategoryPath, " / "), State: "unseen"}
		versions[p.Slug] = nodeContentVersion(p)
	}
	latest := map[string]types.LearningEvent{}
	reviewEvents := map[string][]types.LearningEvent{}
	for _, e := range events {
		n := bySlug[e.Slug]
		if n == nil {
			continue
		}
		if reviewAction(e.Type) != "" {
			reviewEvents[e.Slug] = append(reviewEvents[e.Slug], e)
		}
		switch e.Type {
		case types.LearningEventWikiToolRead, types.LearningEventWikiDeepRead, types.LearningEventNodeRead:
			n.Reads++
			if e.OccurredAt.After(n.LastReadAt) {
				n.LastReadAt = e.OccurredAt
			}
		case types.LearningEventAnswerCite, types.LearningEventCrossRef, types.LearningEventReAsk:
			n.Cites++
		}
		if isNodePreference(e.Type) {
			old, ok := latest[e.Slug]
			if !ok || e.OccurredAt.After(old.OccurredAt) || (e.OccurredAt.Equal(old.OccurredAt) && e.ID > old.ID) {
				latest[e.Slug] = e
			}
		}
	}
	trouble := map[string]bool{}
	for _, o := range objectives {
		n := bySlug[o.Slug]
		if n == nil || o.ObjectiveStatus != types.LearningObjectiveStatusPublished {
			continue
		}
		n.ObjectiveTotal++
		if o.State == types.ObjectiveStateVerified {
			n.ObjectiveVerified++
		}
		if o.State == types.ObjectiveStateConflicting || o.State == types.ObjectiveStateStale {
			trouble[o.Slug] = true
		}
	}
	for slug, n := range bySlug {
		n.Review = recallStatus(projectRecall(reviewEvents[slug]), versions[slug], time.Now())
		if n.Reads+n.Cites > 0 {
			n.State = "learning"
		}
		if at, ok := skips[slug]; ok {
			n.State = "self_known"
			n.DeclaredAt = at
		}
		if e, ok := latest[slug]; ok {
			n.DeclaredAt = e.OccurredAt
			switch e.Type {
			case types.LearningEventNodeKnown, types.LearningEventReviewHard, types.LearningEventReviewGood, types.LearningEventReviewEasy:
				n.State = "self_known"
				if e.ContentVersion != "" && e.ContentVersion != versions[slug] {
					n.State = "review"
					n.Updated = true
				}
			case types.LearningEventNodeReview, types.LearningEventReviewAgain:
				n.State = "review"
			case types.LearningEventNodeRead:
				n.State = "learning"
			}
		}
		if n.ObjectiveTotal > 0 && n.ObjectiveVerified == n.ObjectiveTotal && n.State != "review" {
			n.State = "verified"
		}
		// A current explicit declaration controls the personal learning queue.
		// Conflicting objective evidence remains separate and is never erased.
		if trouble[slug] && n.State != "self_known" {
			n.State = "review"
		}
	}
	out := make([]interfaces.LearningNodeView, 0, len(bySlug))
	for _, n := range bySlug {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}
