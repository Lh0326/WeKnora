package learning

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PassiveChanges serves the "遗忘动态" channel: decay-driven state changes
// the user never acted to cause. The design line drawn here mirrors the
// repo's event-sourcing discipline — the timeline (learning_events) stays a
// log of real actions only, so passive drift can never flood out the
// user's own activity; forgetting is reported through this separate
// read-time channel instead (the Anki deck-list split: activity history vs
// due queue). Nothing here persists anything: every field is derived from
// the folded state and the clock, exactly like EffectiveP.
func (s *Service) PassiveChanges(ctx context.Context, kbID string, limit int) (*interfaces.PassiveChangesSummary, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	pages, err := s.nodePages(ctx, kbID)
	if err != nil {
		return nil, err
	}
	states := map[string]FoldState{}
	if rows, err := s.repo.ListMastery(ctx, scope); err != nil {
		logger.Warnf(ctx, "learning: passive-changes mastery read failed (kb %s): %v", kbID, err)
	} else {
		for i := range rows {
			states[rows[i].Slug] = StateFromModel(&rows[i])
		}
	}
	// Standing skips leave the forgetting digest too — the user retired the
	// node from every "what to do next" surface, this queue included.
	skips := map[string]bool{}
	if rows, err := s.repo.ListSkips(ctx, scope); err != nil {
		logger.Warnf(ctx, "learning: passive-changes skip read failed (kb %s): %v", kbID, err)
	} else {
		for slug := range rows {
			skips[slug] = true
		}
	}
	return derivePassiveChanges(pages, states, s.directFacts(ctx, scope), skips, time.Now(), limit), nil
}

// derivePassiveChanges is the pure, table-testable core: which earned
// tiers has forgetting already demoted, which will demote within
// PassiveDueSoonDays, and the most urgent items first. Nodes with nothing
// earned (anchor unseen) are skipped — nothing to forget. The anchor is
// direct-gate capped: a tier the gate would not grant can never read as
// "demoted from" it. Skipped nodes (nil map = none) are excluded — the
// user's standing declaration retires the node from this queue as well.
func derivePassiveChanges(
	pages []*types.WikiPage, states map[string]FoldState,
	direct map[string][]DirectQuizFact, skips map[string]bool, now time.Time, limit int,
) *interfaces.PassiveChangesSummary {
	titleBySlug := map[string]string{}
	for _, p := range pages {
		if p != nil && p.Slug != "" {
			titleBySlug[p.Slug] = p.Title
		}
	}

	type row struct {
		ch   interfaces.PassiveChange
		drop int // tier ranks lost to decay (0 = due soon, not yet demoted)
		due  float64
	}
	var rows []row
	demoted, dueSoon := 0, 0

	for slug, st := range states {
		if st.EvidenceCount == 0 || st.LastEvidenceAt.IsZero() {
			continue
		}
		if skips[slug] {
			continue // user-retired from every next-step surface
		}
		anchor, view := gatedAnchorAndView(st, direct[slug], now)
		ar, vr := levelRank(anchor), levelRank(view)
		if ar == 0 {
			continue // never earned a lit tier: nothing to forget
		}

		var next *float64
		if vr >= ar {
			// Still holds its earned tier: when does decay cross the gate?
			if threshold := tierDownThreshold(anchor); threshold > 0 {
				next = NextReviewDays(st, now, threshold)
			}
		}
		isDemoted := vr < ar
		isDueSoon := !isDemoted && next != nil && *next <= PassiveDueSoonDays
		if !isDemoted && !isDueSoon {
			continue
		}
		if isDemoted {
			demoted++
		} else {
			dueSoon++
		}

		daysIdle := 0.0
		if now.After(st.LastEvidenceAt) {
			daysIdle = now.Sub(st.LastEvidenceAt).Hours() / 24
		}
		due := math.Inf(1)
		if next != nil {
			due = *next
		}
		rows = append(rows, row{
			ch: interfaces.PassiveChange{
				Slug: slug, Title: titleBySlug[slug],
				AnchorLevel: string(anchor), ViewLevel: string(view),
				BaseP: 1 / (1 + math.Exp(-st.Logit)),
				PEff:  EffectiveP(st, now),
				DaysIdle: daysIdle, StabilityDays: st.Stability,
				NextReviewDays: next, Demoted: isDemoted,
				LowConfidence: st.EvidenceCount < LowConfidenceEvidence,
				LastEvidenceAt: st.LastEvidenceAt,
			},
			drop: ar - vr,
			due:  due,
		})
	}

	// Most urgent first: demotions before due-soon, deeper demotions
	// before shallower ones, then soonest due; slug breaks ties so the
	// order is deterministic regardless of map iteration.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].drop != rows[j].drop {
			return rows[i].drop > rows[j].drop
		}
		if rows[i].due != rows[j].due {
			return rows[i].due < rows[j].due
		}
		return rows[i].ch.Slug < rows[j].ch.Slug
	})

	if limit < 0 {
		limit = 0
	}
	if limit > len(rows) {
		limit = len(rows)
	}
	items := make([]interfaces.PassiveChange, 0, limit)
	for _, r := range rows[:limit] {
		items = append(items, r.ch)
	}
	return &interfaces.PassiveChangesSummary{
		DemotedCount: demoted, DueSoonCount: dueSoon, Items: items,
	}
}
