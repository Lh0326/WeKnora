package learning

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// prefsCacheTTL bounds how long a subject's collection opt-out is trusted
// without re-reading the row. Sixty seconds keeps the per-answer check at
// near-zero cost while an opt-out still takes effect within a minute — the
// same trade the design doc makes for the "进程内缓存，近零开销" promise.
const prefsCacheTTL = 60 * time.Second

type prefsEntry struct {
	disabled  bool
	expiresAt time.Time
}

// learningNoPrefs is the shared "row absent, collection allowed" value; a
// pointer so the cache read path never has to branch on nil twice.
var learningNoPrefs = types.LearningSubjectPrefs{}

// prefsCache caches the per-subject collection opt-out so the hot path
// (every completed answer) does not pay a prefs read. The key is the
// subject alone: the opt-out is a property of the person, and shared-KB
// requests arrive under the KB owner's effective tenant — a tenant-keyed
// cache would let a home-tenant opt-out leak collection in every shared
// workspace. Errors are NOT cached and read as disabled: when consent
// status cannot be verified, the collector stays silent — the
// data-sovereignty-first reading of the opt-out contract.
type prefsCache struct {
	m sync.Map // subjectID -> prefsEntry
}

// prefsReader is the prefs slice of the learning repository the hot path
// needs; the seam exists for the same testability reason as pageReader.
type prefsReader interface {
	GetSubjectPrefs(ctx context.Context, subjectID string) (*types.LearningSubjectPrefs, error)
}

func (c *prefsCache) collectionDisabled(
	ctx context.Context, repo prefsReader, subjectID string,
) bool {
	now := time.Now()
	if v, ok := c.m.Load(subjectID); ok {
		entry := v.(prefsEntry)
		if now.Before(entry.expiresAt) {
			return entry.disabled
		}
		c.m.Delete(subjectID)
	}

	prefs, err := repo.GetSubjectPrefs(ctx, subjectID)
	if err != nil {
		logger.Warnf(ctx, "learning: prefs lookup failed, skipping collection this turn: %v", err)
		return true
	}
	if prefs == nil {
		prefs = &learningNoPrefs
	}
	c.m.Store(subjectID, prefsEntry{disabled: prefs.CollectDisabled, expiresAt: now.Add(prefsCacheTTL)})
	return prefs.CollectDisabled
}

// invalidate drops the cached opt-out the moment the row is written, so
// "delete my profile + stop collecting" takes effect on the very next
// request — data sovereignty must not wait out a cache TTL. The write
// paths (UpdateSettings, DeleteProfile+opt_out) call this after a
// successful upsert.
func (c *prefsCache) invalidate(subjectID string) {
	c.m.Delete(subjectID)
}
