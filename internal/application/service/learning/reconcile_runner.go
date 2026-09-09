package learning

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
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
	// Skips join the walk: a subject who only ever skipped a node (never
	// folded mastery — the "看一眼就会了" archetype) must still get alias
	// repair, or a rename silently resurrectes the retired recommendation.
	skipRows, err := r.repo.ListAllSkips(ctx)
	if err != nil {
		return err
	}
	// Edges are KB-shared inventory: their KBs join the discovery union so
	// a KB whose subjects' rows are all gone still gets its edges repaired.
	edgeRows, err := r.repo.ListAllEdges(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 && len(skipRows) == 0 && len(edgeRows) == 0 {
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
	kbSkips := map[kbKey]map[string]map[string]time.Time{}
	for _, row := range skipRows {
		key := kbKey{row.TenantID, row.KnowledgeBaseID}
		if kbSkips[key] == nil {
			kbSkips[key] = map[string]map[string]time.Time{}
		}
		if kbSkips[key][row.SubjectID] == nil {
			kbSkips[key][row.SubjectID] = map[string]time.Time{}
		}
		kbSkips[key][row.SubjectID][row.Slug] = row.CreatedAt
	}
	// The KB universe is the union: a skip-only KB has no mastery rows but
	// still owes its users alias repair (and the orphan sweep).
	kbKeys := make([]kbKey, 0, len(byKB)+len(kbSkips))
	inList := map[kbKey]bool{}
	for key := range byKB {
		kbKeys = append(kbKeys, key)
		inList[key] = true
	}
	for _, e := range edgeRows {
		key := kbKey{e.TenantID, e.KnowledgeBaseID}
		if !inList[key] {
			kbKeys = append(kbKeys, key)
			inList[key] = true
		}
	}
	for key := range kbSkips {
		if !inList[key] {
			kbKeys = append(kbKeys, key)
			inList[key] = true
		}
	}
	sort.Slice(kbKeys, func(i, j int) bool {
		if kbKeys[i].tenant != kbKeys[j].tenant {
			return kbKeys[i].tenant < kbKeys[j].tenant
		}
		return kbKeys[i].kb < kbKeys[j].kb
	})

	migrations := 0
	repaired := 0
	skipsMoved := 0
	edgesMoved := 0
	edgesDropped := 0
	for _, key := range kbKeys {
		kbRows := byKB[key]
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Orphan sweep first: a KB that no longer resolves (soft-deleted or
		// gone) has no live pages, and its wiki_pages rows may themselves
		// linger (upstream deletion does not scrub them), which would make
		// the slug reconciliation below treat stale slugs as live. Clean
		// the KB's learning data instead of reconciling it.
		if r.kbRepo != nil {
			kb, err := r.kbRepo.GetKnowledgeBaseByID(ctx, key.kb)
			if err != nil && !errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
				logger.Warnf(ctx, "learning: KB lookup failed, retaining learning data (kb %s): %v", key.kb, err)
				continue
			}
			if kb == nil || errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
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

		// Edge repair: prerequisite edges whose endpoints no longer resolve
		// (page deleted, or renamed so the slug drifted) gate their targets
		// forever - the reader filters them, and here the storage heals:
		// endpoint slugs follow the alias index, and an edge that resolves
		// nowhere on either side is dropped outright.
		if edges, err := r.repo.ListEdges(ctx, key.tenant, key.kb); err != nil {
			logger.Warnf(ctx, "learning: reconcile edges read failed (kb %s): %v", key.kb, err)
		} else {
			for _, e := range edges {
				if e.Relation != types.LearningEdgePrerequisite {
					continue
				}
				resolve := func(slug string) (string, bool) {
					if live[slug] {
						return slug, true
					}
					if to, ok := aliasIndex[slug]; ok {
						return to, true
					}
					return "", false
				}
				from, fromOK := resolve(e.FromSlug)
				to, toOK := resolve(e.ToSlug)
				switch {
				case !fromOK || !toOK:
					if err := r.repo.DeleteEdge(ctx, key.tenant, key.kb, e.FromSlug, e.ToSlug); err != nil {
						logger.Warnf(ctx, "learning: reconcile edge drop failed (%s->%s): %v", e.FromSlug, e.ToSlug, err)
					} else {
						edgesDropped++
					}
				case from != e.FromSlug || to != e.ToSlug:
					if err := r.repo.UpsertEdge(ctx, &types.LearningEdge{
						TenantID: key.tenant, KnowledgeBaseID: key.kb,
						FromSlug: from, ToSlug: to,
						Relation:   types.LearningEdgePrerequisite,
						Confidence: e.Confidence, Source: e.Source,
					}); err != nil {
						logger.Warnf(ctx, "learning: reconcile edge move failed (%s->%s): %v", e.FromSlug, e.ToSlug, err)
					} else if err := r.repo.DeleteEdge(ctx, key.tenant, key.kb, e.FromSlug, e.ToSlug); err != nil {
						logger.Warnf(ctx, "learning: reconcile edge retire failed (%s->%s): %v", e.FromSlug, e.ToSlug, err)
					} else {
						edgesMoved++
					}
				}
			}
		}

		// Group rows per subject so the pure reconcile sees one person's
		// whole state at a time. Skip-only subjects join the walk too.
		bySubject := map[string][]types.MasteryState{}
		for _, row := range kbRows {
			bySubject[row.SubjectID] = append(bySubject[row.SubjectID], row)
		}
		subjects := make([]string, 0, len(bySubject))
		for s := range bySubject {
			subjects = append(subjects, s)
		}
		for s := range kbSkips[key] {
			if _, ok := bySubject[s]; !ok {
				subjects = append(subjects, s)
			}
		}
		sort.Strings(subjects)

		for _, subject := range subjects {
			scope := interfaces.LearningScope{TenantID: key.tenant, SubjectID: subject, KnowledgeBaseID: key.kb}
			// Skip rows ride the same alias repair and run for EVERY subject:
			// a standing "已掌握，不再推荐" must follow the node it names, or
			// a rename silently resurrects the recommendation the user
			// retired — including for subjects with no folded mastery at
			// all (the skip archetype: seen it, known it, moved on).
			staleSkips := make([]string, 0, len(kbSkips[key][subject]))
			for from := range kbSkips[key][subject] {
				staleSkips = append(staleSkips, from)
			}
			// Two stale slugs can alias onto one live page (a page that
			// absorbed several renames); the EARLIEST declaration must win the
			// AddSkip conflict, so the iteration order is fixed instead of left
			// to map randomness.
			sort.Slice(staleSkips, func(i, j int) bool {
				a, b := kbSkips[key][subject][staleSkips[i]], kbSkips[key][subject][staleSkips[j]]
				if !a.Equal(b) {
					return a.Before(b)
				}
				return staleSkips[i] < staleSkips[j]
			})
			for _, from := range staleSkips {
				createdAt := kbSkips[key][subject][from]
				to, ok := aliasIndex[from]
				if !ok || to == from {
					continue
				}
				if err := r.repo.AddSkip(ctx, scope, to, createdAt); err != nil {
					logger.Warnf(ctx, "learning: reconcile skip move failed (%s -> %s): %v", from, to, err)
					continue
				}
				if err := r.repo.RemoveSkip(ctx, scope, from); err != nil {
					logger.Warnf(ctx, "learning: reconcile skip retire failed (%s): %v", from, err)
					continue
				}
				skipsMoved++
			}
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

			events, err := r.repo.ListEvents(ctx, scope, time.Time{}, reconcileEventLimit)
			if err != nil {
				logger.Warnf(ctx, "learning: reconcile events read failed (subject %s): %v", subject, err)
				continue
			}
			// Never rebuild OR merge from a possibly truncated suffix. A
			// paginated replay is required for these large histories.
			if len(events) >= reconcileEventLimit {
				logger.Warnf(ctx, "learning: event history capped; skipping fold and slug merge for subject %s", subject)
				continue
			}
			repaired += r.repairFoldDrift(ctx, scope, states, events, live, aliasIndex)
			if !hasStale {
				continue
			}

			eventsBySlug := map[string][]Event{}
			for _, ev := range events {
				eventsBySlug[ev.Slug] = append(eventsBySlug[ev.Slug],
					Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
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
	if skipsMoved > 0 {
		logger.Infof(ctx, "learning: reconcile moved %d skip rows onto live slugs", skipsMoved)
	}
	if edgesMoved > 0 || edgesDropped > 0 {
		logger.Infof(ctx, "learning: reconcile repaired %d and dropped %d prerequisite edges", edgesMoved, edgesDropped)
	}
	if repaired > 0 {
		logger.Infof(ctx, "learning: reconcile repaired %d drifted mastery folds by replay", repaired)
	}
	return nil
}

// repairFoldDrift audits every persisted fold against a from-scratch replay
// of the subject's nonzero-weight events and rewrites what drifted.
//
// The write path appends an event and folds it in two steps with no shared
// transaction: a crash (or a lost multi-writer race) between them leaves the
// event stored but its contribution missing from mastery_states — until now
// permanently, because the reconcile only repaired slug renames, never the
// fold itself. The replay is the same FoldEvent the write path runs, so the
// audit converges folds to exactly what a clean sequential history would
// have produced.
//
// Scope, deliberately narrow: only groups whose rows already sit on their
// canonical live slug. A group still holding a stale-slug row is the rename
// migration's to rebuild (its merge branch is itself a replay); auditing it
// now would fight the move, and the audit catches survivors next pass.
// Subjects whose events hit the fetch cap are skipped upstream. A subject
// with NO folded rows reaches this walk only through the skip-list union
// (skip-only subjects), so a very first lost fold still waits for the
// node's next event — the honest boundary of a daily pass rooted in folded
// rows.
func (r *ReconcileRunner) repairFoldDrift(
	ctx context.Context,
	scope interfaces.LearningScope,
	states map[string]FoldState,
	events []types.LearningEvent,
	live map[string]bool,
	aliasIndex map[string]string,
) int {
	// Group events by canonical slug: a stale slug's events fold onto the
	// live slug its alias names.
	grouped := map[string][]Event{}
	for _, ev := range events {
		target := ev.Slug
		if !live[ev.Slug] {
			if to, ok := aliasIndex[ev.Slug]; ok {
				target = to
			}
		}
		grouped[target] = append(grouped[target], Event{Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt})
	}
	pending := map[string]bool{}
	for slug := range states {
		if !live[slug] {
			if to, ok := aliasIndex[slug]; ok {
				pending[to] = true
			}
		}
	}

	targets := make([]string, 0, len(grouped))
	for target := range grouped {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	repaired := 0
	for _, target := range targets {
		if pending[target] {
			continue
		}
		evs := grouped[target]
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].OccurredAt.Before(evs[j].OccurredAt) })
		var expected FoldState
		for _, e := range evs {
			if e.Weight != 0 { // zero-weight events (deduped re-reads, unsure answers) never fold
				expected = FoldEvent(expected, e)
			}
		}
		persisted, hasRow := states[target]
		if hasRow && foldMatches(persisted, expected) {
			continue
		}
		if !hasRow && expected.EvidenceCount == 0 {
			continue
		}
		row := &types.MasteryState{
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: scope.KnowledgeBaseID,
			Slug:            target,
		}
		expected.ApplyTo(row)
		if err := r.repo.UpsertMastery(ctx, row); err != nil {
			logger.Warnf(ctx, "learning: fold drift repair failed (%s): %v", target, err)
			continue
		}
		repaired++
	}
	return repaired
}

// foldMatches compares a persisted fold with its replay expectation. Logit
// and stability tolerate float-association noise (incremental appends and
// the sorted replay add the same weights in different orders); counters and
// seen-bounds must match exactly.
func foldMatches(persisted, expected FoldState) bool {
	const eps = 1e-9
	if persisted.EvidenceCount != expected.EvidenceCount ||
		persisted.PositiveCount != expected.PositiveCount ||
		persisted.NegativeCount != expected.NegativeCount {
		return false
	}
	if math.Abs(persisted.Logit-expected.Logit) > eps ||
		math.Abs(persisted.Stability-expected.Stability) > eps {
		return false
	}
	return persisted.FirstSeenAt.Equal(expected.FirstSeenAt) &&
		persisted.LastEvidenceAt.Equal(expected.LastEvidenceAt)
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
