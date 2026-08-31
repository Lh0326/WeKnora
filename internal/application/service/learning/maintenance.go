package learning

import (
	"context"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// scopeType aliases the interface scope for terse constructor helpers.
type scopeType = interfaces.LearningScope

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// RunMaintenance runs the three stage-3 write paths: topic mapping for
// every subject that has memory topics, then prerequisite edges and quiz
// generation for every KB whose learning_features switch is on. The
// reconcile runner calls it on startup and once a day; every pass is
// incremental (page watermarks, quiz deficits) and safe to re-run.
func (s *Service) RunMaintenance(ctx context.Context) error {
	if !learningEnabled() {
		return nil
	}
	if err := s.runTopicMapping(ctx); err != nil {
		logger.Warnf(ctx, "learning: topic mapping pass failed: %v", err)
	}
	if err := s.runEdgeAndQuizPass(ctx); err != nil {
		logger.Warnf(ctx, "learning: edge/quiz pass failed: %v", err)
	}
	return nil
}

// runTopicMapping projects memory topics onto wiki nodes (channel B). The
// KB universe for a subject is the set of KBs their doc affinity already
// names — the libraries the person actually works in — so a topic is never
// adjudicated against pages it cannot plausibly concern.
func (s *Service) runTopicMapping(ctx context.Context) error {
	stats, err := s.repo.ListTopicStats(ctx)
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		return nil
	}

	type scopeKey struct {
		tenant  uint64
		subject string
	}
	byScope := map[scopeKey][]*types.MemoryTopicStat{}
	for i := range stats {
		st := stats[i]
		if st.TenantID == 0 || st.SubjectID == "" || st.NormalizedKey == "" {
			continue
		}
		key := scopeKey{st.TenantID, st.SubjectID}
		byScope[key] = append(byScope[key], &st)
	}

	affinities, err := s.repo.ListDocAffinity(ctx)
	if err != nil {
		return err
	}
	kbsByScope := map[scopeKey]map[string]bool{}
	for _, row := range affinities {
		key := scopeKey{row.TenantID, row.SubjectID}
		if row.KnowledgeBaseID == "" {
			continue
		}
		if kbsByScope[key] == nil {
			kbsByScope[key] = map[string]bool{}
		}
		kbsByScope[key][row.KnowledgeBaseID] = true
	}

	scopes := make([]scopeKey, 0, len(byScope))
	for key := range byScope {
		scopes = append(scopes, key)
	}
	for _, key := range scopes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for kbID := range kbsByScope[key] {
			kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
			if err != nil || kb == nil {
				continue
			}
			modelID := resolveLearningModelID(kb)
			if modelID == "" {
				continue // config gap, not an error; next pass may have a model
			}
			pages, err := s.wikiRepo.ListAll(ctx, kbID)
			if err != nil || len(pages) == 0 {
				continue
			}
			// Channel B projects onto knowledge NODES only (the node layer's
			// entity/concept definition); summary/index/synthesis/comparison
			// pages are document projections and session artifacts, so a
			// topic must never land on them.
			nodes := make([]*types.WikiPage, 0, len(pages))
			for _, p := range pages {
				if p != nil && (p.PageType == "entity" || p.PageType == "concept") {
					nodes = append(nodes, p)
				}
			}
			if len(nodes) == 0 {
				continue
			}
			pagesBySlug := map[string]*types.WikiPage{}
			for _, p := range nodes {
				if p != nil {
					pagesBySlug[p.Slug] = p
				}
			}
			// Tenant-scoped model channel: the background context carries no
			// tenant, so inject the scope's before any LLM adjudication.
			tenantCtx := context.WithValue(ctx, types.TenantIDContextKey, key.tenant)
			s.mapTopicsForScope(tenantCtx, key.tenant, key.subject, kbID, modelID, byScope[key], nodes, pagesBySlug)
		}
	}
	return nil
}

