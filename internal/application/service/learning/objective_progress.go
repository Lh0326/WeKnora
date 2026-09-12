package learning

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"sort"
	"strings"
	"time"
)

// ObjectiveProgress keeps live-library, selected-goal and human-contact denominators separate.
func (s *Service) ObjectiveProgress(ctx context.Context, kbID string, goalIDs ...string) (*interfaces.ObjectiveProgressSummary, error) {
	view, err := s.ObjectiveView(ctx, kbID)
	if err != nil {
		return nil, err
	}
	pages, err := s.nodePages(ctx, kbID)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, p := range pages {
		if p != nil && p.Slug != "" {
			live[p.Slug] = true
		}
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	events, err := listEventWindow(ctx, s.repo, scope, time.Time{})
	if err != nil {
		return nil, err
	}
	marks, err := s.repo.ListSelfAssess(ctx, scope)
	if err != nil {
		return nil, err
	}
	contact := map[string]bool{}
	for _, ev := range events {
		if !live[ev.Slug] {
			continue
		}
		switch ev.Type {
		case types.LearningEventNodeRead, types.LearningEventWikiToolRead, types.LearningEventWikiDeepRead, types.LearningEventAnswerCite, types.LearningEventCrossRef, types.LearningEventReAsk:
			contact[ev.Slug] = true
		}
	}
	out := &interfaces.ObjectiveProgressSummary{TotalNodes: len(live), ContactNodes: len(contact)}
	for slug, mark := range marks {
		if live[slug] && mark.Direction != "" {
			out.SelfReportCount++
		}
	}
	selected := map[string]bool{}
	for _, id := range goalIDs {
		selected[id] = true
	}
	var versions []string
	for _, e := range view.Entries {
		if !live[e.Slug] || e.ObjectiveStatus != types.LearningObjectiveStatusPublished {
			continue
		}
		out.TotalObjectives++
		out.LegacyAttemptCount += e.Evidence.LegacyAttempts
		switch e.State {
		case types.ObjectiveStateVerified:
			out.VerifiedObjectives++
		case types.ObjectiveStateConflicting:
			out.ConflictingObjectives++
		case types.ObjectiveStateStale:
			out.StaleObjectives++
		case types.ObjectiveStatePartial:
			out.PartialObjectives++
		default:
			out.UnverifiedObjectives++
			if e.Evidence.Unknown {
				out.UntestedObjectives++
			}
		}
		if len(goalIDs) == 0 || selected[e.ObjectiveID] {
			delete(selected, e.ObjectiveID)
			out.GoalTotalObjectives++
			if e.State == types.ObjectiveStateVerified {
				out.GoalVerifiedObjectives++
			}
			versions = append(versions, e.ObjectiveID+":"+e.ContentVersion+":"+e.ContractVersion)
		}
	}
	if len(selected) > 0 {
		return nil, fmt.Errorf("%w: unknown or unpublished goal selection", ErrInvalidLearningRequest)
	}
	sort.Strings(versions)
	out.GoalSetVersion = fmt.Sprintf("goalset-v2-%x", sha256.Sum256([]byte(kbID+"\n"+strings.Join(versions, "\n"))))
	return out, nil
}
