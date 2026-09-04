package learning

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrNoLearningScope mirrors the memory subsystem's scope error: the
// handler maps it to 401, because a missing principal is never the
// client's payload's fault but always the client's identity's.
var ErrNoLearningScope = errors.New("learning: no subject scope in context")

// ErrQuizNotFound marks a quiz item id that does not resolve in the KB.
var ErrQuizNotFound = errors.New("learning: quiz item not found")

// ErrWikiReadTarget marks a wiki-read signal aimed at a slug that is not a
// knowledge node (entity/concept page) of the KB.
var ErrWikiReadTarget = errors.New("learning: wiki read target is not a knowledge node")

// resolveReadScope derives the read scope from the request context alone
// (Principal.StorageID()), exactly like every write path. No handler
// parameter can select another person's data.
func resolveReadScope(ctx context.Context, kbID string) (interfaces.LearningScope, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return interfaces.LearningScope{}, ErrNoLearningScope
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return interfaces.LearningScope{}, ErrNoLearningScope
	}
	subjectID := principal.StorageID()
	if subjectID == "" {
		return interfaces.LearningScope{}, ErrNoLearningScope
	}
	return interfaces.LearningScope{TenantID: tenantID, SubjectID: subjectID, KnowledgeBaseID: kbID}, nil
}

// Read-path DTOs live on the interfaces package so handlers depend on
// interfaces only, the same contract shape as the memory subsystem.
type (
	LearningProgress     = interfaces.LearningProgress
	LearningUnitProgress = interfaces.LearningUnitProgress
	TodaySummary         = interfaces.TodaySummary
	MasteryView          = interfaces.MasteryView
	QuizQuestion         = interfaces.QuizQuestion
	QuizSourceDoc        = interfaces.QuizSourceDoc
	AnswerResult         = interfaces.AnswerResult
	TimelineItem         = interfaces.TimelineItem
	ExportPayload        = interfaces.ExportPayload
	ExportKBSummary      = interfaces.ExportKBSummary
	LearningSettings     = interfaces.LearningSettings
	MasteryOverlayEntry  = interfaces.MasteryOverlayEntry
)

// GetProgress assembles the tab header from the KB's node pages and the
// caller's folds.
func (s *Service) GetProgress(ctx context.Context, kbID string) (*LearningProgress, error) {
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
		logger.Warnf(ctx, "learning: progress mastery read failed (kb %s): %v", kbID, err)
	} else {
		for i := range rows {
			states[rows[i].Slug] = StateFromModel(&rows[i])
		}
	}

	now := time.Now()
	direct := s.directFacts(ctx, scope)
	progress := &LearningProgress{Levels: map[string]int{}}
	// Folder names are the human-readable labels of the rolled-up units;
	// a missing folder (deleted between reads) degrades to the root label.
	folderNames := map[string]string{}
	if folders, err := s.wikiRepo.ListAllFolders(ctx, kbID); err == nil {
		for _, f := range folders {
			if f != nil && f.ID != "" {
				folderNames[f.ID] = f.Name
			}
		}
	} else {
		logger.Warnf(ctx, "learning: progress folder read failed (kb %s): %v", kbID, err)
	}
	unitIndex := map[string]*LearningUnitProgress{}
	for _, p := range pages {
		progress.TotalNodes++
		lv := gatedAnchoredLevel(states[p.Slug], direct[p.Slug], now)
		progress.Levels[string(lv.Level)]++
		if lv.Level != LevelUnseen {
			progress.LitNodes++
		}
		unit := unitIndex[p.FolderID]
		if unit == nil {
			unit = &LearningUnitProgress{FolderID: p.FolderID, FolderName: folderNames[p.FolderID]}
			unitIndex[p.FolderID] = unit
		}
		unit.Total++
		if lv.Level != LevelUnseen {
			unit.Lit++
		}
	}
	ids := make([]string, 0, len(unitIndex))
	for id := range unitIndex {
		ids = append(ids, id)
	}
	ids = sortedStrings(ids)
	for _, id := range ids {
		progress.Units = append(progress.Units, *unitIndex[id])
	}
	progress.Today = s.todaySummary(ctx, scope, states, now)
	return progress, nil
}

