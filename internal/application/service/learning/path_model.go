package learning

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// modelReadValue ranks coverage gaps per minute, with bounded relevance and
// graph-unlock preference. It does not estimate learning yield or ability.
func modelReadValue(in planInput, slug string) float64 {
	e := in.Estimates[slug]
	if e == nil {
		return 0
	}
	minutes := in.PageMinutes[slug]
	if minutes < 1 {
		minutes = 1
	}
	relevance := 1 + math.Min(0.3, math.Max(0, float64(in.Relevance[slug].Priority)*0.1))
	unlock := 1 + math.Min(0.2, float64(len(in.StrictEdges[slug]))*0.05)
	return e.ReadPriority * relevance * unlock / float64(minutes)
}

func modelReadReason(in planInput, slug string) PathReason {
	e := in.Estimates[slug]
	detail := "结合阅读进度和所需时间，这一页适合接着学习。"
	if e.Coverage == 0 {
		detail = "这一页尚缺少阅读记录；结合所需时间，优先补足这处阅读空白。"
	} else if e.Coverage < 1 {
		detail = "这一页已有初步接触；继续阅读可补足覆盖，无需先确认已会。"
	} else {
		detail = "这一页已有阅读记录；本次回看不直接增加掌握估计。"
	}
	if in.Relevance[slug].Priority > 0 {
		detail += " 同时参考了你的相关主题或常用文档。"
	}
	return PathReason{Code: "plan_reason_reading_gap", Detail: detail, Evidence: []string{
		e.ModelVersion, fmt.Sprintf("priority_per_minute=%.6f", modelReadValue(in, slug)),
	}}
}

// One optional diagnostic, at most a quarter of the budget, after actual
// reading. Never force a quiz to navigate, use an unavailable/draft item, or
// repeat a diagnostic within a day. Explicit objective goals are handled by
// the existing verification path instead.
func modelCheckObjective(in planInput, predecessors map[string][]string, budget int) string {
	if actionMinutes[ActionVerify]*4 > budget || in.AsOf.IsZero() {
		return ""
	}
	ids := make([]string, 0, len(in.Objectives))
	for id := range in.Objectives {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	best, bestValue := "", 0.0
	for _, id := range ids {
		o := in.Objectives[id]
		e := in.Estimates[o.Slug]
		if e == nil || e.Coverage < 1 || !in.HasVerification[id] || in.Skips[o.Slug] || in.ExcludedSlugs[o.Slug] || in.NodeReview[o.Slug] || (len(in.PageScope) > 0 && !in.PageScope[o.Slug]) || in.States[id].State == types.ObjectiveStateVerified {
			continue
		}
		if !e.LastAnswerAt.IsZero() && in.AsOf.Sub(e.LastAnswerAt) < 24*time.Hour {
			continue
		}
		ready := o.PrereqObjectiveID == "" || in.States[o.PrereqObjectiveID].State == types.ObjectiveStateVerified
		for _, pre := range predecessors[o.Slug] {
			ready = ready && in.NodeVerified[pre]
		}
		if !ready {
			continue
		}
		facts := in.States[id].Evidence
		value := 1 / float64(1+facts.EligiblePasses+facts.EligibleFailures)
		if value > bestValue {
			best, bestValue = id, value
		}
	}
	return best
}

// Keep the four highest-utility candidates, then offer one previously unseen
// alternative from the remainder. The daily subject/KB seed makes the choice
// reproducible; no randomness, extra service, or user click is required. The
// planner still applies dependency closure, exclusions and its time budget.
func promoteModelExploration(in planInput, candidates []string) string {
	if in.Estimates == nil || in.ExplorationSeed == "" || len(candidates) < 6 {
		return ""
	}
	chosen, best := -1, ^uint64(0)
	for i := 4; i < len(candidates); i++ {
		slug := candidates[i]
		if e := in.Estimates[slug]; e == nil || e.Level != "unseen" {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(in.ExplorationSeed + "|" + slug))
		if v := h.Sum64(); chosen < 0 || v < best {
			chosen, best = i, v
		}
	}
	if chosen < 0 {
		return ""
	}
	slug := candidates[chosen]
	copy(candidates[5:chosen+1], candidates[4:chosen])
	candidates[4] = slug
	return slug
}
