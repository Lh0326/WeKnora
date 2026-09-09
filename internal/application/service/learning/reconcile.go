package learning

import (
	"github.com/Tencent/WeKnora/internal/types"
	"sort"
)

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

// ReconcileSlug groups every source by final target and computes one shared
// replay result for each group. The caller commits all migrations inside a
// subject transaction; a single-source legacy state without events is preserved.
func ReconcileSlug(states map[string]FoldState, eventsBySlug map[string][]Event, aliasIndex map[string]string) []SlugMigration {
	groups := map[string][]string{}
	for from := range states {
		to := aliasIndex[from]
		if to != "" && to != from {
			groups[to] = append(groups[to], from)
		}
	}
	var migrations []SlugMigration
	for to, sources := range groups {
		sort.Strings(sources)
		merged := append([]Event{}, eventsBySlug[to]...)
		for _, from := range sources {
			merged = append(merged, eventsBySlug[from]...)
		}
		sort.SliceStable(merged, func(i, j int) bool { return eventLess(merged[i], merged[j]) })
		seen := map[string]bool{}
		var folded FoldState
		count := 0
		for _, e := range merged {
			if e.ID != "" && seen[e.ID] {
				continue
			}
			if e.ID != "" {
				seen[e.ID] = true
			}
			count++
			folded = FoldEvent(folded, e)
		}
		// Preserve a legacy single-source state if its event history is unavailable.
		if len(merged) == 0 && len(sources) == 1 {
			if _, exists := states[to]; !exists {
				folded = states[sources[0]]
			}
		}
		for _, from := range sources {
			migrations = append(migrations, SlugMigration{FromSlug: from, ToSlug: to, State: folded, EventCount: count})
		}
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].FromSlug < migrations[j].FromSlug })
	return migrations
}

func eventLess(a, b Event) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.Before(b.OccurredAt)
	}
	return a.ID < b.ID
}

// canonicalAliasIndex never aliases an existing live node and rejects aliases
// claimed by two different targets instead of letting iteration order decide.
func canonicalAliasIndex(pages []*types.WikiPage) (map[string]bool, map[string]string) {
	live := map[string]bool{}
	aliases := map[string]string{}
	ambiguous := map[string]bool{}
	for _, p := range pages {
		if p != nil && p.Slug != "" {
			live[p.Slug] = true
		}
	}
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		for _, key := range aliasKeysForPage(p) {
			if live[key] || ambiguous[key] {
				continue
			}
			if target, ok := aliases[key]; ok && target != p.Slug {
				delete(aliases, key)
				ambiguous[key] = true
				continue
			}
			aliases[key] = p.Slug
		}
	}
	return live, aliases
}