// todaySummary derives the daily digest from the same rows the tab already
// reads: today's events (answers and their outcomes), nodes whose first
// evidence landed today, and the consecutive-day streak. Collection-free by
// construction — it summarises events that were already stored.
func (s *Service) todaySummary(
	ctx context.Context, scope interfaces.LearningScope, states map[string]FoldState, now time.Time,
) *TodaySummary {
	today := &TodaySummary{}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	// Bug fix: an unbounded ListEvents silently caps at 500 rows (repo
	// default), truncating streak and quizStruggled for heavy users. Bound
	// the lookback to a year — no streak or struggle signal survives longer.
	streakLookback := 365 * 24 * time.Hour
	events, err := s.repo.ListEvents(ctx, scope, now.Add(-streakLookback), 0)
	if err != nil {
		logger.Warnf(ctx, "learning: today summary read failed (kb %s): %v", scope.KnowledgeBaseID, err)
		return today
	}
	daySeen := map[string]bool{}
	for _, ev := range events {
		d := ev.OccurredAt.Format("2006-01-02")
		if !daySeen[d] {
			daySeen[d] = true
		}
		if !ev.OccurredAt.Before(start) {
			switch ev.Type {
			case types.LearningEventQuizCorrect, types.LearningEventQuizWrong, types.LearningEventQuizUnsure:
				today.Answers++
				if ev.Type == types.LearningEventQuizCorrect {
					today.CorrectCount++
				}
			}
		}
	}
	for _, st := range states {
		if !st.FirstSeenAt.IsZero() && !st.FirstSeenAt.Before(start) && st.EvidenceCount > 0 {
			today.LitToday++
		}
	}
	// Streak: consecutive days ending today (or yesterday, so this morning
	// does not read as a broken streak before the first action).
	// Bug fix: daySeen must contain ONLY days with actual events — seeding
	// today unconditionally manufactured streak=1 for never-active users.
	day := start
	if !daySeen[day.Format("2006-01-02")] {
		day = day.AddDate(0, 0, -1)
	}
	for daySeen[day.Format("2006-01-02")] {
		today.StreakDays++
		day = day.AddDate(0, 0, -1)
	}
	return today
}

