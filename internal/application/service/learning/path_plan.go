package learning

import (
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Stage 3: cold-start and short-path guidance (01 §7). The recommendation
// unit becomes (node, objective, action): every step names WHAT to do
// (overview/read/bridge/practice/verify/review), WHY now (structured
// reason + evidence refs), how long it should take, what "done" means,
// and which step follows under which condition — successors are
// conditional, never assumed already satisfied.
//
// planShortPath is PURE: same input + policy version + seed → same path.
// LLM text polish, if ever added, may only render the structured reason —
// never alter it or invent mastery claims.

// PathPolicyVersion identifies this decision rule set; replays record it.
const PathPolicyVersion = "path-plan-v8-evidence"

// Edge relation layers (01 §6.3): only REVIEWED strict prerequisites
// constrain default eligibility; suggested and related edges never gate.
const (
	EdgeStrictPrereq = "strict_prereq"
	EdgeSuggested    = "suggested_first"
	EdgeRelated      = "related"
)

// PathAction is the action vocabulary.
const (
	ActionOverview = "overview" // 概览：the map-level orientation step
	ActionRead     = "read"     // 阅读：open the node page
	ActionBridge   = "bridge"   // 桥接：patch a missing strict prerequisite
	ActionPractice = "practice" // 练习：ungraded practice items
	ActionVerify   = "verify"   // 验证：graded, evidence-bearing quiz/task
	ActionReview   = "review"   // 复核：time-suggested re-verification
	ActionConfirm  = "confirm"  // 自主确认理解：不依赖题库，不产生验证证据
	ActionRecall   = "recall"   // 自愿回忆练习：自评难度调整复习间隔
)

// Estimated minutes per action (frozen with the policy version).
var actionMinutes = map[string]int{
	ActionOverview: 2, ActionRead: 4, ActionBridge: 3, ActionPractice: 3, ActionVerify: 3, ActionReview: 3, ActionConfirm: 1, ActionRecall: 2,
}

// PathReason is the structured, machine-checkable reason of one step.
type PathReason struct {
	Code     string   `json:"code"`               // stable reason code (see planReason*)
	Detail   string   `json:"detail,omitempty"`   // template-rendered human line
	Evidence []string `json:"evidence,omitempty"` // objective ids / edge refs backing it
}

// PathStep is one recommended step.
type PathStep struct {
	ID        string     `json:"id"`
	Completed bool       `json:"completed"`
	Slug      string     `json:"slug"`
	Title     string     `json:"title,omitempty"`
	Objective string     `json:"objective,omitempty"` // the objective this step advances
	Action    string     `json:"action"`
	Reason    PathReason `json:"reason"`
	// Minutes is the estimated duration under the frozen policy.
	Minutes int `json:"minutes"`
	// DoneWhen states the completion condition ("完成验证并全部关键检查
	// 通过" / "阅读 ≥5 秒记一次触达").
	DoneWhen string `json:"done_when"`
	// Requires lists conditions that gate THIS step (e.g. the previous
	// step's success); an empty list means actionable now.
	Requires []string `json:"requires,omitempty"`
	// Next names the follow-up step ids and their conditions.
	Next []PathNext `json:"next,omitempty"`
	// Eligibility is "default" ( prerequisites met) or "override"
	// (user-declared level-up: path eligibility only, never certification).
	Eligibility string `json:"eligibility"`
}

// PathNext is one conditional successor.
type PathNext struct {
	StepID string `json:"step_id"`
	When   string `json:"when"`
}

// PathPlan is the short path (3–5 steps) plus policy metadata.
type PathPlan struct {
	Personalization string     `json:"personalization,omitempty"`
	PolicyVersion   string     `json:"policy_version"`
	Steps           []PathStep `json:"steps"`
	// Degrade is non-empty when no qualified candidates existed and a
	// fallback orientation path was produced (never blank, never looping).
	Degrade string `json:"degrade,omitempty"`
}

// planInput is everything the pure planner needs.
type planInput struct {
	Estimates          map[string]*interfaces.LearningEstimate
	AsOf               time.Time
	ExplorationSeed    string
	RecallDue          map[string]PathReason
	RecallDueAt        map[string]time.Time
	Relevance          map[string]pathRelevance
	Personalization    string
	IncludeExploration bool
	PageScope          map[string]bool
	PageMinutes        map[string]int
	PageFolders        map[string]string
	NodeReview         map[string]bool
	NodeVerified       map[string]bool
	GoalsResolved      bool
	HasVerification    map[string]bool
	ExcludedSlugs      map[string]bool
	TenantID           uint64
	KBID               string
	// Pages are the KB's node pages (entity/concept), title set.
	Pages map[string]string // slug → title
	// Objectives are the PUBLISHED objectives (strict verification only).
	Objectives map[string]types.LearningObjective // id → def
	// ObjBySlug lists objective ids per node slug.
	ObjBySlug map[string][]string
	// States carry the objective derivations per id.
	States map[string]ObjectiveDerivation
	// StrictEdges: reviewed strict prerequisites (from → []to).
	StrictEdges map[string][]string
	// DocOrder: shallow-to-deep fallback navigation rank per slug.
	DocOrder map[string]int
	// Skips: standing user retirements (path choice, never verified).
	Skips map[string]bool
	// Exposure: contact-only facts per slug (reads/cites), never ability.
	Exposure map[string]bool
	// Recent: newest-first strong-behaviour anchors (continuity).
	Recent []RecentNode
	// UserGoals: the goal set selected at cold start (objective ids).
	UserGoals []string
	// TimeBudgetMinutes caps the plan; steps beyond it are dropped and the
	// remaining intent summarised in the last step's Next.
	TimeBudgetMinutes int
	// FastTrack: the user volunteered for the diagnostic challenge.
	FastTrack bool
	// ReviewDue: objectives whose evidence is old (time-based REVIEW
	// SUGGESTIONS; capped so they never crowd out new content).
	ReviewDue map[string]bool
	// FailedRecently: objectives with ≥2 consecutive recent eligible
	// failures — the anti-stuck signal switching to bridge/alternative.
	FailedRecently map[string]bool
}

// planShortPath derives the 3–5 step path. Ordering discipline:
// filter (permission/validity happens service-side before this input)
// → strict-prereq eligibility closure → goal progression & continuity
// → stable deterministic tie-breaks. Review entries are capped at one
// and never displace new-goal steps.
func planShortPath(in planInput) PathPlan {
	plan := PathPlan{PolicyVersion: PathPolicyVersion, Personalization: in.Personalization, Steps: []PathStep{}}
	budget := in.TimeBudgetMinutes
	if budget <= 0 {
		budget = 15
	}
	if budget > 120 {
		budget = 120
	}
	preds := map[string][]string{}
	for from, tos := range in.StrictEdges {
		if in.Pages[from] == "" {
			continue
		}
		for _, to := range tos {
			if from != to && in.Pages[to] != "" {
				preds[to] = append(preds[to], from)
			}
		}
	}
	for slug := range preds {
		sort.Strings(preds[slug])
	}
	rank := func(slug string) int {
		if r, ok := in.DocOrder[slug]; ok {
			return r
		}
		return 1 << 30
	}
	lessObj := func(a, b string) bool {
		oa, ob := in.Objectives[a], in.Objectives[b]
		if in.Estimates != nil && modelReadValue(in, oa.Slug) != modelReadValue(in, ob.Slug) {
			return modelReadValue(in, oa.Slug) > modelReadValue(in, ob.Slug)
		}
		if in.Relevance[oa.Slug].Priority != in.Relevance[ob.Slug].Priority {
			return in.Relevance[oa.Slug].Priority > in.Relevance[ob.Slug].Priority
		}
		if rank(oa.Slug) != rank(ob.Slug) {
			return rank(oa.Slug) < rank(ob.Slug)
		}
		return a < b
	}
	goals := append([]string{}, in.UserGoals...)
	if len(goals) == 0 && !in.GoalsResolved {
		for id := range in.Objectives {
			goals = append(goals, id)
		}
	}
	sort.Slice(goals, func(i, j int) bool { return lessObj(goals[i], goals[j]) })
	goalSet := map[string]bool{}
	for _, id := range goals {
		goalSet[id] = true
	}
	seen := map[string]bool{}
	visiting := map[string]bool{}
	spent := 0
	reviewCount := 0
	var add func(types.LearningObjective, string, string, []string, string) string
	add = func(o types.LearningObjective, action, reason string, requires []string, elig string) string {
		id := fmt.Sprintf("%s:%s:%s", o.Slug, o.ID, action)
		for _, st := range plan.Steps {
			if st.ID == id {
				return id
			}
			if st.Slug == o.Slug && (action == ActionRead || action == ActionBridge) && (st.Action == ActionRead || st.Action == ActionBridge) {
				return st.ID
			}
		}
		minutes := actionMinutes[action]
		if (action == ActionRead || action == ActionBridge || action == ActionOverview) && in.PageMinutes[o.Slug] > 0 {
			minutes = in.PageMinutes[o.Slug]
		}
		if spent+minutes > budget || len(plan.Steps) >= 5 {
			return ""
		}
		spent += minutes
		why := PathReason{Code: reason}
		if o.ID != "" {
			why.Evidence = []string{o.ID}
		}
		if relevant := in.Relevance[o.Slug]; relevant.Priority > 0 && (reason == "plan_reason_docorder_fallback" || reason == "plan_reason_first_contact") {
			why = relevant.Reason
		}
		plan.Steps = append(plan.Steps, PathStep{ID: id, Slug: o.Slug, Title: in.Pages[o.Slug], Objective: o.ID, Action: action, Reason: why, Minutes: minutes, DoneWhen: doneWhenOf(action), Requires: requires, Eligibility: elig})
		return id
	}
	var visit func(string) string
	visit = func(id string) string {
		o, ok := in.Objectives[id]
		if !ok || in.Pages[o.Slug] == "" || in.Skips[o.Slug] || in.ExcludedSlugs[o.Slug] {
			return ""
		}
		d := in.States[id]
		if d.State == types.ObjectiveStateVerified {
			return ""
		}
		if visiting[id] {
			plan.Degrade = "plan_degrade_cycle"
			return ""
		}
		if seen[id] {
			for i := len(plan.Steps) - 1; i >= 0; i-- {
				if plan.Steps[i].Objective == id {
					return plan.Steps[i].ID
				}
			}
			return ""
		}
		visiting[id] = true
		defer func() { visiting[id] = false; seen[id] = true }()
		deps := []string{}
		elig := "default"
		if !in.FastTrack {
			preIDs := []string{}
			if o.PrereqObjectiveID != "" {
				preIDs = append(preIDs, o.PrereqObjectiveID)
			}
			for _, slug := range preds[o.Slug] {
				if in.Skips[slug] {
					elig = "override"
					continue
				}
				ids := in.ObjBySlug[slug]
				if len(ids) == 0 {
					elig = "unconfirmed"
					if !in.Exposure[slug] {
						bridge := types.LearningObjective{Slug: slug}
						if step := add(bridge, ActionBridge, "plan_reason_bridge_prereq", nil, "unconfirmed"); step != "" {
							deps = append(deps, step)
						}
					}
				} else {
					preIDs = append(preIDs, ids...)
				}
			}
			sort.Slice(preIDs, func(i, j int) bool { return lessObj(preIDs[i], preIDs[j]) })
			for _, pre := range preIDs {
				if in.States[pre].State == types.ObjectiveStateVerified {
					continue
				}
				if step := visit(pre); step != "" {
					deps = append(deps, step)
				} else {
					elig = "unconfirmed"
				}
			}
		} else {
			elig = "override"
		}
		hasBank := in.HasVerification == nil || in.HasVerification[id]
		if in.FastTrack && hasBank {
			return add(o, ActionVerify, "plan_reason_fast_track_challenge", nil, "override")
		}
		// Reading changes only the action to recommend, never the evidence state.
		last := ""
		if !in.Exposure[o.Slug] {
			action := ActionRead
			reason := "plan_reason_first_contact"
			if !goalSet[id] {
				action = ActionBridge
				reason = "plan_reason_bridge_prereq"
			}
			if len(preds[o.Slug]) > 0 && len(deps) > 0 {
				reason = "plan_reason_goal_successor"
			}
			if in.FailedRecently[id] {
				action = ActionBridge
				reason = "plan_reason_bridge_after_failures"
			}
			last = add(o, action, reason, deps, elig)
			if last == "" {
				return ""
			}
			deps = []string{last}
		}
		if hasBank {
			if st := add(o, ActionVerify, "plan_reason_verify_contract", deps, elig); st != "" {
				last = st
			}
		} else if last == "" {
			plan.Degrade = "plan_degrade_no_verification"
		}
		return last
	}
	// Reserve one place for an explicitly requested revisit, even when a
	// large published objective bank would otherwise consume every slot.
	requestedReviews := []string{}
	for slug, needed := range in.NodeReview {
		if needed && in.RecallDue[slug].Code == "" && in.Pages[slug] != "" && !in.Skips[slug] && !in.ExcludedSlugs[slug] && (len(in.PageScope) == 0 || in.PageScope[slug]) {
			requestedReviews = append(requestedReviews, slug)
		}
	}
	sort.Slice(requestedReviews, func(i, j int) bool {
		if rank(requestedReviews[i]) != rank(requestedReviews[j]) {
			return rank(requestedReviews[i]) < rank(requestedReviews[j])
		}
		return requestedReviews[i] < requestedReviews[j]
	})
	for _, slug := range requestedReviews {
		if add(types.LearningObjective{Slug: slug}, ActionRead, "plan_reason_user_review", nil, "exploration") != "" {
			break
		}
	}
	// Voluntary recall is independent of question-bank eligibility. Reserve at
	// most two minutes and one slot; the rest stays available for new learning.
	recalls := []string{}
	for slug := range in.RecallDue {
		if in.Pages[slug] != "" && !in.ExcludedSlugs[slug] && (len(in.PageScope) == 0 || in.PageScope[slug]) {
			recalls = append(recalls, slug)
		}
	}
	sort.Slice(recalls, func(i, j int) bool {
		a, b := in.RecallDueAt[recalls[i]], in.RecallDueAt[recalls[j]]
		if !a.Equal(b) {
			return a.Before(b)
		}
		return recalls[i] < recalls[j]
	})
	for _, slug := range recalls {
		duplicate := false
		for _, step := range plan.Steps {
			if step.Slug == slug {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		if add(types.LearningObjective{Slug: slug}, ActionRecall, "plan_reason_scheduled_recall", nil, "self_report") != "" {
			plan.Steps[len(plan.Steps)-1].Reason = in.RecallDue[slug]
			break
		}
	}
	if len(goals) == 0 && len(plan.Steps) == 0 && in.Estimates != nil {
		if id := modelCheckObjective(in, preds, budget); id != "" {
			o := in.Objectives[id]
			if step := add(o, ActionVerify, "plan_reason_model_check", nil, "default"); step != "" {
				last := &plan.Steps[len(plan.Steps)-1]
				last.Reason = PathReason{Code: "plan_reason_model_check", Detail: "这一页已阅读；用一道未见过的题补充这个目标的检查证据。", Evidence: []string{id, "fewest_independent_observations_first"}}
				last.DoneWhen = "回答一道未见过的题；也可直接继续阅读，不要求一次完成目标验证。"
			}
		}
	}
	for _, id := range goals {
		o := in.Objectives[id]
		if reviewCount == 0 && in.ReviewDue[id] && !in.Skips[o.Slug] && !in.ExcludedSlugs[o.Slug] && (in.HasVerification == nil || in.HasVerification[id]) {
			if add(o, ActionReview, "plan_reason_review_due", nil, "default") != "" {
				reviewCount++
			}
		}
	}
	for _, id := range goals {
		visit(id)
	}
	if in.IncludeExploration {
		candidates := []string{}
		for slug := range in.Pages {
			if e := in.Estimates[slug]; e != nil && e.ReadPriority <= 0 && !in.NodeReview[slug] {
				continue
			}
			if (len(in.PageScope) == 0 || in.PageScope[slug]) && !in.Skips[slug] && !in.ExcludedSlugs[slug] && (!in.NodeVerified[slug] || in.NodeReview[slug]) {
				candidates = append(candidates, slug)
			}
		}
		recentFolder := ""
		if len(in.Recent) > 0 {
			recentFolder = in.PageFolders[in.Recent[0].Slug]
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			if in.NodeReview[a] != in.NodeReview[b] {
				return in.NodeReview[a]
			}
			// Model estimates drive ordinary reading. Legacy snapshots without
			// estimates retain the previous explicit-confirmation fallback.
			if in.Estimates != nil {
				av, bv := modelReadValue(in, a), modelReadValue(in, b)
				if av != bv {
					return av > bv
				}
			}
			if in.Estimates == nil && len(in.Recent) > 0 && (a == in.Recent[0].Slug) != (b == in.Recent[0].Slug) {
				return a == in.Recent[0].Slug
			}
			ac, bc := recentFolder != "" && in.PageFolders[a] == recentFolder, recentFolder != "" && in.PageFolders[b] == recentFolder
			if ac != bc {
				return ac
			}
			if in.Relevance[a].Priority != in.Relevance[b].Priority {
				return in.Relevance[a].Priority > in.Relevance[b].Priority
			}
			if rank(a) != rank(b) {
				return rank(a) < rank(b)
			}
			return a < b
		})
		explorationSlug := promoteModelExploration(in, candidates)
		visitingPages := map[string]bool{}
		var explore func(string) (string, bool)
		explore = func(slug string) (string, bool) {
			if e := in.Estimates[slug]; e != nil && e.ReadPriority <= 0 && !in.NodeReview[slug] {
				return "", true
			}
			if in.Skips[slug] || (in.NodeVerified[slug] && !in.NodeReview[slug]) {
				return "", true
			}
			if in.ExcludedSlugs[slug] {
				return "", false
			}
			for _, step := range plan.Steps {
				if step.Slug == slug {
					return step.ID, true
				}
			}
			if visitingPages[slug] {
				plan.Degrade = "plan_degrade_cycle"
				return "", false
			}
			visitingPages[slug] = true
			defer delete(visitingPages, slug)
			deps := []string{}
			for _, pre := range preds[slug] {
				// Reading a prerequisite enables exploration, never verification.
				if in.Exposure[pre] && !in.NodeReview[pre] {
					continue
				}
				id, ok := explore(pre)
				if !ok {
					return "", false
				}
				if id != "" {
					deps = append(deps, id)
				}
			}
			reason := "plan_reason_docorder_fallback"
			if in.NodeReview[slug] {
				reason = "plan_reason_user_review"
			} else if recentFolder != "" && in.PageFolders[slug] == recentFolder {
				reason = "plan_reason_continue_module"
			}
			action := ActionRead
			if in.Estimates == nil && in.Exposure[slug] && !in.NodeReview[slug] {
				action, reason = ActionConfirm, "plan_reason_confirm_understanding"
			}
			id := add(types.LearningObjective{Slug: slug}, action, reason, deps, "unconfirmed")
			if id != "" && in.Estimates[slug] != nil && !in.NodeReview[slug] {
				for i := range plan.Steps {
					if plan.Steps[i].ID == id {
						plan.Steps[i].Reason = modelReadReason(in, slug)
						if slug == explorationSlug {
							plan.Steps[i].Reason = PathReason{Code: "plan_reason_model_exploration", Detail: "本轮留出一个未接触知识点，帮助发现常用主题之外的空白；仍遵守前置关系与时间预算。", Evidence: []string{PathPolicyVersion}}
						}
						plan.Steps[i].DoneWhen = "有效阅读会自动更新阅读进度与下一步；熟悉或困难反馈仅用于调整推荐。"
					}
				}
			}
			return id, id != ""
		}
		for _, slug := range candidates {
			if len(plan.Steps) >= 5 {
				break
			}
			explore(slug)
		}
		if len(plan.Steps) == 0 && plan.Degrade == "" {
			if len(in.Pages) == 0 {
				plan.Degrade = "plan_degrade_no_content"
			} else if len(candidates) == 0 {
				plan.Degrade = "plan_degrade_covered"
			} else {
				plan.Degrade = "plan_degrade_budget"
			}
		}
	} else {
		if len(plan.Steps) == 0 {
			allDone := len(goals) > 0
			for _, id := range goals {
				if in.States[id].State != types.ObjectiveStateVerified {
					allDone = false
				}
			}
			if allDone {
				plan.Degrade = "plan_degrade_complete"
				return plan
			}
			var slugs []string
			for slug := range in.Pages {
				if !in.Skips[slug] && !in.ExcludedSlugs[slug] && !in.Exposure[slug] {
					slugs = append(slugs, slug)
				}
			}
			sort.Slice(slugs, func(i, j int) bool {
				if rank(slugs[i]) != rank(slugs[j]) {
					return rank(slugs[i]) < rank(slugs[j])
				}
				return slugs[i] < slugs[j]
			})
			if len(in.Pages) == 0 {
				plan.Degrade = "plan_degrade_no_content"
			} else if len(slugs) == 0 {
				if plan.Degrade == "" {
					plan.Degrade = "plan_degrade_explored"
				}
			} else {
				plan.Degrade = "plan_degrade_orientation"
				o := types.LearningObjective{Slug: slugs[0]}
				action := ActionRead
				if budget < actionMinutes[action] {
					action = ActionOverview
				}
				add(o, action, "plan_reason_docorder_fallback", nil, "unconfirmed")
			}
		}
	}
	for i := range plan.Steps {
		for j := range plan.Steps {
			for _, dep := range plan.Steps[j].Requires {
				if dep == plan.Steps[i].ID {
					plan.Steps[i].Next = append(plan.Steps[i].Next, PathNext{StepID: plan.Steps[j].ID, When: "完成所需阅读或验证后继续"})
				}
			}
		}
	}
	return plan
}

func stepSlugAction(s PathStep) string { return s.Slug + ":" + s.Action }

func actionForState(d ObjectiveDerivation) string {
	switch d.State {
	case types.ObjectiveStateUnverified:
		if d.Evidence.Unknown {
			return ActionRead // no contact yet: read first
		}
		return ActionPractice
	case types.ObjectiveStatePartial, types.ObjectiveStateConflicting:
		return ActionVerify
	case types.ObjectiveStateStale:
		return ActionReview
	default:
		return ActionVerify
	}
}

func reasonForAction(action string) string {
	switch action {
	case ActionRead:
		return "plan_reason_first_contact"
	case ActionPractice:
		return "plan_reason_practice_before_verify"
	case ActionVerify:
		return "plan_reason_verify_contract"
	case ActionBridge:
		return "plan_reason_bridge_prereq"
	case ActionReview:
		return "plan_reason_review_due"
	}
	return "plan_reason_advance"
}

func doneWhenOf(action string) string {
	switch action {
	case ActionOverview:
		return "浏览领域地图，了解模块划分"
	case ActionRead:
		return "阅读会自动记录并更新建议；可以按需反馈已经熟悉或仍有困难。"
	case ActionConfirm:
		return "确认已会后点亮为绿色并移出待学；有疑问可回看材料或标记需要再学。"
	case ActionRecall:
		return "尝试回忆、核对材料并提交回忆难度，更新个人复习安排。"
	case ActionBridge:
		return "补学前置材料并理解其行为目标"
	case ActionPractice:
		return "完成练习并获得反馈（不计入能力证据）"
	case ActionVerify:
		return "通过验证契约（两个独立题族 / 任务全部关键检查）"
	case ActionReview:
		return "完成一次复核验证，恢复或确认已验证状态"
	}
	return "完成本步骤"
}

// eligibilityOf: default when every strict prerequisite chain is verified;
// override is only set by the fast-track path (user declaration).
func eligibilityOf(slug string) string { return "default" }
