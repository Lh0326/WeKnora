package learning

import (
	"context"
	"sort"
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
	index *pageRefIndex
	// pages is the same entity/concept fetch the index is built from,
	// retained so read paths can share it instead of re-reading the wiki
	// tables on every request. Sorted by slug; callers treat as read-only —
	// the cache hands out shared pointers.
	pages     []*types.WikiPage
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

func (c *pageIndexCache) load(ctx context.Context, wikiRepo pageReader, kbID string) (*pageIndexEntry, error) {
	now := time.Now()
	if v, ok := c.m.Load(kbID); ok {
		entry := v.(pageIndexEntry)
		if now.Before(entry.expiresAt) {
			return &entry, nil
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
	kept := pages[:0]
	for _, p := range pages {
		if p != nil && p.Slug != "" {
			kept = append(kept, p)
		}
	}
	pages = kept
	sort.Slice(pages, func(i, j int) bool { return pages[i].Slug < pages[j].Slug })

	index := buildPageRefIndex(pages)
	entry := pageIndexEntry{index: index, pages: pages, expiresAt: now.Add(pageIndexTTL)}
	c.m.Store(kbID, entry)
	return &entry, nil
}

func (c *pageIndexCache) index(
	ctx context.Context, wikiRepo pageReader, kbID string,
) (*pageRefIndex, error) {
	entry, err := c.load(ctx, wikiRepo, kbID)
	if err != nil {
		return nil, err
	}
	return entry.index, nil
}

// nodes returns the KB's node universe (entity/concept pages, slug-sorted)
// from the same cached fetch the reference index is built from — the read
// paths' every-request ListAll re-read is what this removes.
func (c *pageIndexCache) nodes(
	ctx context.Context, wikiRepo pageReader, kbID string,
) ([]*types.WikiPage, error) {
	entry, err := c.load(ctx, wikiRepo, kbID)
	if err != nil {
		return nil, err
	}
	return entry.pages, nil
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