// ListMasteryView derives every node's current level and decayed
// probability at read time. The view covers the whole node universe —
// including never-touched pages as unseen entries — so a consumer (tier
// list, graph hover card) never meets a node the map cannot describe;
// progress counts and map rows can no longer disagree.
func (s *Service) ListMasteryView(ctx context.Context, kbID string) ([]MasteryView, error) {
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
		logger.Warnf(ctx, "learning: mastery read failed (kb %s): %v", kbID, err)
	} else {
		for i := range rows {
			states[rows[i].Slug] = StateFromModel(&rows[i])
		}
	}
	now := time.Now()
	direct := s.directFacts(ctx, scope)
	// Raw last activity per slug (zero-weight touches included) — the
	// constellation's "studied within 48h" marker must see deduped re-reads
	// and unsure answers, which never enter the fold. Failure is soft: the
	// view falls back to the folded last_evidence_at, exactly the old
	// behavior.
	activity := map[string]time.Time{}
	if m, err := s.repo.ListLastActivity(ctx, scope); err != nil {
		logger.Warnf(ctx, "learning: last-activity read failed (kb %s): %v", kbID, err)
	} else {
		activity = m
	}
	selfAssess := map[string]interfaces.SelfAssessMark{}
	if m, err := s.repo.ListSelfAssess(ctx, scope); err != nil {
		logger.Warnf(ctx, "learning: self-assess read failed (kb %s): %v", kbID, err)
	} else {
		selfAssess = m
	}
	out := make([]MasteryView, 0, len(pages))
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		state := states[p.Slug]
		lv := gatedAnchoredLevel(state, direct[p.Slug], now)
		lastActivity := activity[p.Slug]
		if lastActivity.IsZero() || lastActivity.Before(state.LastEvidenceAt) {
			lastActivity = state.LastEvidenceAt
		}
		var mark *interfaces.SelfAssessMark
		if m, ok := selfAssess[p.Slug]; ok {
			mark = &m
		}
		pEff := EffectiveP(state, now)
		// Bug fix: a node with zero evidence and zero timestamps has a raw
		// sigmoid(0)=0.5 — "a statement nobody earned" (anchoredLevel's own
		// words). The mastery map must report p_eff=0 and tier_progress=0
		// for never-touched nodes, not a half-full progress bar.
		if state.EvidenceCount == 0 && state.LastEvidenceAt.IsZero() {
			pEff = 0
		}
		out = append(out, MasteryView{
			Slug: p.Slug, Level: string(lv.Level), Title: p.Title,
			PEff: pEff, EvidenceCount: state.EvidenceCount,
			LowConfidence: lv.LowConfidence, LastEvidenceAt: state.LastEvidenceAt,
			LastActivityAt: lastActivity, SelfAssess: mark,
			TierProgress:   TierProgress(lv.Level, pEff),
			NextTierHint:   NextTierHint(lv.Level, direct[p.Slug], now),
		})
	}
	// Deterministic order: lit tiers first (mastered → touched), unseen
	// last, titles within a tier — the same reading order as the header's
	// tier cards.
	sort.Slice(out, func(i, j int) bool {
		ri, rj := tierSortRank(out[i].Level), tierSortRank(out[j].Level)
		if ri != rj {
			return ri > rj
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// tierSortRank orders the display tiers for the mastery map: mastered
// first, unseen last.
func tierSortRank(level string) int {
	switch level {
	case string(LevelMastered):
		return 3
	case string(LevelFamiliar):
		return 2
	case string(LevelTouched):
		return 1
	default:
		return 0
	}
}

// Recommend produces the "look" cards for the caller.
func (s *Service) Recommend(ctx context.Context, kbID string, limit int) ([]Recommendation, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	pages, err := s.nodePages(ctx, kbID)
	if err != nil {
		return nil, err
	}
	edges, err := s.repo.ListEdges(ctx, scope.TenantID, kbID)
	if err != nil {
		return nil, err
	}
	states := map[string]FoldState{}
	if rows, err := s.repo.ListMastery(ctx, scope); err != nil {
		logger.Warnf(ctx, "learning: recommend mastery read failed (kb %s): %v", kbID, err)
	} else {
		for i := range rows {
			states[rows[i].Slug] = StateFromModel(&rows[i])
		}
	}

	// Affinity: pages whose SourceRefs cite the docs this person keeps
	// working from get the relevance nudge.
	affinity := map[string]bool{}
	if affinities, err := s.repo.ListDocAffinityByScope(ctx, scope.TenantID, scope.SubjectID); err != nil {
		logger.Warnf(ctx, "learning: recommend affinity read failed: %v", err)
	} else {
		docs := map[string]bool{}
		for _, row := range affinities {
			if row.Hits >= types.MemoryDocAffinityMinHits {
				docs[row.KnowledgeID] = true
			}
		}
		for _, p := range pages {
			for _, ref := range p.SourceRefs {
				if docID := sourceRefDocID(ref); docID != "" && docs[docID] {
					affinity[p.Slug] = true
					break
				}
			}
		}
	}

	quizCount := map[string]int{}
	allItems, itemsErr := s.repo.ListQuizItemsByKB(ctx, scope.TenantID, kbID)
	if itemsErr != nil {
		logger.Warnf(ctx, "learning: recommend quiz read failed (kb %s): %v", kbID, err)
		allItems = nil
	}
	for _, it := range allItems {
		if it.Status == types.LearningQuizStatusActive {
			quizCount[it.Slug]++
		}
	}
	hasQuiz := make(map[string]bool, len(quizCount))
	for slug, n := range quizCount {
		hasQuiz[slug] = n > 0
	}

	// Direct-evidence struggle: slugs this subject answered wrong at least
	// once — the strongest remedial signal, one indexed event scan.
	quizStruggled := map[string]bool{}
	// Bug fix: bound the lookback — unbounded ListEvents silently caps at
	// 500 rows, hiding older wrong-answer slugs from the struggle signal.
	if history, err := s.repo.ListEvents(ctx, scope, time.Now().Add(-touchLookback), 0); err != nil {
		logger.Warnf(ctx, "learning: recommend history read failed (kb %s): %v", kbID, err)
	} else {
		for _, ev := range history {
			if ev.Type == types.LearningEventQuizWrong {
				quizStruggled[ev.Slug] = true
			}
		}
	}

	// Self-assessment marks (latest per slug, visible-window bounded by the
	// repo): the freshest explicit user claim must shape reason attribution —
	// without it a "题目太简单" demotion keeps the stale 错题重练 label built
	// from old wrong answers the user just re-contextualised.
	selfAssess := map[string]interfaces.SelfAssessMark{}
	if marks, err := s.repo.ListSelfAssess(ctx, scope); err != nil {
		logger.Warnf(ctx, "learning: recommend self-assess read failed (kb %s): %v", kbID, err)
	} else {
		selfAssess = marks
	}

	direct := s.directFacts(ctx, scope)
	recs := recommendNodes(recommendInput{
		Pages: pages, Edges: edges, States: states, Affinity: affinity, HasQuiz: hasQuiz,
		QuizStruggled: quizStruggled, DirectFacts: direct, SelfAssess: selfAssess,
	}, time.Now(), rand.New(rand.NewSource(time.Now().UnixNano())), limit)

	// Display extras, derived here so the pure recommender stays a pure
	// policy: the node's folder, its active quiz count, its current tier.
	folderBySlug := map[string]string{}
	for _, p := range pages {
		folderBySlug[p.Slug] = p.FolderID
	}
	folderNames := map[string]string{}
	if folders, err := s.wikiRepo.ListAllFolders(ctx, kbID); err == nil {
		for _, f := range folders {
			if f != nil && f.ID != "" {
				folderNames[f.ID] = f.Name
			}
		}
	} else {
		logger.Warnf(ctx, "learning: recommend folder read failed (kb %s): %v", kbID, err)
	}
	now := time.Now()
	for i := range recs {
		slug := recs[i].Slug
		recs[i].FolderName = folderNames[folderBySlug[slug]]
		recs[i].QuizCount = quizCount[slug]
		if state, ok := states[slug]; ok {
			lv := gatedAnchoredLevel(state, direct[slug], now)
			recs[i].Level = string(lv.Level)
			// Explainability: the numbers behind the tier, plus the faded
			// distinction (unseen tier WITH history) so the client can say
			// 已淡化 instead of the misleading 未接触.
			recs[i].PEff = EffectiveP(state, now)
			recs[i].EvidenceCount = state.EvidenceCount
			recs[i].PositiveCount = state.PositiveCount
			recs[i].NegativeCount = state.NegativeCount
			recs[i].Faded = lv.Level == LevelUnseen && state.EvidenceCount > 0
			recs[i].TierProgress = TierProgress(lv.Level, recs[i].PEff)
			recs[i].NextTierHint = NextTierHint(lv.Level, direct[slug], now)
		} else {
			// Bug fix: explore/bypass/blind-spot picks are state-less by
			// definition — the level must be the explicit "unseen" enum,
			// not empty (client tier maps and fade logic expect the same
			// enumeration the /map endpoint returns).
			recs[i].Level = string(LevelUnseen)
			recs[i].NextTierHint = HintFirstTouch
		}
	}
	return recs, nil
}

// TakeQuiz serves the active questions of one node, preferring ones the
// caller has not answered yet, and strips the answer material. Source
// documents are resolved for every served item so the client can trace a
// question back to the document its evidence chunks live in.
func (s *Service) TakeQuiz(ctx context.Context, kbID, slug string) ([]QuizQuestion, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListQuizItems(ctx, scope.TenantID, kbID, slug)
	if err != nil {
		return nil, err
	}
	attempts, err := s.repo.ListAttempts(ctx, scope, slug)
	if err != nil {
		logger.Warnf(ctx, "learning: quiz attempts read failed (slug %s): %v", slug, err)
		attempts = nil // preference, not correctness
	}
	seen := map[string]bool{}
	for _, a := range attempts {
		seen[a.QuizItemID] = true
	}

	var fresh, used []QuizQuestion
	for _, it := range items {
		if it.Status != types.LearningQuizStatusActive {
			continue
		}
		q := QuizQuestion{ID: it.ID, Question: it.Question, Options: map[string]string{}, ChunkRefs: []string(it.ChunkRefs)}
		for k, v := range it.Options {
			q.Options[k] = v
		}
		q.SourceDocs = s.resolveSourceDocs(ctx, scope, kbID, slug, &it)
		if seen[it.ID] {
			used = append(used, q)
		} else {
			fresh = append(fresh, q)
		}
	}
	return append(fresh, used...), nil
}

// resolveSourceDocs maps one item's evidence chunks back to their source
// documents: chunk → knowledge_id via the chunk repository (one indexed
// batch read), title via the page's own SourceRefs ("<id>|<title>") with
// the knowledge repository as the bare-id fallback. Deterministic,
// read-only; unresolved chunks are skipped rather than guessed.
func (s *Service) resolveSourceDocs(
	ctx context.Context, scope interfaces.LearningScope, kbID, slug string, item *types.LearningQuizItem,
) []QuizSourceDoc {
	if len(item.ChunkRefs) == 0 || s.chunkRepo == nil {
		return nil
	}
	chunks, err := s.chunkRepo.ListChunksByID(ctx, scope.TenantID, []string(item.ChunkRefs))
	if err != nil {
		logger.Warnf(ctx, "learning: quiz source chunk read failed (slug %s): %v", slug, err)
		return nil
	}
	titles := map[string]string{}
	if p, err := s.wikiRepo.GetBySlug(ctx, kbID, slug); err == nil && p != nil {
		for _, ref := range p.SourceRefs {
			if id, title := sourceRefParts(ref); id != "" {
				titles[id] = title
			}
		}
	}
	type docAcc struct {
		doc   QuizSourceDoc
		order int
	}
	acc := map[string]*docAcc{}
	for _, c := range chunks {
		if c == nil || c.KnowledgeID == "" {
			continue
		}
		a := acc[c.KnowledgeID]
		if a == nil {
			a = &docAcc{doc: QuizSourceDoc{KnowledgeID: c.KnowledgeID, Title: titles[c.KnowledgeID]}, order: len(acc)}
			acc[c.KnowledgeID] = a
		}
		a.doc.ChunkCount++
	}
	out := make([]QuizSourceDoc, 0, len(acc))
	for _, a := range acc {
		out = append(out, a.doc)
	}
	// Titles the page's SourceRefs could not supply (bare-id refs) fall
	// back to the knowledge repository, so a user never meets a raw UUID.
	for i := range out {
		if out[i].Title != "" || s.knowledgeRepo == nil {
			continue
		}
		if k, err := s.knowledgeRepo.GetKnowledgeByID(ctx, scope.TenantID, out[i].KnowledgeID); err == nil && k != nil && k.Title != "" {
			out[i].Title = k.Title
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChunkCount != out[j].ChunkCount {
			return out[i].ChunkCount > out[j].ChunkCount
		}
		return out[i].KnowledgeID < out[j].KnowledgeID
	})
	return out
}

// SubmitAnswer grades deterministically, records the attempt and folds the
// event — unless the subject opted out of collection, in which case the
// verdict and explanation are still served but nothing is stored.
func (s *Service) SubmitAnswer(ctx context.Context, kbID, itemID, chosenKey string) (*AnswerResult, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	// Locate the item (and its slug) without trusting the client for it.
	item := s.findQuizItem(ctx, scope, itemID)
	if item == nil {
		return nil, ErrQuizNotFound
	}

	// Repeat-attempt decay counts only attempts on this item inside the
	// re-ask window: rapid re-answers still converge to nothing (the
	// anti-farm guarantee), but practice spaced across days earns the full
	// weight again — the spacing effect, so a learner who returns tomorrow
	// is never trapped in near-zero-weight drills.
	var priorAttempts []types.LearningQuizAttempt
	prior := 0
	if attempts, err := s.repo.ListAttempts(ctx, scope, item.Slug); err == nil {
		priorAttempts = attempts
		windowStart := time.Now().Add(-ReAskWindowHours * time.Hour)
		for _, a := range attempts {
			if a.QuizItemID == itemID && a.AnsweredAt.After(windowStart) {
				prior++
			}
		}
	}
	grade, err := GradeQuiz(item.CorrectKey, chosenKey, prior)
	if err != nil {
		return nil, err
	}
	result := &AnswerResult{
		Correct: grade.Correct, CorrectKey: item.CorrectKey,
		Explanation: item.Explanation, ChunkRefs: []string(item.ChunkRefs),
		Unsure: grade.EventType == types.LearningEventQuizUnsure,
	}

	if s.prefs.collectionDisabled(ctx, s.repo, scope.TenantID, scope.SubjectID) {
		return result, nil // opted out: verdict served, nothing stored
	}

	now := time.Now()
	// The fast-feedback bracket: p_eff immediately before this answer.
	facts := CollectDirectFacts(priorAttempts)[item.Slug]
	var before *float64
	if row, err := s.repo.GetMastery(ctx, scope, item.Slug); err == nil && row != nil {
		if p := EffectiveP(StateFromModel(row), now); p > 0 {
			before = &p
		}
	}
	if err := s.repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
		TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
		QuizItemID: itemID, Slug: item.Slug,
		ChosenKey: chosenKey, IsCorrect: grade.Correct, AnsweredAt: now,
	}); err != nil {
		return nil, err
	}
	if err := s.repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug: item.Slug, Type: grade.EventType, Weight: grade.Weight, OccurredAt: now,
	}); err != nil {
		return nil, err
	}
	if grade.Weight != 0 {
		// A correct answer adds one distinct-item fact for the gate; an
		// unsure declaration folds nothing and grants nothing.
		// Bug fix: only append when this item isn't already in the facts —
		// CollectDirectFacts deduplicates by first-correct per item, but a
		// re-correct on the same item was appending a duplicate, letting
		// the gate see "2 facts" from one memorized answer.
		if grade.Correct {
			duplicate := false
			for _, f := range facts {
				if f.ItemID == itemID {
					duplicate = true
					break
				}
			}
			if !duplicate {
				facts = append(facts, DirectQuizFact{ItemID: itemID, FirstCorrectAt: now})
			}
		}
		s.foldOne(ctx, scope, item.Slug, Event{Type: grade.EventType, Weight: grade.Weight, OccurredAt: now})
	}
	// The deterministic review schedule: how many days the folded state
	// keeps its tier before decay pulls it below the demotion gate, plus
	// the fast-feedback bracket around the fold.
	if row, err := s.repo.GetMastery(ctx, scope, item.Slug); err == nil && row != nil {
		state := StateFromModel(row)
		if grade.Weight != 0 {
			if p := EffectiveP(state, now); p > 0 {
				after := p
				result.PEffAfter = &after
				result.PEffBefore = before // nil on the node's first evidence
			}
		}
		if threshold := tierDownThreshold(gatedAnchoredLevel(state, facts, now).Level); threshold > 0 {
			result.NextReviewDays = NextReviewDays(state, now, threshold)
		}
	}
	return result, nil
}

