package learning

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Stage 3 service layer: assembles the pure planner's input from the
// caller's server-side facts and exposes the cold-start + short-path API.

// ColdStartRequest is the first-entry payload: goal set, depth, time
// budget, and the VOLUNTARY fast-track challenge.
type ColdStartRequest struct {
	GoalObjectives []string `json:"goal_objectives"`
	Depth          string   `json:"depth"` // aware | operate | analyze
	TimeBudgetMin  int      `json:"time_budget_minutes"`
	GoalSlugs      []string `json:"goal_slugs"`
	FastTrack      bool     `json:"fast_track"`
	UseMemory      *bool    `json:"use_memory,omitempty"`
}

// ShortPath derives the caller's 3–5 step path (pure planner under the
// hood; deterministic for the same snapshot + policy version).
func (s *Service) ShortPath(ctx context.Context, kbID string, payload interfaces.ColdStartRequestPayload) (*interfaces.LearningPathPlan, error) {
	if payload.Depth != "" && payload.Depth != "aware" && payload.Depth != "operate" && payload.Depth != "analyze" {
		return nil, fmt.Errorf("%w: invalid learning depth", ErrInvalidLearningRequest)
	}
	if payload.TimeBudgetMin < 0 || payload.TimeBudgetMin > 120 {
		return nil, fmt.Errorf("%w: time budget must be between 1 and 120 minutes", ErrInvalidLearningRequest)
	}
	req := ColdStartRequest{
		GoalObjectives: payload.GoalObjectives, Depth: payload.Depth,
		TimeBudgetMin: payload.TimeBudgetMin, FastTrack: payload.FastTrack, GoalSlugs: payload.GoalSlugs,
		UseMemory: payload.UseMemory,
	}
	in, err := s.planInputOf(ctx, kbID, req)
	if err != nil {
		return nil, err
	}
	in.ExcludedSlugs = map[string]bool{}
	for _, slug := range payload.ExcludedSlugs {
		in.ExcludedSlugs[slug] = true
	}
	plan := planShortPath(in)
	return planToTransport(plan), nil
}

// planToTransport maps the internal plan onto the transport DTO.
func planToTransport(p PathPlan) *interfaces.LearningPathPlan {
	out := &interfaces.LearningPathPlan{PolicyVersion: p.PolicyVersion, Degrade: p.Degrade, Personalization: p.Personalization, Steps: []interfaces.LearningPathStep{}}
	for _, st := range p.Steps {
		ts := interfaces.LearningPathStep{
			ID: st.ID, Completed: st.Completed, Slug: st.Slug, Title: st.Title, Objective: st.Objective, Action: st.Action,
			Minutes: st.Minutes, DoneWhen: st.DoneWhen, Eligibility: st.Eligibility,
		}
		ts.Reason.Code = st.Reason.Code
		ts.Reason.Detail = st.Reason.Detail
		ts.Reason.Evidence = st.Reason.Evidence
		for _, r := range st.Requires {
			if ts.Requires != "" {
				ts.Requires += "; "
			}
			ts.Requires += r
		}
		for _, n := range st.Next {
			if ts.Next != "" {
				ts.Next += "; "
			}
			ts.Next += n.StepID + " 当 " + n.When
		}
		out.Steps = append(out.Steps, ts)
	}
	return out
}

