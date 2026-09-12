package learning

import (
	"context"
	"encoding/json"
	"fmt"
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
		return s.freezeAssessmentComponents(tx, scope, out)
	})
	if err != nil {
		return nil, err
	}
	return out, err
}

// Only personal learning facts and shared material are read here. In particular,
// ComponentView would perform unrelated relevance reads and use a different
// projection time. All personal reads instead share the caller's subject fence.
func (s *Service) freezeAssessmentComponents(ctx context.Context, scope interfaces.LearningScope, out *interfaces.AssessmentFreeze) error {
	out.ComponentModelVersion = componentModelVersion
	out.ComponentStatesBefore = map[string]string{}
	out.ComponentVersions = map[string]string{}
	out.ComponentAvailable = map[string]bool{}
	out.ComponentEvidenceFamiliesBefore = []string{}
	out.ComponentCheckIDsBefore = map[string][]string{}
	out.ComponentExposureComplete = false
	out.ComponentExposureNote = "Submitted checks only, including practice and previous material versions. Displayed-but-unanswered checks and exposure outside this event history are unknown; verify them before assigning holdouts."
	r, ok := s.repo.(interfaces.LearningComponentRepository)
	if !ok {
		return nil // Existing page-only repository implementations remain supported.
	}
	rows, err := r.ListComponents(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return err
	}
	var pages []*types.WikiPage
	if len(rows) > 0 {
		sourceRepo, ok := s.repo.(interfaces.LearningComponentAssessmentRepository)
		if !ok {
			return fmt.Errorf("component assessment source repository unavailable")
		}
		pages, err = sourceRepo.ListComponentAssessmentSources(ctx, scope.TenantID, scope.KnowledgeBaseID)
		if err != nil {
			return err
		}
	}
	events, err := listEventWindow(ctx, s.repo, scope, time.Time{})
	if err != nil {
		return err
	}
	// Personal writers are serialized by the subject fence. Shared material
	// reads use the same connection but do not raise database isolation; under
	// READ COMMITTED these are statement snapshots, not a global content lock.
	out.StateAsOf = time.Now().UTC()
	personal := make([]types.LearningEvent, 0, len(events))
	families := map[string]bool{}
	checks := map[string]map[string]bool{}
	for _, ev := range events {
		if ev.TenantID != scope.TenantID || ev.SubjectID != scope.SubjectID || ev.KnowledgeBaseID != scope.KnowledgeBaseID || ev.OccurredAt.After(out.StateAsOf) {
			continue
		}
		personal = append(personal, ev)
		if ev.Type != types.LearningEventComponent {
			continue
		}
		var fact componentFact
		if json.Unmarshal(ev.ReviewData, &fact) != nil || fact.Action != "check" || fact.ComponentID == "" || ev.Slug != "kc:"+fact.ComponentID {
			continue
		}
		// Exposure cannot be erased by revising material or marking an attempt
		// ineligible: the learner has still seen the submitted check.
		if fact.Family != "" {
			families[fact.Family] = true
		}
		if fact.CheckID != "" {
			if checks[fact.ComponentID] == nil {
				checks[fact.ComponentID] = map[string]bool{}
			}
			checks[fact.ComponentID][fact.CheckID] = true
		}
	}
	for _, row := range rows {
		if row.TenantID != scope.TenantID || row.KnowledgeBaseID != scope.KnowledgeBaseID {
			continue
		}
		out.ComponentStatesBefore[row.ID] = projectComponent(row, personal, out.StateAsOf).Level
		out.ComponentVersions[row.ID] = row.Version
		out.ComponentAvailable[row.ID] = componentAvailable(row, pages)
	}
	for family := range families {
		out.ComponentEvidenceFamiliesBefore = append(out.ComponentEvidenceFamiliesBefore, family)
	}
	sort.Strings(out.ComponentEvidenceFamiliesBefore)
	for componentID, ids := range checks {
		out.ComponentCheckIDsBefore[componentID] = []string{}
		for id := range ids {
			out.ComponentCheckIDsBefore[componentID] = append(out.ComponentCheckIDsBefore[componentID], id)
		}
		sort.Strings(out.ComponentCheckIDsBefore[componentID])
	}
	return nil
}