// mapTopicsForScope batches topics with candidates, adjudicates, stores
// accepted mappings and emits the deduplicated topic_signal events.
func (s *Service) mapTopicsForScope(
	ctx context.Context, tenant uint64, subject, kbID, modelID string,
	stats []*types.MemoryTopicStat, pages []*types.WikiPage, pagesBySlug map[string]*types.WikiPage,
) {
	scope := interfaces.LearningScope{TenantID: tenant, SubjectID: subject, KnowledgeBaseID: kbID}
	now := time.Now()
	// Debounce watermark: topics that already carry a decided mapping onto
	// a still-live node of this KB are settled — re-adjudicating them daily
	// would only burn model budget on an idempotent upsert. A mapping whose
	// target page no longer resolves (rename/merge the reconcile pass has
	// not migrated yet) is NOT settled and does get re-adjudicated.
	settled := map[string]bool{}
	if priorMaps, err := s.repo.ListMapsBySubject(ctx, tenant, subject); err == nil {
		for _, m := range priorMaps {
			if m.KnowledgeBaseID == kbID && pagesBySlug[m.Slug] != nil {
				settled[m.NormalizedTopicKey] = true
			}
		}
	} else {
		logger.Warnf(ctx, "learning: topic-map watermark read failed (kb %s): %v", kbID, err)
	}
	candidatesByTopic := map[string][]string{}
	var withCandidates []*types.MemoryTopicStat
	for _, st := range stats {
		if settled[st.NormalizedKey] {
			continue
		}
		if cands := topicCandidates(st, pages, topicMaxCandidates); len(cands) > 0 {
			candidatesByTopic[st.NormalizedKey] = cands
			withCandidates = append(withCandidates, st)
		}
	}
	if len(withCandidates) == 0 {
		return
	}

	prior, priorErr := s.repo.ListEvents(ctx, scope, now.Add(-topicSignalWindow), 0)
	if priorErr != nil {
		logger.Warnf(ctx, "learning: topic-signal dedup read failed, skipping batch: %v", priorErr)
		return // conservative: skip rather than risk duplicate signals
	}
	recentSignal := map[string]bool{}
	for _, ev := range prior {
		if ev.Type == types.LearningEventTopicSignal {
			recentSignal[ev.Slug] = true
		}
	}

	for start := 0; start < len(withCandidates); start += topicBatchSize {
		end := start + topicBatchSize
		if end > len(withCandidates) {
			end = len(withCandidates)
		}
		batch := withCandidates[start:end]
		user := topicMapUserPrompt(batch, candidatesByTopic, pagesBySlug)
		var resp topicMapResponse
		if err := s.callLearningJSON(ctx, modelID, agent.LearningTopicMapPrompt, user,
			topicMapSchema, learningAdjudicateBudget, learningAdjudicateRetry, &resp); err != nil {
			continue // warned inside; the next daily pass retries
		}
		for topicKey, verdict := range acceptedTopicMaps(resp, candidatesByTopic) {
			var label string
			for _, st := range batch {
				if st.NormalizedKey == topicKey {
					label = st.Topic
					break
				}
			}
			if err := s.repo.UpsertTopicMap(ctx, &types.MemoryWikiMap{
				TenantID: tenant, SubjectID: subject, KnowledgeBaseID: kbID,
				NormalizedTopicKey: topicKey, Slug: verdict.Slug,
				TopicLabel: label, Confidence: verdict.Confidence,
				DecidedBy: types.LearningMapDecidedByLLM,
			}); err != nil {
				logger.Warnf(ctx, "learning: topic map upsert failed (%s→%s): %v", topicKey, verdict.Slug, err)
				continue
			}
			// The projection event: at most one topic_signal per slug per
			// window, so several topics converging on one node read as one
			// consolidation, not as farming.
			if recentSignal[verdict.Slug] {
				continue
			}
			event := &types.LearningEvent{
				TenantID: tenant, SubjectID: subject, KnowledgeBaseID: kbID,
				Slug: verdict.Slug, Type: types.LearningEventTopicSignal,
				Weight: WeightTopicSignal, OccurredAt: now,
			}
			if err := s.repo.AppendEvent(ctx, event); err == nil {
				s.foldOne(ctx, scope, verdict.Slug,
					Event{Type: types.LearningEventTopicSignal, Weight: WeightTopicSignal, OccurredAt: now})
				recentSignal[verdict.Slug] = true
			}
		}
	}
}

// runEdgeAndQuizPass iterates the KBs that carry wiki activity and runs
// the gated passes. Structure mirrors the reconcile pass: read pages once,
// group per KB, decide, write.
func (s *Service) runEdgeAndQuizPass(ctx context.Context) error {
	for _, kbID := range s.distinctKnownKBs(ctx) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil || kb == nil {
			continue
		}
		if !learningKBGate(kb) {
			continue // the per-KB switch gates the two sustained-LLM paths
		}
		modelID := resolveLearningModelID(kb)
		if modelID == "" {
			logger.Warnf(ctx, "learning: KB %s has no synthesis/summary model, skipping edge/quiz pass", kbID)
			continue
		}
		// The model channel is tenant-scoped (GetChatModel panics without a
		// tenant in ctx), and this pass runs from the background runner whose
		// context is empty — inject the KB's tenant, the wiki-ingest
		// background-task pattern.
		kbCtx := context.WithValue(ctx, types.TenantIDContextKey, kb.TenantID)
		if err := s.runEdgePass(kbCtx, kb, modelID); err != nil {
			logger.Warnf(ctx, "learning: edge pass failed (kb %s): %v", kbID, err)
		}
		if err := s.runQuizPass(kbCtx, kb, modelID); err != nil {
			logger.Warnf(ctx, "learning: quiz pass failed (kb %s): %v", kbID, err)
		}
	}
	return nil
}