// tierDownThreshold maps a display tier to its demotion gate — the p_eff
// it must stay above to hold the tier. Unseen returns 0 (nothing to hold).
func tierDownThreshold(level Level) float64 {
	switch level {
	case LevelMastered:
		return LevelMasteredDown
	case LevelFamiliar:
		return LevelFamiliarDown
	case LevelTouched:
		return LevelTouchedDown
	default:
		return 0
	}
}

// findQuizItem resolves an item id inside the caller's KB.
func (s *Service) findQuizItem(ctx context.Context, scope interfaces.LearningScope, itemID string) *types.LearningQuizItem {
	item, err := s.repo.GetQuizItemByID(ctx, scope.TenantID, itemID)
	if err != nil || item == nil {
		return nil
	}
	if item.KnowledgeBaseID != scope.KnowledgeBaseID {
		return nil
	}
	return item
}

// Timeline returns one newest-first page of the caller's lighting events.
func (s *Service) Timeline(ctx context.Context, kbID string, page, pageSize int) ([]TimelineItem, int64, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, 0, err
	}
	events, total, err := s.repo.ListRecentEvents(ctx, scope, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	// Resolve every touched slug's page title in one batch so the timeline
	// reads in Chinese titles, not slugs; an unresolvable slug (deleted
	// page) keeps its raw slug as the fallback.
	slugSet := map[string]bool{}
	for _, e := range events {
		slugSet[e.Slug] = true
	}
	slugs := make([]string, 0, len(slugSet))
	for slug := range slugSet {
		slugs = append(slugs, slug)
	}
	pagesBySlug := map[string]*types.WikiPageLite{}
	if len(slugs) > 0 {
		if m, err := s.wikiRepo.ListBySlugs(ctx, kbID, slugs); err == nil {
			pagesBySlug = m
		} else {
			logger.Warnf(ctx, "learning: timeline title read failed (kb %s): %v", kbID, err)
		}
	}
	out := make([]TimelineItem, 0, len(events))
	for _, e := range events {
		item := TimelineItem{
			EventType: e.Type, Slug: e.Slug, Weight: e.Weight, OccurredAt: e.OccurredAt,
			SessionID: e.SessionID, MessageID: e.MessageID,
		}
		if p := pagesBySlug[e.Slug]; p != nil {
			item.Title = p.Title
			item.PageType = p.PageType
		}
		out = append(out, item)
	}
	return out, total, nil
}

