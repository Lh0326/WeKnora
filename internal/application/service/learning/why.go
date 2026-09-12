package learning

import (
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// recentAnchors distils the continuity mainline from the caller's event
// history: the newest STRONG-behaviour touch per node, newest first, at
// most three anchors inside the continuity window. Strong behaviour = the
// person acted (answered a quiz item, opened a page, asked a question the
// answer cited); passive projections (topic mapping, backfilled affinity),
// re-ask churn and unsure declarations are excluded so a stray click or a
// background job cannot steer the "刚学完 → 下一个" thread.
func recentAnchors(history []types.LearningEvent, now time.Time) []RecentNode {
	cutoff := now.Add(-ContinuityWindow)
	newest := map[string]time.Time{}
	for _, ev := range history {
		switch ev.Type {
		case types.LearningEventQuizCorrect, types.LearningEventQuizWrong,
			types.LearningEventWikiToolRead, types.LearningEventAnswerCite,
			types.LearningEventCrossRef:
		default:
			continue // passive or zero-information types never anchor
		}
		if ev.OccurredAt.Before(cutoff) || ev.OccurredAt.After(now) {
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
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At) // newest anchor = the mainline
		}
		return out[i].Slug < out[j].Slug
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// buildWhy fills the one-line learning reason (承上启下 narrative) for a
// card the specialized channels have not claimed. Priority:
//
//   - already-set keys (continues_prereq / same_section / continues_prev)
//     pass through untouched — they describe real learning history;
//   - the specialized channels (self-verify, review, remedial, struggling,
//     shore-up, bypass) keep their own explanations: no narrative line, the
//     chapter/document context fields still ride along;
//   - otherwise the material speaks: first_stop for the opening card,
//     material_start for foundation cards, chapter_of / in_doc as the
//     always-true fallbacks. No resolvable material → no line.
func buildWhy(rec *Recommendation, mat NodeMaterial) {
	if rec.Why != "" {
		return
	}
	switch rec.Reason {
	case "self-verify", "review", "remedial", "struggling", "prerequisite-stuck", "bypass":
		return
	}
	if mat == (NodeMaterial{}) {
		return
	}
	switch {
	case rec.DocRank == 1:
		rec.Why = "first_stop"
	case rec.Reason == "foundation":
		rec.Why = "material_start"
	case mat.Section != "":
		rec.Why = "chapter_of"
		rec.WhyRef = mat.Section
	default:
		rec.Why = "in_doc"
		rec.WhyRef = mat.DocTitle
	}
}