// runEdgePass generates, adjudicates and stores prerequisite edges for one KB.
func (s *Service) runEdgePass(ctx context.Context, kb *types.KnowledgeBase, modelID string) error {
	pages, err := s.wikiRepo.ListAll(ctx, kb.ID)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return nil
	}
	pagesBySlug := map[string]*types.WikiPage{}
	for _, p := range pages {
		if p != nil {
			pagesBySlug[p.Slug] = p
		}
	}

	existing, err := s.repo.ListEdges(ctx, kb.TenantID, kb.ID)
	if err != nil {
		return err
	}
	pairs := pairsInvolvingNewPages(edgeCandidatePairs(pages), pagesBySlug, edgeWatermark(existing))
	if len(pairs) == 0 {
		return nil
	}

	user := edgeUserPrompt(pagesBySlug, pairs)
	var resp edgeResponse
	if err := s.callLearningJSON(ctx, modelID, agent.LearningPrereqEdgePrompt, user,
		edgeSchema, learningAdjudicateBudget, learningAdjudicateRetry, &resp); err != nil {
		return err
	}
	for _, v := range acceptedEdges(resp, pairs) {
		edge := &types.LearningEdge{
			TenantID: kb.TenantID, KnowledgeBaseID: kb.ID,
			FromSlug:   v.From,
			ToSlug:     v.To,
			Relation:   types.LearningEdgePrerequisite,
			Confidence: v.Confidence, Source: types.LearningEdgeSourceHeuristicLLM,
		}
		if err := s.repo.UpsertEdge(ctx, edge); err != nil {
			logger.Warnf(ctx, "learning: edge upsert failed (%s→%s): %v", v.From, v.To, err)
		}
	}
	return nil
}

// runQuizPass tops up the grounded question bank for one KB's pages.
func (s *Service) runQuizPass(ctx context.Context, kb *types.KnowledgeBase, modelID string) error {
	pages, err := s.wikiRepo.ListAll(ctx, kb.ID)
	if err != nil {
		return err
	}
	for _, page := range pages {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if page == nil || page.Slug == "" {
			continue
		}
		items, err := s.repo.ListQuizItems(ctx, kb.TenantID, kb.ID, page.Slug)
		if err != nil {
			continue
		}
		active := 0
		for _, it := range items {
			if it.Status == types.LearningQuizStatusActive {
				active++
			}
		}
		need := quizDeficit(page, active)
		if need == 0 || len(page.ChunkRefs) == 0 {
			continue
		}

		ids := append([]string{}, page.ChunkRefs...)
		if len(ids) > quizEvidenceChunkCap {
			ids = ids[:quizEvidenceChunkCap]
		}
		chunks, err := s.chunkRepo.ListChunksByID(ctx, kb.TenantID, ids)
		if err != nil {
			logger.Warnf(ctx, "learning: quiz evidence read failed (slug %s): %v", page.Slug, err)
			continue
		}
		excerpts := map[string]string{}
		for _, c := range chunks {
			if c != nil && c.Content != "" {
				excerpts[c.ID] = c.Content
			}
		}
		if len(excerpts) == 0 {
			continue // no evidence, no questions — refusing beats inventing
		}

		user := quizUserPrompt(page, need, excerpts)
		var resp quizResponse
		if err := s.callLearningJSON(ctx, modelID, agent.LearningQuizPrompt, user,
			quizSchema, learningQuizBudget, learningQuizRetry, &resp); err != nil {
			continue
		}
		stored := 0
		for _, draft := range resp.Questions {
			if stored >= need {
				break
			}
			item := validateQuizDraft(draft, page)
			if item == nil {
				continue // ungrounded or malformed: dropped whole, never rescued
			}
			item.TenantID = kb.TenantID
			if err := s.repo.UpsertQuizItem(ctx, item); err != nil {
				logger.Warnf(ctx, "learning: quiz upsert failed (slug %s): %v", page.Slug, err)
				continue
			}
			stored++
		}
	}
	return nil
}

// distinctKnownKBs rolls up the KB ids the learning layer has activity in.
func (s *Service) distinctKnownKBs(ctx context.Context) []string {
	// Primary: mastery activity; secondary: doc affinity (backfill universe).
	rows, err := s.repo.ListAllMastery(ctx)
	if err != nil {
		rows = nil
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.KnowledgeBaseID] = true
	}
	if affinities, err := s.repo.ListDocAffinity(ctx); err == nil {
		for _, a := range affinities {
			if a.KnowledgeBaseID != "" {
				seen[a.KnowledgeBaseID] = true
			}
		}
	}
	var out []string
	for kb := range seen {
		out = append(out, kb)
	}
	return sortedStrings(out)
}
