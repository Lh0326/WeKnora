package learning

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Stage-1 separated evidence profile (01 §5.1/§5.2): five independent
// dimensions per (node, objective). The objective_evidence dimension is a
// PURE derivation from attempt facts frozen at answer time; exposure,
// self-report, recency and path status are assembled alongside and NEVER
// feed the evidence derivation — reading a hundred times or declaring
// "已掌握" moves its own dimension and nothing else.

// ObjectiveView derives the caller's separated profile for one KB. Scope
// comes from server authentication alone (tenant/subject), the same
// containment story as every learning read.
func (s *Service) ObjectiveView(ctx context.Context, kbID string) (*interfaces.ObjectiveViewResponse, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	objectives, err := s.currentObjectiveDefinitions(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	attempts, err := s.repo.ListAttempts(ctx, scope, "")
	if err != nil {
		return nil, err
	}
	// Exposure facts: the subject's whole event history for this KB,
	// paginated — contact is a fact stream, not a score.
	events, err := listEventWindow(ctx, s.repo, scope, time.Time{})
	if err != nil {
		return nil, err
	}
	taskAttempts, err := s.repo.ListTaskAttempts(ctx, scope)
	if err != nil {
		return nil, err
	}
	selfAssess, err := s.repo.ListSelfAssess(ctx, scope)
	if err != nil {
		return nil, err
	}
	skips, err := s.repo.ListSkips(ctx, scope)
	if err != nil {
		return nil, err
	}

	resp := &interfaces.ObjectiveViewResponse{
		Entries:           []interfaces.ObjectiveViewEntry{},
		ProjectionVersion: types.ObjectiveProjectionVersion,
		LegacyByNode:      map[string]int{},
	}
	// Display-only legacy association: attempts without frozen objective
	// metadata map to their item's CURRENT objective so per-objective
	// legacy counts stay visible. The derivation never converts them into
	// strict evidence.
	legacyByItem := map[string]string{}
	if items, err := s.repo.ListQuizItemsByKB(ctx, scope.TenantID, scope.KnowledgeBaseID); err == nil {
		for _, it := range items {
			if it.ObjectiveID != "" {
				legacyByItem[it.ID] = it.ObjectiveID
			}
		}
	} else {
		logger.Warnf(ctx, "learning: objective legacy item map failed (kb %s): %v", kbID, err)
	}
	definedObjectives := map[string]bool{}
	for _, o := range objectives {
		definedObjectives[o.ID] = true
	}
	// Metadata-less attempts (or attempts pointing at objectives that no
	// longer exist) stay visible per node — legacy_unverified, never
	// strict evidence.
	for _, a := range attempts {
		if a.ObjectiveID == "" || !definedObjectives[a.ObjectiveID] {
			node := a.OriginalSlug
			if node == "" {
				node = a.Slug
			}
			resp.LegacyByNode[node]++
		}
	}

	facts := indexObjectiveFacts(attempts, legacyByItem, taskAttempts)
	eventsBySlug := map[string][]types.LearningEvent{}
	for _, ev := range events {
		eventsBySlug[ev.Slug] = append(eventsBySlug[ev.Slug], ev)
	}
	for _, o := range objectives {
		entry := deriveObjectiveEntry(o, facts.quiz[o.ID], legacyByItem, facts.tasks[o.ID], eventsBySlug[o.Slug], selfAssess, skips)
		resp.Entries = append(resp.Entries, entry)
	}
	pages, err := s.nodePages(ctx, kbID)
	if err != nil {
		return nil, err
	}
	resp.Nodes = deriveLearningNodes(pages, resp.Entries, events, skips)
	attachNodeEstimates(resp.Nodes, deriveNodeEstimates(pages, events, attempts, taskAttempts, objectives, time.Now()))
	// Folder names must come from the actual wiki directory, not optional
	// generated category metadata. One batch lookup serves the whole KB.
	if folders, folderErr := s.wikiRepo.ListAllFolders(ctx, kbID); folderErr == nil {
		names := map[string]string{}
		for _, folder := range folders {
			if folder != nil {
				names[folder.ID] = folder.Name
			}
		}
		for i := range resp.Nodes {
			if name := names[resp.Nodes[i].FolderID]; name != "" {
				resp.Nodes[i].FolderName = name
			}
		}
	}
	return resp, nil
}

// deriveObjectiveEntry assembles one row's five dimensions from the
// caller's facts. Pure in (objective, attempts, events, marks, skips);
// deterministic output ordering comes from the repository's ordering.
func deriveObjectiveEntry(
	o types.LearningObjective,
	attempts []types.LearningQuizAttempt,
	legacyByItem map[string]string,
	taskAttempts []types.LearningTaskAttempt,
	events []types.LearningEvent,
	selfAssess map[string]interfaces.SelfAssessMark,
	skips map[string]time.Time,
) interfaces.ObjectiveViewEntry {
	d := DeriveObjectiveState(o, attempts, legacyByItem, taskAttempts)

	entry := interfaces.ObjectiveViewEntry{
		Slug:            o.Slug,
		ObjectiveID:     o.ID,
		Title:           o.Title,
		Behavior:        o.Behavior,
		CapabilityType:  o.CapabilityType,
		ContractType:    o.ContractType,
		ContractVersion: o.ContractVersion,
		ContentVersion:  o.ContentVersion,
		ObjectiveStatus: o.Status,
		State:           d.State,
		Evidence:        evidenceToView(d.Evidence),
	}
	// Unreviewed or retired definitions never certify ability: their
	// evidence stays visible, the state reads unverified.
	if o.Status != types.LearningObjectiveStatusPublished {
		entry.State = types.ObjectiveStateUnverified
	}

	// Exposure: contact facts only (human reads, citations, agent reads).
	for _, ev := range events {
		node := ev.Slug
		if node != o.Slug {
			continue
		}
		switch ev.Type {
		case types.LearningEventWikiToolRead, types.LearningEventWikiDeepRead, types.LearningEventNodeRead:
			entry.Exposure.Reads++
		case types.LearningEventAnswerCite, types.LearningEventCrossRef, types.LearningEventReAsk:
			entry.Exposure.Cites++
		case types.LearningEventAgentRead:
			entry.Exposure.AgentReads++
		default:
			continue
		}
		if ev.OccurredAt.After(entry.Exposure.LastAt) {
			entry.Exposure.LastAt = ev.OccurredAt
		}
	}

	// Self-report: the user's own claim, kept exactly as claimed.
	if mark, ok := selfAssess[o.Slug]; ok {
		entry.SelfReport.Direction = mark.Direction
		entry.SelfReport.At = mark.At
	}
	if _, skipped := skips[o.Slug]; skipped {
		entry.SelfReport.Skipped = true
	}

	// Recency: timestamps only. Verified facts are never demoted by time
	// passage here — that is a review SUGGESTION dimension elsewhere.
	entry.Recency.LastVerifiedAt = d.Evidence.LastPassAt
	entry.Recency.LastEvidenceAt = latestTime(d.Evidence.LastPassAt, d.Evidence.LastFailureAt)
	entry.Recency.LastExposureAt = entry.Exposure.LastAt

	// Path status: minimal stage-1 semantics — the user's own standing
	// choices, never an inferred gate. Prerequisite-aware readiness is a
	// guidance-layer concern (stage 4), deliberately absent here.
	entry.PathStatus = "available"
	if entry.SelfReport.Skipped {
		entry.PathStatus = "user_retired"
	} else if entry.SelfReport.Direction == "up" {
		entry.PathStatus = "challenge_pending"
	}
	return entry
}

func evidenceToView(e ObjectiveEvidence) interfaces.ObjectiveEvidenceView {
	v := interfaces.ObjectiveEvidenceView{
		FamiliesPassed:         e.FamiliesPassed,
		FamiliesPassedHistoric: e.FamiliesPassedHistoric,
		EligiblePasses:         e.EligiblePasses,
		EligibleFailures:       e.EligibleFailures,
		LastPassAt:             e.LastPassAt,
		LastFailureAt:          e.LastFailureAt,
		ContractMetAt:          e.ContractMetAt,
		StalePasses:            e.StalePasses,
		LegacyAttempts:         e.LegacyAttempts,
		Source:                 e.Source,
		Unknown:                e.Unknown,
	}
	if v.FamiliesPassed == nil {
		v.FamiliesPassed = []string{}
	}
	if v.FamiliesPassedHistoric == nil {
		v.FamiliesPassedHistoric = []string{}
	}
	return v
}

func latestTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// objectiveExportRows derives the export payload's objective section for
// one subject-scoped attempt set: definitions plus current derived state.
// Legacy metadata never fabricates strict states (DeriveObjectiveState
// guarantees that); rows are deterministic for the same snapshot.
func objectiveExportRows(objectives []types.LearningObjective, attempts []types.LearningQuizAttempt, legacyByItem map[string]string, taskAttempts []types.LearningTaskAttempt) []interfaces.ObjectiveExportRow {
	var out []interfaces.ObjectiveExportRow
	facts := indexObjectiveFacts(attempts, legacyByItem, taskAttempts)
	for _, o := range objectives {
		d := DeriveObjectiveState(o, facts.quiz[o.ID], legacyByItem, facts.tasks[o.ID])
		state := d.State
		if o.Status != types.LearningObjectiveStatusPublished {
			state = types.ObjectiveStateUnverified
		}
		out = append(out, interfaces.ObjectiveExportRow{
			LearningObjective: o,
			State:             state,
			Evidence:          evidenceToView(d.Evidence),
			Source:            d.Evidence.Source,
		})
	}
	return out
}
