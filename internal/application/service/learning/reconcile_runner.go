package learning

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	reconcileInterval     = 24 * time.Hour
	reconcileStartupDelay = 10 * time.Minute
	reconcileRunTimeout   = 30 * time.Second
	// reconcileEventLimit bounds the per-subject event fetch that feeds the
	// replay merge. It deliberately exceeds the API-facing default so a
	// merge rarely works from a truncated history.
	reconcileEventLimit = 10000
)

// ReconcileRunner repairs wiki-rename drift in mastery_states on a slow
// cadence: once a day it walks every folded row whose slug no longer
// resolves, and migrates it onto the live slug named by the page's
// aliases. Structure and lifecycle follow AuditLogRetentionRunner
// (constructor + Start/Stop + ResourceCleaner registration): the repo has
// no cron facility and does not need one for a daily housekeeping pass —
// "no robfig/cron, no asynq" is the documented house style.
type ReconcileRunner struct {
	repo     interfaces.LearningRepository
	wikiRepo pageReader
	// kbRepo decides whether a knowledge base still exists: soft-deleted
	// KBs hide from GetKnowledgeBaseByID, which is exactly the orphan test
	// the sweep below relies on.
	kbRepo interfaces.KnowledgeBaseRepository

	interval     time.Duration
	startupDelay time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	started   atomic.Bool
}

// NewReconcileRunner builds the runner with production cadence.
func NewReconcileRunner(
	repo interfaces.LearningRepository,
	wikiRepo interfaces.WikiPageRepository,
	kbRepo interfaces.KnowledgeBaseRepository,
) *ReconcileRunner {
	return &ReconcileRunner{
		repo:         repo,
		wikiRepo:     wikiRepo,
		kbRepo:       kbRepo,
		interval:     reconcileInterval,
		startupDelay: reconcileStartupDelay,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start launches the runner unless the kill switch is closed. The backfill
// rides along on the startup path (idempotent, so restarts are free),
// which is what makes "enable the feature, get a half-lit map" true
// without a separate trigger surface.
func (r *ReconcileRunner) Start(ctx context.Context, svc interfaces.LearningService) {
	if !learningEnabled() {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		go r.loop(ctx, svc)
	})
}

// Stop asks the loop to exit and waits for it. A runner that never
// started (gate closed) has no goroutine to wait for.
func (r *ReconcileRunner) Stop() {
	r.stopOnce.Do(func() {
		if !r.started.Load() {
			return
		}
		close(r.stopCh)
		<-r.doneCh
	})
}

// Started reports whether the loop is running (test seam).
func (r *ReconcileRunner) Started() bool { return r.started.Load() }

func (r *ReconcileRunner) loop(ctx context.Context, svc interfaces.LearningService) {
	defer close(r.doneCh)

	// A housekeeping goroutine must never take the server down: every pass
	// runs behind a recover that logs and moves on, matching the "collection
	// failures are logged, never felt" contract of the write path.
	runPass := func(name string, fn func(context.Context) error) {
		defer func() {
			if v := recover(); v != nil {
				logger.Errorf(ctx, "learning: %s panicked: %v", name, v)
			}
		}()
		if err := fn(ctx); err != nil {
			logger.Warnf(ctx, "learning: %s failed: %v", name, err)
		}
	}

	runPass("startup backfill", svc.RunBackfill)
	runPass("startup maintenance", svc.RunMaintenance)

	timer := time.NewTimer(r.startupDelay)
	defer timer.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-timer.C:
		}
		runPass("reconcile pass", func(passCtx context.Context) error {
			runCtx, cancel := context.WithTimeout(passCtx, reconcileRunTimeout)
			defer cancel()
			return r.runOnce(runCtx)
		})
		runPass("maintenance pass", func(passCtx context.Context) error {
			maintCtx, cancel := context.WithTimeout(passCtx, reconcileRunTimeout*4)
			defer cancel()
			return svc.RunMaintenance(maintCtx)
		})
		timer.Reset(r.interval)
	}
}