// ExportProfile assembles the caller's full personal learning data.
func (s *Service) ExportProfile(ctx context.Context) (*ExportPayload, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrNoLearningScope
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return nil, ErrNoLearningScope
	}
	subject := principal.StorageID()
	if subject == "" {
		return nil, ErrNoLearningScope
	}

	payload := &ExportPayload{ExportedAt: time.Now()}
	var err error
	if payload.Events, err = s.repo.ListEventsBySubject(ctx, tenantID, subject); err != nil {
		return nil, err
	}
	if payload.Mastery, err = s.repo.ListMasteryBySubject(ctx, tenantID, subject); err != nil {
		return nil, err
	}
	if payload.TopicMaps, err = s.repo.ListMapsBySubject(ctx, tenantID, subject); err != nil {
		return nil, err
	}
	if payload.Attempts, err = s.repo.ListAttemptsBySubject(ctx, tenantID, subject); err != nil {
		return nil, err
	}
	payload.KBSummary = s.exportKBSummary(ctx, payload)
	return payload, nil
}

// exportKBSummary rolls the payload up by knowledge base at the top of the
// export: per-KB event/node/attempt counts, the KB's name when it still
// resolves, and Exists=false for KBs deleted after the data was collected
// (their soft-deleted rows hide from GetKnowledgeBaseByID) so the group
// labels itself instead of surfacing as mystery data.
func (s *Service) exportKBSummary(ctx context.Context, payload *ExportPayload) []ExportKBSummary {
	byKB := map[string]*ExportKBSummary{}
	get := func(kbID string) *ExportKBSummary {
		if kbID == "" {
			kbID = "-"
		}
		if u, ok := byKB[kbID]; ok {
			return u
		}
		u := &ExportKBSummary{KbID: kbID}
		// kbRepo is nil only in narrow test fixtures; treat such KBs as
		// existing rather than labelling everything deleted.
		u.Exists = s.kbRepo == nil
		if s.kbRepo != nil {
			if kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID); err == nil && kb != nil {
				u.KbName = kb.Name
				u.Exists = true
			}
		}
		byKB[kbID] = u
		return u
	}
	for _, e := range payload.Events {
		get(e.KnowledgeBaseID).Events++
	}
	for _, m := range payload.Mastery {
		get(m.KnowledgeBaseID).MasteryNodes++
	}
	for _, a := range payload.Attempts {
		get(a.KnowledgeBaseID).Attempts++
	}
	out := make([]ExportKBSummary, 0, len(byKB))
	for _, u := range byKB {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].KbID < out[j].KbID
	})
	return out
}

