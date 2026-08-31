package learning

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// pageIndexTTL bounds how long a KB's page-reference index stays cached.
// Five minutes keeps repeat questions in one conversation on a warm cache
// while still picking up freshly generated pages reasonably soon — the
// index exists to keep per-answer cost off the wiki tables, not to be a
// source of truth.
const pageIndexTTL = 5 * time.Minute

type pageIndexEntry struct {
	index     *pageRefIndex
	expiresAt time.Time
}

// pageIndexCache caches the per-KB page-reference index (the KB-side half
// of the evidence channel) so a burst of questions does not re-read every
// entity/concept page on each answer. Pattern follows the SSRF outbound
// cache: sync.Map of key → {value, expiry}, checked under the entry's own
// deadline. There is no singleflight here because a stale-margin rebuild
// is idempotent and cheap relative to the answer path it serves.
type pageIndexCache struct {
	m sync.Map // kbID -> pageIndexEntry
}

func (c *pageIndexCache) index(
	ctx context.Context, wikiRepo pageReader, kbID string,
) (*pageRefIndex, error) {
	now := time.Now()
	if v, ok := c.m.Load(kbID); ok {
		entry := v.(pageIndexEntry)
		if now.Before(entry.expiresAt) {
			return entry.index, nil
		}
		c.m.Delete(kbID)
	}

	entities, err := wikiRepo.ListByType(ctx, kbID, "entity")
	if err != nil {
		return nil, err
	}
	concepts, err := wikiRepo.ListByType(ctx, kbID, "concept")
	if err != nil {
		return nil, err
	}
	pages := make([]*types.WikiPage, 0, len(entities)+len(concepts))
	pages = append(pages, entities...)
	pages = append(pages, concepts...)

	index := buildPageRefIndex(pages)
	c.m.Store(kbID, pageIndexEntry{index: index, expiresAt: now.Add(pageIndexTTL)})
	return index, nil
}

// pageReader is the slice of WikiPageRepository the learning layer needs.
// Naming the seam keeps the collector testable with a one-method stub and
// documents exactly which wiki reads the evidence channel depends on.
type pageReader interface {
	ListByType(ctx context.Context, kbID string, pageType string) ([]*types.WikiPage, error)
	ListAll(ctx context.Context, kbID string) ([]*types.WikiPage, error)
	// Folder names humanise the progress units; slug titles humanise the
	// timeline entries — both are read-only projections of wiki metadata.
	ListAllFolders(ctx context.Context, kbID string) ([]*types.WikiFolder, error)
	ListBySlugs(ctx context.Context, kbID string, slugs []string) (map[string]*types.WikiPageLite, error)
	// GetBySlug reads one full page (source refs included) — the quiz
	// serve path resolves evidence chunks back to their documents with it.
	GetBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error)
}
