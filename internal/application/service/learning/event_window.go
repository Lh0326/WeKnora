package learning

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Business decisions need the full time window, not the timeline's first 500
// rows. In particular, agent traces must not evict human dedup evidence.
// Writers call this inside WithSubject so the window and the write are atomic.
func listEventWindow(ctx context.Context, repo interfaces.LearningRepository, scope interfaces.LearningScope, since time.Time) ([]types.LearningEvent, error) {
	const size = 500
	after, id := since, ""
	var all []types.LearningEvent
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := repo.ListEventsPaged(ctx, scope, after, id, size)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < size {
			break
		}
		last := page[len(page)-1]
		after, id = last.OccurredAt, last.ID
	}
	// Preserve the ListEvents newest-first contract for existing consumers.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, nil
}