// DeleteProfile removes the caller's personal learning data (the KB-shared
// quiz bank is not personal and stays), optionally recording the opt-out
// so a deleted profile cannot resurrect on the next question.
func (s *Service) DeleteProfile(ctx context.Context, optOut bool) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return ErrNoLearningScope
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return ErrNoLearningScope
	}
	subject := principal.StorageID()
	if subject == "" {
		return ErrNoLearningScope
	}
	if err := s.repo.DeleteLearningDataBySubject(ctx, tenantID, subject); err != nil {
		return err
	}
	if optOut {
		if err := s.repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{
			TenantID: tenantID, SubjectID: subject, CollectDisabled: true,
		}); err != nil {
			return err
		}
		// The opt-out must hold on the very next request — never wait out
		// the prefs cache TTL after telling the user their data is gone.
		s.prefs.invalidate(tenantID, subject)
	}
	return nil
}

// GetSettings reads the collection opt-out.
func (s *Service) GetSettings(ctx context.Context) (*LearningSettings, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrNoLearningScope
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return nil, ErrNoLearningScope
	}
	prefs, err := s.repo.GetSubjectPrefs(ctx, tenantID, principal.StorageID())
	if err != nil {
		return nil, err
	}
	disabled := prefs != nil && prefs.CollectDisabled
	return &LearningSettings{CollectDisabled: disabled}, nil
}