// runOnce walks every folded mastery row, groups it per (tenant, KB),
// rebuilds the live-slug/alias view of that KB's wiki, and applies the
// pure ReconcileSlug decisions to storage. Event history stays untouched:
// events are facts about the past under the name of their day, only the
// folded state follows the node to its new slug.
func (r *ReconcileRunner) runOnce(ctx context.Context) error {
	rows, err := r.repo.ListAllMastery(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	type kbKey struct {
		tenant uint64
		kb     string
	}
	byKB := map[kbKey][]types.MasteryState{}
	for _, row := range rows {
		key := kbKey{row.TenantID, row.KnowledgeBaseID}
		byKB[key] = append(byKB[key], row)
	}

	migrations := 0
	for key, kbRows := range byKB {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Orphan sweep first: a KB that no longer resolves (soft-deleted or
		// gone) has no live pages, and its wiki_pages rows may themselves
		// linger (upstream deletion does not scrub them), which would make
		// the slug reconciliation below treat stale slugs as live. Clean
		// the KB's learning data instead of reconciling it.
		if r.kbRepo != nil {
			if kb, err := r.kbRepo.GetKnowledgeBaseByID(ctx, key.kb); err != nil || kb == nil {
				if err := r.repo.DeleteLearningDataByKB(ctx, key.tenant, key.kb); err != nil {
					logger.Warnf(ctx, "learning: orphan KB sweep failed (kb %s): %v", key.kb, err)
				} else {
					logger.Infof(ctx, "learning: orphan KB sweep removed learning data of deleted kb %s (tenant %d)", key.kb, key.tenant)
				}
				continue
			}
		}
		entities, err := r.wikiRepo.ListByType(ctx, key.kb, "entity")
		if err != nil {
			logger.Warnf(ctx, "learning: reconcile wiki read failed (kb %s): %v", key.kb, err)
			continue
		}
		concepts, err := r.wikiRepo.ListByType(ctx, key.kb, "concept")
		if err != nil {
			logger.Warnf(ctx, "learning: reconcile wiki read failed (kb %s): %v", key.kb, err)
			continue
		}
		pages := append(append([]*types.WikiPage{}, entities...), concepts...)

		live := map[string]bool{}
		aliasIndex := map[string]string{}
		for _, p := range pages {
			if p == nil || p.Slug == "" {
				continue
			}
			live[p.Slug] = true
			for _, candidate := range aliasKeysForPage(p) {
				if candidate != p.Slug && !live[candidate] {
					aliasIndex[candidate] = p.Slug
				}
			}
		}

		// Group rows per subject so the pure reconcile sees one person's
		// whole state at a time.
		bySubject := map[string][]types.MasteryState{}
		for _, row := range kbRows {
			bySubject[row.SubjectID] = append(bySubject[row.SubjectID], row)
		}
		subjects := make([]string, 0, len(bySubject))
		for s := range bySubject {
			subjects = append(subjects, s)
		}
		sort.Strings(subjects)

		for _, subject := range subjects {
			scope := interfaces.LearningScope{TenantID: key.tenant, SubjectID: subject, KnowledgeBaseID: key.kb}
			// Live rows join the map too: a stale slug migrating onto a
			// live target that already carries state must take the
			// replay-merge branch inside ReconcileSlug, not overwrite it.
			// Live rows themselves never migrate — aliasIndex keys are
			// never live slugs — so they are inert as sources.
			states := map[string]FoldState{}
			hasStale := false
			for _, row := range bySubject[subject] {
				if !live[row.Slug] {
					hasStale = true
				}
				states[row.Slug] = StateFromModel(&row)
			}
			if !hasStale {
				continue
			}

			eventsBySlug := map[string][]Event{}
			if events, err := r.repo.ListEvents(ctx, scope, time.Time{}, reconcileEventLimit); err == nil {
				for _, ev := range events {
					eventsBySlug[ev.Slug] = append(eventsBySlug[ev.Slug],
						Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
				}
			} else {
				logger.Warnf(ctx, "learning: reconcile events read failed (subject %s): %v", subject, err)
				continue
			}

			for _, m := range ReconcileSlug(states, eventsBySlug, aliasIndex) {
				row := &types.MasteryState{
					TenantID:        key.tenant,
					SubjectID:       subject,
					KnowledgeBaseID: key.kb,
					Slug:            m.ToSlug,
				}
				m.State.ApplyTo(row)
				if err := r.repo.UpsertMastery(ctx, row); err != nil {
					logger.Warnf(ctx, "learning: reconcile upsert failed (%s -> %s): %v", m.FromSlug, m.ToSlug, err)
					continue
				}
				if err := r.repo.DeleteMastery(ctx, scope, m.FromSlug); err != nil {
					logger.Warnf(ctx, "learning: reconcile retire failed (%s): %v", m.FromSlug, err)
					continue
				}
				migrations++
			}
		}
	}
	if migrations > 0 {
		logger.Infof(ctx, "learning: reconcile migrated %d mastery rows onto live slugs", migrations)
	}
	return nil
}

// aliasKeysForPage produces the deterministic set of keys under which a
// stale mastery slug may refer to this live page: the raw alias, the
// type-prefixed alias, and the type-prefixed kebab form (wiki slugs are
// the kebab of the title, so an alias stored as a title still matches its
// slug-shaped ghost). The kebab rules mirror wiki_ingest's slugify, which
// is package-private and cannot be imported from here.
func aliasKeysForPage(p *types.WikiPage) []string {
	pageType := ""
	if i := strings.IndexByte(p.Slug, '/'); i > 0 {
		pageType = p.Slug[:i]
	}
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, alias := range p.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		add(alias)
		if pageType != "" {
			add(pageType + "/" + alias)
			add(pageType + "/" + kebabSlug(alias))
		}
	}
	return keys
}

// kebabSlug lowercases and hyphenates the way wiki slugs are formed.
func kebabSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '/':
			b.WriteRune(r)
			lastHyphen = r == '-'
		case r == ' ' || r == '_':
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		case r >= 0x4E00 && r <= 0x9FFF: // keep CJK as-is, like wiki slugify
			b.WriteRune(r)
			lastHyphen = false
		default:
			// dropped, like wiki slugify
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}