func (s *Service) planInputOf(ctx context.Context, kbID string, req ColdStartRequest) (planInput, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return planInput{}, err
	}
	// Validity filter first: only live node pages enter the universe.
	// (Direct entity/concept read like the reconcile pass; the ref index
	// carries no titles.)
	pageList, err := s.nodePages(ctx, kbID)
	if err != nil {
		return planInput{}, err
	}
	pages := map[string]string{}
	for _, p := range pageList {
		if p != nil && p.Slug != "" {
			pages[p.Slug] = p.Title
			if p.Title == "" {
				pages[p.Slug] = p.Slug
			}
		}
	}

	objectives, objErr := s.currentObjectiveDefinitions(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if objErr != nil {
		return planInput{}, objErr
	}
	objBySlug := map[string][]string{}
	states := map[string]ObjectiveDerivation{}
	pubByID := map[string]types.LearningObjective{}
	for _, o := range objectives {
		// Only PUBLISHED objectives participate in strict verification
		// (draft/retired definitions never gate or advance the path).
		if o.Status != types.LearningObjectiveStatusPublished {
			continue
		}
		if pages[o.Slug] == "" {
			continue // objective on a dead page: skipped deterministically
		}
		pubByID[o.ID] = o
		objBySlug[o.Slug] = append(objBySlug[o.Slug], o.ID)
	}

	for _, id := range req.GoalObjectives {
		if _, ok := pubByID[id]; !ok {
			return planInput{}, fmt.Errorf("%w: unknown or unpublished goal %q", ErrInvalidLearningRequest, id)
		}
	}
	// Evidence derivations from frozen trial facts (stage-1/2 chains).
	attempts, err := s.repo.ListAttempts(ctx, scope, "")
	if err != nil {
		return planInput{}, err
	}
	taskAttempts, err := s.repo.ListTaskAttempts(ctx, scope)
	if err != nil {
		return planInput{}, err
	}
	items, err := s.repo.ListQuizItemsByKB(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return planInput{}, err
	}
	legacyByItem := map[string]string{}
	for _, item := range items {
		if item.ObjectiveID != "" {
			legacyByItem[item.ID] = item.ObjectiveID
		}
	}

	facts := indexObjectiveFacts(attempts, legacyByItem, taskAttempts)
	for id := range pubByID {
		states[id] = DeriveObjectiveState(pubByID[id], facts.quiz[id], legacyByItem, facts.tasks[id])
	}

	// Strict edges: only REVIEWED strict prerequisites constrain default
	// eligibility. The stored relation vocabulary gains the layered
	// reading: rows whose Source is human-reviewed count as strict;
	// heuristic/LLM edges degrade to suggested (never gate).
	strict := map[string][]string{}
	edges, err := s.repo.ListEdges(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return planInput{}, err
	}
	for _, e := range edges {
		if e.Relation != types.LearningEdgePrerequisite || e.Source != types.LearningEdgeSourceManual || e.FromSlug == e.ToSlug {
			continue
		}
		if pages[e.FromSlug] == "" || pages[e.ToSlug] == "" {
			continue // dangling endpoints: deterministic skip
		}
		strict[e.FromSlug] = append(strict[e.FromSlug], e.ToSlug)
	}
	for from := range strict {
		dst := strict[from]
		sortStrings(dst)
		strict[from] = dst
	}

	// Skips are PATH CHOICES: they release default gating and map to
	// user_retired; they never turn into verified evidence.
	skipMarks, err := s.repo.ListSkips(ctx, scope)
	if err != nil {
		return planInput{}, err
	}
	events, err := listEventWindow(ctx, s.repo, scope, time.Time{})
	if err != nil {
		return planInput{}, err
	}
	nodeEntries := make([]interfaces.ObjectiveViewEntry, 0, len(pubByID))
	for id, o := range pubByID {
		nodeEntries = append(nodeEntries, interfaces.ObjectiveViewEntry{Slug: o.Slug, ObjectiveStatus: o.Status, State: states[id].State})
	}
	nodes := deriveLearningNodes(pageList, nodeEntries, events, skipMarks)
	skips, exposure, nodeReview, nodeVerified := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	lastExposure := map[string]time.Time{}
	recallDue := map[string]PathReason{}
	recallDueAt := map[string]time.Time{}
	for _, n := range nodes {
		skips[n.Slug] = n.State == "self_known"
		exposure[n.Slug] = n.Reads+n.Cites > 0 || n.State == "verified"
		nodeReview[n.Slug] = n.State == "review"
		nodeVerified[n.Slug] = n.State == "verified"
		lastExposure[n.Slug] = n.LastReadAt
		if n.Review != nil && n.Review.Active && n.Review.Due {
			recallDueAt[n.Slug] = n.Review.DueAt
			detail := "你已开启间隔复习，本次已到期；先尝试回忆，再核对材料并反馈难度。"
			if n.Review.ContentChanged {
				detail = "你加入复习的材料已更新，核对新内容后重新安排复习。"
			}
			recallDue[n.Slug] = PathReason{Code: "plan_reason_scheduled_recall", Detail: detail, Evidence: []string{n.Review.Revision}}
		}
	}
	if len(req.GoalObjectives) > 0 {
		goalPages := map[string]bool{}
		for _, id := range req.GoalObjectives {
			goalPages[pubByID[id].Slug] = true
		}
		for slug := range recallDue {
			if !goalPages[slug] {
				delete(recallDue, slug)
				delete(recallDueAt, slug)
			}
		}
	}
	for _, ev := range events {
		if ev.Type == types.LearningEventAnswerCite || ev.Type == types.LearningEventCrossRef {
			if ev.OccurredAt.After(lastExposure[ev.Slug]) {
				lastExposure[ev.Slug] = ev.OccurredAt
			}
		}
	}
	pageScope := map[string]bool{}
	for _, slug := range req.GoalSlugs {
		if _, ok := pages[slug]; !ok {
			return planInput{}, fmt.Errorf("%w: unknown goal page", ErrInvalidLearningRequest)
		}
		pageScope[slug] = true
	}
	pageMinutes, pageFolders := map[string]int{}, map[string]string{}
	for _, p := range pageList {
		if p == nil {
			continue
		}
		minutes := 1 + len([]rune(p.Content))/350
		if minutes > 8 {
			minutes = 8
		}
		pageMinutes[p.Slug] = minutes
		pageFolders[p.Slug] = p.FolderID
	}
	// Continuity anchors: human strong behaviour, newest first (the why.go
	// recentAnchors vocabulary, bounded).
	recentEvents := make([]types.LearningEvent, 0)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, ev := range events {
		if !ev.OccurredAt.Before(cutoff) {
			recentEvents = append(recentEvents, ev)
		}
	}
	recent := recentAnchorsOf(recentEvents)

	// Objective evidence does not expire merely because two weeks passed.
	// Voluntary recall has its own per-person schedule, separate from grading.
	failedRecently := map[string]bool{}
	for id, d := range states {
		if d.Evidence.EligibleFailures >= 2 && d.Evidence.LastFailureAt.After(d.Evidence.LastPassAt) {
			failedRecently[id] = true
			if lastExposure[pubByID[id].Slug].Before(d.Evidence.LastFailureAt) {
				exposure[pubByID[id].Slug] = false
			}
		}
	}

	// Batch availability checks from this request's already source-validated definitions.
	// Avoid taking every quiz/task, which re-reads the full wiki and objective bank per item.
	hasBank := map[string]bool{}
	for i := range items {
		item := &items[i]
		o, ok := pubByID[item.ObjectiveID]
		if !ok || o.ContractType != types.ObjectiveContractConceptTwoFamily || item.Slug != o.Slug || item.ObjectiveVersion != o.ContentVersion || item.EvidenceHash != o.EvidenceHash || item.ScorerVersion != types.MCQScorerVersion {
			continue
		}
		prior := 0
		if facts.quizSeen(*item) {
			prior = 1
		}
		if eligible, _ := strictQuizEligibility(item, item.AssistanceMode, prior); eligible {
			hasBank[o.ID] = true
		}
	}
	tasks, err := s.repo.ListTasks(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return planInput{}, err
	}
	for i := range tasks {
		task := &tasks[i]
		o, ok := pubByID[task.ObjectiveID]
		if !ok || o.ContractType != types.ObjectiveContractTaskChecks || task.Slug != o.Slug || task.ObjectiveVersion != o.ContentVersion || task.EvidenceHash != o.EvidenceHash || task.ScorerVersion != types.TaskScorerVersion || task.Status != types.LearningQuizStatusPublished || task.Reviewer == "" || task.PublishedAt == nil || task.FamilyID == "" || validateTaskShape(task) != nil {
			continue
		}
		if !facts.taskSeen(*task) {
			hasBank[o.ID] = true
		}
	}
	docOrder := s.docOrderRanks(ctx, kbID)
	relevance, personalization := s.pathRelevanceOf(ctx, scope, pageList, req.UseMemory)

	return planInput{
		TenantID: scope.TenantID, KBID: scope.KnowledgeBaseID,
		IncludeExploration: true, PageScope: pageScope, PageMinutes: pageMinutes, PageFolders: pageFolders, NodeReview: nodeReview, NodeVerified: nodeVerified,
		GoalsResolved: true, HasVerification: hasBank, Pages: pages, Objectives: pubByID, ObjBySlug: objBySlug, States: states,
		StrictEdges: strict, DocOrder: docOrder, Skips: skips, Exposure: exposure,
		Recent: recent, UserGoals: scopedGoalIDs(req, pubByID, pageScope),
		TimeBudgetMinutes: req.TimeBudgetMin, FastTrack: req.FastTrack,
		RecallDue: recallDue, RecallDueAt: recallDueAt, FailedRecently: failedRecently,
		Relevance: relevance, Personalization: personalization,
	}, nil
}

