package learning

import "sort"

// The ordinary Wiki path has no authored KC capability boundary. Its focus is
// a page to read, and only the service's manual prerequisite edges establish
// a dependency. Folder membership and source position are navigation hints.
func focusedReadingPredecessors(in planInput) map[string][]string {
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
	return preds
}

func readingFocusDepth(slug string, preds map[string][]string, visiting map[string]bool, memo map[string]int) int {
	if depth, ok := memo[slug]; ok {
		return depth
	}
	if visiting[slug] {
		return 0
	}
	visiting[slug] = true
	defer delete(visiting, slug)
	depth := 0
	for _, pre := range preds[slug] {
		depth = max(depth, 1+readingFocusDepth(pre, preds, visiting, memo))
	}
	memo[slug] = depth
	return depth
}

func planFocusedReading(in planInput) PathPlan {
	type choice struct {
		slug     string
		action   string
		priority int
		depth    int
	}
	preds := focusedReadingPredecessors(in)
	depths := map[string]int{}
	choices := []choice{}
	readFolders := map[string]bool{}
	for slug, title := range in.Pages {
		if title == "" || in.ExcludedSlugs[slug] || (len(in.PageScope) > 0 && !in.PageScope[slug]) {
			continue
		}
		c := choice{slug: slug, action: ActionRead, depth: readingFocusDepth(slug, preds, map[string]bool{}, depths)}
		if in.RecallDue[slug].Code != "" {
			c.action, c.priority = ActionRecall, 2
		} else if in.NodeReview[slug] && !in.Skips[slug] {
			c.priority = 1
		} else {
			e := in.Estimates[slug]
			if in.Skips[slug] || in.NodeVerified[slug] || e == nil || e.ReadPriority <= 0 {
				continue
			}
		}
		choices = append(choices, c)
		if c.action == ActionRead && in.PageFolders[slug] != "" {
			readFolders[in.PageFolders[slug]] = true
		}
	}
	budget := in.TimeBudgetMinutes
	if budget <= 0 {
		budget = 15
	}
	budget = min(120, budget)
	// An available diagnostic remains an option after new reading. It cannot
	// repeatedly hold the first recommendation ahead of an unread successor.
	if id := modelCheckObjective(in, preds, budget); id != "" {
		choices = append(choices, choice{slug: in.Objectives[id].Slug, action: ActionVerify, priority: -1})
	}
	currentFolder := ""
	for _, recent := range in.Recent {
		folder := in.PageFolders[recent.Slug]
		if in.Pages[recent.Slug] != "" && readFolders[folder] {
			currentFolder = folder
			break
		}
	}
	rank := func(slug string) int {
		if value, ok := in.DocOrder[slug]; ok {
			return value
		}
		return 1 << 30
	}
	sort.SliceStable(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		if a.action == ActionRecall && !in.RecallDueAt[a.slug].Equal(in.RecallDueAt[b.slug]) {
			return in.RecallDueAt[a.slug].Before(in.RecallDueAt[b.slug])
		}
		nearA := currentFolder != "" && in.PageFolders[a.slug] == currentFolder
		nearB := currentFolder != "" && in.PageFolders[b.slug] == currentFolder
		if nearA != nearB {
			return nearA
		}
		if a.depth != b.depth {
			return a.depth > b.depth
		}
		if av, bv := modelReadValue(in, a.slug), modelReadValue(in, b.slug); av != bv {
			return av > bv
		}
		if rank(a.slug) != rank(b.slug) {
			return rank(a.slug) < rank(b.slug)
		}
		return a.slug < b.slug
	})
	if len(choices) == 0 {
		// No usable focus is a stopping condition. Returning to the legacy
		// exploration loop could refill the plan with pages whose current
		// estimates are missing, since legacy snapshots permit those pages.
		plan := PathPlan{PolicyVersion: PathPolicyVersion, Personalization: in.Personalization, Steps: []PathStep{}, Degrade: "plan_degrade_covered"}
		if len(in.Pages) == 0 {
			plan.Degrade = "plan_degrade_no_content"
		}
		plan.Progression = "当前范围没有待安排的新阅读或到期动作；可以自主回看，暂时没有建议不代表全部知识已经掌握。"
		plan.Limitations = []string{"Wiki 阅读记录不等于已审核能力；缺少可用检查时不会自动生成验证任务。"}
		for slug := range in.Pages {
			if (len(in.PageScope) == 0 || in.PageScope[slug]) && in.Estimates[slug] == nil {
				plan.Limitations = append(plan.Limitations, "部分页面缺少当前阅读估计，暂不自动安排；缺少估计不能视为已经掌握。")
				break
			}
		}
		return plan
	}
	var first PathPlan
	for index, candidate := range choices {
		focused := in
		focused.FocusResolved = true
		focused.SkipOptionalCheck = candidate.action != ActionVerify
		focused.PageScope = map[string]bool{candidate.slug: true}
		// The existing bounded traversal expands only necessary manual
		// prerequisites, including across the requested module. No global
		// search or new model is run for these feasibility fallbacks.
		plan := planShortPath(focused)
		plan.FocusSlug, plan.FocusTitle = candidate.slug, in.Pages[candidate.slug]
		plan.Limitations = []string{"本轮围绕 Wiki 页安排阅读；阅读准备、自评和独立检查分别记录，不把页面视为已审核能力目标。"}
		if len(preds[candidate.slug]) == 0 {
			plan.Limitations = append(plan.Limitations, "该页没有可用的人工先修关系；同一模块、相关链接和来源位置都不能证明教学先后。")
		}
		hasCheck := false
		for _, id := range in.ObjBySlug[candidate.slug] {
			hasCheck = hasCheck || in.HasVerification[id]
		}
		if !hasCheck {
			plan.Limitations = append(plan.Limitations, "该页暂无可用的已审核检查，当前仅提供阅读或自愿回忆建议。")
		}
		switch {
		case len(plan.Steps) == 0:
			plan.Progression = "这个阅读目标暂时无法起步；保留先修、排除项和时间限制，不假定未完成的前置已经具备。"
		case candidate.action == ActionRecall:
			plan.Progression = "先处理已到期的自愿回忆，再根据实际反馈安排下一次学习；回忆自评不增加独立验证证据。"
		case candidate.priority == 1:
			plan.Progression = "先回看仍有困难的页面；实际阅读和反馈返回后再安排后续，不要求再声明已会才能继续。"
		case candidate.action == ActionVerify:
			plan.Progression = "当前没有可推进的新阅读，本次提供已有的自愿检查；可以不参加，检查结果只归属其已审核目标。"
		case plan.Steps[0].Slug != candidate.slug:
			plan.Progression = "围绕「" + plan.FocusTitle + "」阅读，先补「" + plan.Steps[0].Title + "」；顺序来自人工标注的先修，后续按实际阅读结果重算，不预先认证能力。"
		case len(preds[candidate.slug]) > 0:
			plan.Progression = "继续阅读「" + plan.FocusTitle + "」；人工先修的阅读准备允许继续通读，不代表前置能力已通过检查。"
		case currentFolder != "" && in.PageFolders[candidate.slug] == currentFolder:
			plan.Progression = "继续最近仍有待学内容的模块，减少来回切换；该页没有人工先修，仅安排连续阅读。"
		default:
			plan.Progression = "结合阅读空白、所需时间与来源位置选择这一页；没有人工先修时，仅作为阅读建议，不宣称从基础到应用。"
		}
		if index == 0 {
			first = plan
		}
		if len(plan.Steps) > 0 {
			if index > 0 {
				plan.Progression += " 优先目标暂时无法起步，本轮改为范围内可执行的目标。"
			}
			return plan
		}
	}
	return first
}
