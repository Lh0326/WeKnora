package learning

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"sort"
	"time"
)

// FreezeAssessment captures this authenticated participant's state before any holdout is presented.
// Personal writes serialize on the same subject fence as grading/deletion. No labels enter this API.
func (s *Service) FreezeAssessment(ctx context.Context, kbID string) (*interfaces.AssessmentFreeze, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	epoch, err := s.operationEpoch(ctx, scope.SubjectID)
	if err != nil {
		return nil, err
	}
	var out *interfaces.AssessmentFreeze
	err = s.repo.WithSubject(ctx, scope.SubjectID, epoch, false, func(tx context.Context) error {
		view, e := s.ObjectiveView(tx, kbID)
		if e != nil {
			return e
		}
		pages, e := s.nodePages(tx, kbID)
		if e != nil {
			return e
		}
		live := map[string]bool{}
		for _, p := range pages {
			if p != nil {
				live[p.Slug] = true
			}
		}
		attempts, e := s.repo.ListAttempts(tx, scope, "")
		if e != nil {
			return e
		}
		tasks, e := s.repo.ListTaskAttempts(tx, scope)
		if e != nil {
			return e
		}
		families := map[string]bool{}
		for _, a := range attempts {
			if a.FamilyID != "" {
				families[a.FamilyID] = true
			}
		}
		for _, a := range tasks {
			if a.FamilyID != "" {
				families[a.FamilyID] = true
			}
		}
		out = &interfaces.AssessmentFreeze{SnapshotID: uuid.NewString(), KnowledgeBaseID: kbID, PolicyVersion: types.ObjectiveProjectionVersion, GoalStatesBefore: map[string]string{}, ObjectiveVersions: map[string]string{}, EvidenceFamiliesBefore: []string{}}
		out.LearningModelVersion = NodeEstimateVersion
		out.NodeEstimates = map[string]*interfaces.LearningEstimate{}
		for _, node := range view.Nodes {
			if node.Estimate != nil {
				out.NodeEstimates[node.Slug] = node.Estimate
			}
		}
		for _, o := range view.Entries {
			if o.ObjectiveStatus == types.LearningObjectiveStatusPublished && live[o.Slug] {
				out.GoalStatesBefore[o.ObjectiveID] = o.State
				out.ObjectiveVersions[o.ObjectiveID] = o.ContentVersion
			}
		}
		for f := range families {
			out.EvidenceFamiliesBefore = append(out.EvidenceFamiliesBefore, f)
		}
		sort.Strings(out.EvidenceFamiliesBefore)
		out.StateAsOf = time.Now().UTC()
		return nil
	})
	return out, err
}