// goalIDsOf normalises the requested goal set to published objectives
// that exist (unknown ids are dropped deterministically).
func goalIDsOf(req ColdStartRequest, published map[string]types.LearningObjective) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range req.GoalObjectives {
		if _, ok := published[g]; ok && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	if len(req.GoalObjectives) == 0 {
		for id, o := range published {
			if req.Depth == "aware" && o.CapabilityType != "concept" {
				continue
			}
			if req.Depth == "operate" && o.CapabilityType == "analysis" {
				continue
			}
			out = append(out, id)
		}
	}
	sortStrings(out)
	return out
}

// recentAnchorsOf extracts strong-behaviour anchors (why.go vocabulary).
func recentAnchorsOf(events []types.LearningEvent) []RecentNode {
	newest := map[string]time.Time{}
	for _, ev := range events {
		if !types.IsHumanLearningEvent(ev.Type) {
			continue
		}
		switch ev.Type {
		case types.LearningEventQuizCorrect, types.LearningEventQuizWrong,
			types.LearningEventNodeRead, types.LearningEventNodeKnown, types.LearningEventNodeReview,
			types.LearningEventWikiToolRead, types.LearningEventWikiDeepRead,
			types.LearningEventAnswerCite, types.LearningEventCrossRef:
		default:
			continue
		}
		if cur, ok := newest[ev.Slug]; !ok || ev.OccurredAt.After(cur) {
			newest[ev.Slug] = ev.OccurredAt
		}
	}
	out := make([]RecentNode, 0, len(newest))
	for slug, at := range newest {
		out = append(out, RecentNode{Slug: slug, At: at})
	}
	// newest first; deterministic tie by slug.
	sort.Slice(out, func(i, j int) bool {
		return out[i].At.After(out[j].At) || (out[i].At.Equal(out[j].At) && out[i].Slug < out[j].Slug)
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func eventsOf(ctx context.Context, s *Service, scope interfaces.LearningScope) []types.LearningEvent {
	events, err := listEventWindow(ctx, s.repo, scope, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		return nil
	}
	return events
}

// docOrderRanks builds the shallow-to-deep fallback rank (从浅入深).
func (s *Service) docOrderRanks(ctx context.Context, kbID string) map[string]int {
	ranks := map[string]int{}
	pageList, _ := s.nodePages(ctx, kbID)
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return ranks
	}
	materials := s.nodeMaterials(ctx, scope.TenantID, kbID, pageList)
	type pair struct {
		slug string
		r    int
	}
	list := make([]pair, 0, len(pageList))
	for _, p := range pageList {
		if p == nil || p.Slug == "" {
			continue
		}
		m, ok := materials[p.Slug]
		if !ok {
			list = append(list, pair{p.Slug, 1 << 30})
			continue
		}
		list = append(list, pair{p.Slug, m.Rank})
	}
	// stable rank by (rank, slug): unresolvable trails alphabetically.
	sort.Slice(list, func(i, j int) bool {
		return list[i].r < list[j].r || (list[i].r == list[j].r && list[i].slug < list[j].slug)
	})
	for i, p := range list {
		ranks[p.slug] = i
	}
	return ranks
}

func sortStrings(v []string) {
	sort.Strings(v)
}

func scopedGoalIDs(req ColdStartRequest, defs map[string]types.LearningObjective, pages map[string]bool) []string {
	ids := goalIDsOf(req, defs)
	if len(pages) == 0 {
		return ids
	}
	out := []string{}
	for _, id := range ids {
		if pages[defs[id].Slug] {
			out = append(out, id)
		}
	}
	return out
}
