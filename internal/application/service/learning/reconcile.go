package learning

import "sort"

// SlugMigration is one deterministic rename repair: the mastery fold (and
// the events it came from) moves from a slug that no longer resolves to the
// live slug the alias index names. The caller persists the merged state
// under ToSlug and retires the FromSlug row.
type SlugMigration struct {
	FromSlug   string
	ToSlug     string
	State      FoldState
	EventCount int
}

// ReconcileSlug repairs slug drift after wiki pages are renamed or merged.
// aliasIndex maps a stale slug to the live slug that now owns it (built
// from the live pages' Aliases). For every stale slug that has folded state:
//
//   - with a live target that has no state yet, the state moves as-is —
//     identical numbers, hence an unchanged p_eff at any read time;
//   - with a live target that already has state, the merge is a full event
//     replay (both event lists folded together, in occurred_at order), not
//     a logit addition — replay respects the clamp and the counters, an
//     addition would not;
//   - with no alias entry, nothing is emitted: the row stays put for the
//     next reconciliation round, because guessing a target would be worse
//     than waiting.
//
// The function is pure: states and events are handed in, migrations are
// handed back, and the persistence order (write target, then delete source)
// is the caller's single transaction.
func ReconcileSlug(states map[string]FoldState, eventsBySlug map[string][]Event, aliasIndex map[string]string) []SlugMigration {
	var migrations []SlugMigration
	for from, state := range states {
		to, ok := aliasIndex[from]
		if !ok || to == from || to == "" {
			continue
		}

		fromEvents := eventsBySlug[from]
		toEvents := eventsBySlug[to]
		_, toHasState := states[to]

		switch {
		case !toHasState:
			// Pure move: the fold already is the replay of fromEvents.
			migrations = append(migrations, SlugMigration{
				FromSlug:   from,
				ToSlug:     to,
				State:      state,
				EventCount: len(fromEvents),
			})
		default:
			// Merge by replaying both event lists together in time order.
			// Zero-weight events (deduped re-reads, unsure answers) join the
			// EventCount but never the fold — the write path skips them, and
			// the merge must land on exactly the state a clean sequential
			// history would have folded.
			merged := make([]Event, 0, len(fromEvents)+len(toEvents))
			merged = append(merged, fromEvents...)
			merged = append(merged, toEvents...)
			sort.SliceStable(merged, func(i, j int) bool {
				return merged[i].OccurredAt.Before(merged[j].OccurredAt)
			})
			folded := FoldState{}
			for _, e := range merged {
				if e.Weight != 0 {
					folded = FoldEvent(folded, e)
				}
			}
			migrations = append(migrations, SlugMigration{
				FromSlug:   from,
				ToSlug:     to,
				State:      folded,
				EventCount: len(merged),
			})
		}
	}

	// Deterministic output order keeps reconciliation diffs reviewable.
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].FromSlug < migrations[j].FromSlug
	})
	return migrations
}