// UpdateSettings writes the collection opt-out. The prefs cache is
// invalidated on write so a toggle takes effect immediately.
func (s *Service) UpdateSettings(ctx context.Context, disabled bool) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return ErrNoLearningScope
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return ErrNoLearningScope
	}
	if err := s.repo.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{
		TenantID: tenantID, SubjectID: principal.StorageID(), CollectDisabled: disabled,
	}); err != nil {
		return err
	}
	s.prefs.invalidate(tenantID, principal.StorageID())
	return nil
}

// MasteryOverlay derives the graph paint fields for the given slugs.
func (s *Service) MasteryOverlay(ctx context.Context, kbID string, slugs []string) (map[string]MasteryOverlayEntry, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListMastery(ctx, scope)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	direct := s.directFacts(ctx, scope)
	out := map[string]MasteryOverlayEntry{}
	for i := range rows {
		out[rows[i].Slug] = MasteryOverlayEntry{}
	}
	for i := range rows {
		lv := gatedAnchoredLevel(StateFromModel(&rows[i]), direct[rows[i].Slug], now)
		out[rows[i].Slug] = MasteryOverlayEntry{Level: string(lv.Level), LowConfidence: lv.LowConfidence}
	}
	_ = slugs // overlay carries all the caller's nodes; the graph handler filters
	return out, nil
}

// nodePages lists the KB's entity/concept pages once, shared by every read
// path that needs the node universe.
func (s *Service) nodePages(ctx context.Context, kbID string) ([]*types.WikiPage, error) {
	pages, err := s.wikiRepo.ListAll(ctx, kbID)
	if err != nil {
		return nil, err
	}
	out := make([]*types.WikiPage, 0, len(pages))
	for _, p := range pages {
		if p != nil && p.Slug != "" && (p.PageType == "entity" || p.PageType == "concept") {
			out = append(out, p)
		}
	}
	return out, nil
}
