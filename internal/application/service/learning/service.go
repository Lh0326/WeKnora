package learning

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// learningEnabled is the global kill switch. Default off: the feature is
// invisible unless LEARNING_ENABLE=true is set explicitly, which keeps the
// merged-but-dormant shape the design doc promises upstream ("关闸行为必
// 须与未安装本功能完全一致"). Same reading convention as NEO4J_ENABLE.
func learningEnabled() bool {
	return strings.ToLower(os.Getenv("LEARNING_ENABLE")) == "true"
}

// LearningEnabled is the exported read of the kill switch for handlers.
func LearningEnabled() bool { return learningEnabled() }

// Service is the learning layer's write-path entry point. The QA handler
// calls RecordAnswerTouches once per completed answer, from the same
// post-answer goroutine the memory subsystem uses; every failure is logged
// and swallowed there so the answer path never feels this layer.
type Service struct {
	repo     interfaces.LearningRepository
	wikiRepo pageReader
	pages    pageIndexCache
	prefs    prefsCache
	// docOrder caches the per-KB document-order ranks (the 从浅入深
	// channel's node positions in the source material).
	docOrder docOrderCache

	// Local folds keep their node mutex, always acquired after the durable
	// subject transaction. Cross-instance correctness comes from WithSubject.
	foldMu sync.Map // key: scopeKey → *sync.Mutex

	// LLM write paths (stage 3): model channel, chunk evidence, KB config.
	modelService interfaces.ModelService
	chunkRepo    chunkReader
	kbRepo       kbReader
	// knowledgeRepo resolves document titles for the quiz source-doc
	// links (page SourceRefs may carry bare ids without titles).
	knowledgeRepo docReader
}

// NewService wires the collector and the LLM write paths. All six
// dependencies are dig-resolvable interfaces, so the container line from
// stage 2 keeps working unchanged.
func NewService(
	repo interfaces.LearningRepository,
	wikiRepo interfaces.WikiPageRepository,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
	kbRepo interfaces.KnowledgeBaseRepository,
	knowledgeRepo interfaces.KnowledgeRepository,
) *Service {
	return &Service{repo: repo, wikiRepo: wikiRepo, chunkRepo: chunkRepo, modelService: modelService, kbRepo: kbRepo, knowledgeRepo: knowledgeRepo}
}

// docReader is the title slice of the knowledge repository plus the batch
// read the document-order channel needs (document creation times order the
// source material across documents).
type docReader interface {
	GetKnowledgeByID(ctx context.Context, tenantID uint64, id string) (*types.Knowledge, error)
	GetKnowledgeBatch(ctx context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error)
}

// chunkReader is the evidence slice of the chunk repository the quiz
// generator needs.
type chunkReader interface {
	ListChunksByID(ctx context.Context, tenantID uint64, ids []string) ([]*types.Chunk, error)
}

// kbReader is the config slice of the knowledge-base repository.
type kbReader interface {
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
	// ListKnowledgeBases joins the maintenance universe: a wiki KB with
	// pages but zero learning activity (fresh upload) must still get its
	// prerequisite edges and quiz bank — the cold-boot bootstrap.
	ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error)
}

// RecordAnswerTouches folds the knowledge nodes an answer's citations
// touched into the answerer's mastery. The three short-circuits of the
// collection contract, in order of cheapness: the kill switch, missing
// evidence, and the subject's opt-out. Scope derives from the request
// context alone (Principal.StorageID(), the memory subsystem's
// convention), so there is no parameter a caller could use to write
// someone else's mastery.
func (s *Service) recordAnswerTouches(ctx context.Context, assistantMessage *types.Message) error {
	if !learningEnabled() {
		return nil
	}
	if assistantMessage == nil || len(assistantMessage.KnowledgeReferences) == 0 {
		return nil
	}

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}
	subjectID := principal.StorageID()
	if subjectID == "" {
		return nil
	}

	// Deterministic KB order keeps event ordering stable for a given
	// answer, which makes replays reproducible.
	byKB := citationsByKB(assistantMessage.KnowledgeReferences)
	kbIDs := make([]string, 0, len(byKB))
	for kbID := range byKB {
		kbIDs = append(kbIDs, kbID)
	}
	sort.Strings(kbIDs)

	now := time.Now()
	for _, kbID := range kbIDs {
		index, err := s.pages.index(ctx, s.wikiRepo, kbID)
		if err != nil {
			logger.Warnf(ctx, "learning: page index build failed (kb %s): %v", kbID, err)
			continue
		}
		if index.empty() {
			continue
		}

		scope := interfaces.LearningScope{TenantID: tenantID, SubjectID: subjectID, KnowledgeBaseID: kbID}
		prior, err := listEventWindow(ctx, s.repo, scope, now.Add(-touchLookback))
		if err != nil {
			logger.Warnf(ctx, "learning: prior events lookup failed (kb %s): %v", kbID, err)
			continue
		}

		for _, slug := range index.touchedSlugs(byKB[kbID]) {
			duplicate := false
			for _, previous := range prior {
				if assistantMessage.ID != "" && previous.MessageID == assistantMessage.ID && previous.Slug == slug {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			eventType := classifyTouch(now, slug, prior)
			if eventType == "" {
				// Capped repeat touch (the window already recorded a re_ask):
				// no new information, no event, no fold.
				continue
			}
			weight := weightForType(eventType)
			event := &types.LearningEvent{
				TenantID:        tenantID,
				SubjectID:       subjectID,
				KnowledgeBaseID: kbID,
				Slug:            slug,
				Type:            eventType,
				Weight:          weight,
				SessionID:       assistantMessage.SessionID,
				MessageID:       assistantMessage.ID,
				OccurredAt:      now,
			}
			if assistantMessage.ID != "" {
				// Stable identity also deduplicates callbacks beyond the short
				// lookback window. The transaction rolls back a duplicate append.
				event.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", tenantID, subjectID, kbID, slug, assistantMessage.ID))).String()
			}
			if err := s.repo.AppendEvent(ctx, event); err != nil {
				logger.Warnf(ctx, "learning: append event failed (slug %s): %v", slug, err)
				return err
			}
			if err := s.foldOne(ctx, scope, slug, Event{Type: eventType, Weight: weight, OccurredAt: now}); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecordWikiRead folds one deliberate wiki page open into the reader's
// mastery — the spec's opportunistic wiki-read signal (§3.3.6: weight =
// WeightTopicSignal). The caller is the browser view's page-open path, so
// every "点开一个知识点阅读" becomes a visible touch.
//
// Two dedup tiers separate "record the behaviour" from "pay the score":
//  1. a rapid-duplicate window (ReadRapidDedup, one minute): a second open
//     of the same page inside it records NOTHING — it is a refresh or a
//     double-click, not studying;
//  2. the re-ask window (48h): further opens of the same page still append
//     a zero-weight event (the timeline keeps the full trace — "我看了"
//     must always be visible), but add no mastery, so re-reading cannot
//     farm score.
//
// Guards, in order: the kill switch, the opt-out, node membership (only
// entity/concept pages of this KB).
//
// tier: "" / "normal" = the glance signal above; "deep" = the reader stayed
// past the frontend deep-dwell threshold — recorded as its own event type
// (wiki_deep_read, weight WeightWikiDeepRead), at most one per node per
// re-ask window, and exempt from the refresh shield (it fires on page
// leave, so rapid re-entry is already impossible from the caller side).
func (s *Service) recordWikiRead(ctx context.Context, kbID, slug, tier string) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	index, err := s.pages.index(ctx, s.wikiRepo, kbID)
	if err != nil {
		return err
	}
	if _, ok := index.pages[slug]; !ok {
		return ErrWikiReadTarget // not a knowledge node of this KB — not a touch
	}
	// Serialize the dedup read, append and fold as one local operation.
	// Locking only the final fold lets concurrent readers all claim "first".
	mu := s.lockNode(scope, slug)
	defer mu.Unlock()

	now := time.Now()
	prior, err := listEventWindow(ctx, s.repo, scope, now.Add(-touchLookback))
	if err != nil {
		logger.Warnf(ctx, "learning: wiki-read dedup lookup failed (slug %s): %v", slug, err)
		return nil // conservative: skip rather than risk farming
	}
	if tier == "deep" {
		return s.recordDeepRead(ctx, scope, slug, prior, now)
	}
	alreadyReadInWindow := false
	for _, ev := range prior {
		if ev.Slug != slug || ev.Type != types.LearningEventWikiToolRead {
			continue
		}
		if now.Sub(ev.OccurredAt) <= ReadRapidDedup {
			return nil // refresh / double-click shield: not studying
		}
		if now.Sub(ev.OccurredAt) <= ReAskWindowHours*time.Hour {
			alreadyReadInWindow = true
		}
	}

	// Every deliberate open lands in the timeline; only the first per 48h
	// carries mastery weight.
	weight := 0.0
	if !alreadyReadInWindow {
		weight = weightForType(types.LearningEventWikiToolRead)
	}
	if err := s.repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug:            slug,
		Type:            types.LearningEventWikiToolRead,
		Weight:          weight,
		OccurredAt:      now,
	}); err != nil {
		return err
	}
	if weight > 0 {
		return s.foldOneLocked(ctx, scope, slug, Event{Type: types.LearningEventWikiToolRead, Weight: weight, OccurredAt: now})
	}
	return nil
}

// recordDeepRead lands the "deep" tier of the read signal: the reader
// stayed on the page past the deep-dwell threshold. At most one
// wiki_deep_read per node per re-ask window — repeats inside the window
// are silent no-ops (the dwell was already credited).
func (s *Service) recordDeepRead(
	ctx context.Context, scope interfaces.LearningScope, slug string,
	prior []types.LearningEvent, now time.Time,
) error {
	for _, ev := range prior {
		if ev.Slug == slug && ev.Type == types.LearningEventWikiDeepRead &&
			now.Sub(ev.OccurredAt) <= ReAskWindowHours*time.Hour {
			return nil // already credited this window
		}
	}
	weight := WeightWikiDeepRead
	if err := s.repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug:            slug,
		Type:            types.LearningEventWikiDeepRead,
		Weight:          weight,
		OccurredAt:      now,
	}); err != nil {
		return err
	}
	return s.foldOneLocked(ctx, scope, slug, Event{Type: types.LearningEventWikiDeepRead, Weight: weight, OccurredAt: now})
}

// directFacts loads the subject's per-slug direct-evidence facts for one
// KB — the distinct-item correct answers the tier gate reads. One indexed
// query per read path; a failure degrades to "no direct evidence" (the
// gate then caps at touched), which is the conservative side.
func (s *Service) directFacts(ctx context.Context, scope interfaces.LearningScope) map[string][]DirectQuizFact {
	// Incorrect/unsure attempts reveal feedback too and must participate in
	// the eligibility window, otherwise a corrected retry unlocks a tier.
	attempts, err := s.repo.ListAttempts(ctx, scope, "")
	if err != nil {
		logger.Warnf(ctx, "learning: direct-evidence read failed (kb %s): %v", scope.KnowledgeBaseID, err)
		return nil
	}
	return CollectDirectFacts(attempts)
}

// RecordSelfAssess lands the skills-matrix self-assessment track: the user
// claims to know a node better (or worse) than the system believes.
//
// "Better" lifts the frozen logit straight into the mastered band — the
// progress becomes instantly visible in p_eff and the in-tier progress bar —
// yet self-assessment is indirect evidence, so the direct-evidence gate
// still caps the displayed tier until quiz facts arrive. The system is
// literally saying "prove it": the frontend follows an up assessment by
// offering the practice quiz.
//
// "Worse" is a demotion with a reason taxonomy (the skills-matrix interview
// question "which part?"). all = mis-click, reset to the floor; the three
// partial reasons demote exactly one band, landing on the lower band's
// demotion line (hysteresis keeps the node inside that band), and the reason
// itself rides the event type as a content-feedback label for future
// maintenance (quiz regeneration, doc refresh hints).
//
// One self-assessment per (subject, node, direction) per re-ask window —
// repeats are idempotent no-ops; switching direction inside the window is
// allowed (people change their minds). The computed weight is frozen on the
// event row like every other weight, so replays reproduce the set-point.
func (s *Service) recordSelfAssess(ctx context.Context, kbID, slug string, up bool, reason string) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	index, err := s.pages.index(ctx, s.wikiRepo, kbID)
	if err != nil {
		return err
	}
	if _, ok := index.pages[slug]; !ok {
		return ErrWikiReadTarget // not a knowledge node of this KB
	}

	now := time.Now()
	prior, err := listEventWindow(ctx, s.repo, scope, now.Add(-touchLookback))
	if err != nil {
		logger.Warnf(ctx, "learning: self-assess dedup lookup failed (slug %s): %v", slug, err)
		return nil // conservative: skip rather than risk spamming
	}
	for _, ev := range prior {
		if ev.Slug != slug || !strings.HasPrefix(ev.Type, "self_assess_") {
			continue
		}
		wasUp := ev.Type == types.LearningEventSelfAssessUp
		if wasUp == up && now.Sub(ev.OccurredAt) <= ReAskWindowHours*time.Hour {
			return nil // same-direction repeat inside the window: idempotent
		}
	}

	row, err := s.repo.GetMastery(ctx, scope, slug)
	if err != nil {
		return err
	}
	var state FoldState
	if row != nil {
		state = StateFromModel(row)
	}

	var eventType string
	var weight float64
	if up {
		eventType = types.LearningEventSelfAssessUp
		target := logitOf(LevelMasteredUp)
		if state.Logit >= target {
			return nil // already at or above the mastered band: nothing to lift
		}
		weight = target - state.Logit
	} else {
		switch reason {
		case "all":
			eventType = types.LearningEventSelfAssessDownAll
		case "doc_gap":
			eventType = types.LearningEventSelfAssessDownDocGap
		case "doc_updated":
			eventType = types.LearningEventSelfAssessDownDocUpdated
		case "quiz_easy":
			eventType = types.LearningEventSelfAssessDownQuizEasy
		default:
			return fmt.Errorf("invalid self-assess reason %q", reason)
		}
		// "all" is a full reset to the floor; the partial reasons demote
		// exactly one band via the gated level.
		var target float64
		if reason == "all" {
			target = LogitFloor
		} else {
			target = selfAssessDemotionTarget(state, s.directFacts(ctx, scope)[slug], now)
		}
		if state.Logit <= target {
			return nil // already at/below the target band: nothing to demote
		}
		weight = target - state.Logit
	}

	if err := s.repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug:            slug,
		Type:            eventType,
		Weight:          weight,
		OccurredAt:      now,
	}); err != nil {
		return err
	}
	return s.foldOne(ctx, scope, slug, Event{Type: eventType, Weight: weight, OccurredAt: now})
}

// selfAssessDemotionTarget is the logit set-point a "less proficient"
// assessment lands on: exactly one band lower than the current gated level.
// Bug fix: the target must sit on the lower band's UP threshold (not its
// Down line) — LevelOf re-derives the display level from the logit alone
// each read (no persisted prevLevel for hysteresis on this path), and a
// p_eff below the Up line would display as yet another band lower.
// Sitting ON the Up threshold means the node enters that band immediately.
// A node already at the bottom has nothing to demote (caller checks).
func selfAssessDemotionTarget(state FoldState, facts []DirectQuizFact, now time.Time) float64 {
	switch gatedAnchoredLevel(state, facts, now).Level {
	case LevelMastered:
		return logitOf(LevelFamiliarUp)
	case LevelFamiliar:
		return logitOf(LevelTouchedUp)
	default: // touched (or unseen with stale evidence): into the faded zone
		return logitOf(LevelTouchedUp) - 1.0
	}
}

// RecordSkip lands the user's standing queue-suppression declaration
// ("已掌握，不再推荐") or revokes it. Where self-assessment is a claim the
// system answers with "prove it" (the node pins to the top of the queue
// until quiz proof lands), a skip is the opposite intent: "take this out
// of my queue" — so it folds nothing, gates nothing and never expires;
// the recommender simply excludes the node until the user takes it back.
// Recorded regardless of the collection opt-out, like the opt-out setting
// itself: this is a preference the user set on purpose, not telemetry
// collected in the background.
func (s *Service) recordSkip(ctx context.Context, kbID, slug string, skipped bool) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	index, err := s.pages.index(ctx, s.wikiRepo, kbID)
	if err != nil {
		return err
	}
	if _, ok := index.pages[slug]; !ok {
		return ErrWikiReadTarget // not a knowledge node of this KB
	}
	if skipped {
		return s.repo.AddSkip(ctx, scope, slug, time.Time{})
	}
	return s.repo.RemoveSkip(ctx, scope, slug)
}

// logitOf is the inverse sigmoid: the logit at which p equals the threshold.
func logitOf(p float64) float64 {
	return math.Log(p / (1 - p))
}

// evictFoldMu releases cached local mutexes after deletion. The database
// subject lock and operation epoch remain the correctness boundary.
func (s *Service) evictFoldMu(subjectID string) {
	s.foldMu.Range(func(key, _ any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		parts := strings.Split(k, "|")
		if len(parts) >= 4 && parts[1] == subjectID {
			s.foldMu.Delete(k)
		}
		return true
	})
}

// foldOne updates a projection inside the caller's subject transaction.
// Failures propagate so the associated event and attempt roll back too.
func (s *Service) foldOne(
	ctx context.Context, scope interfaces.LearningScope, slug string, event Event,
) error {
	mu := s.lockNode(scope, slug)
	defer mu.Unlock()
	return s.foldOneLocked(ctx, scope, slug, event)
}

// lockNode acquires the per-(scope, slug) keyed mutex and returns it locked;
// the caller defers Unlock. Callers that need the read side of the same
// critical section (SubmitAnswer's prior-attempt count) hold this lock across
// their whole cycle and call foldOneLocked instead of foldOne — the mutex is
// not reentrant.
func (s *Service) lockNode(scope interfaces.LearningScope, slug string) *sync.Mutex {
	// Bug fix: serialize the read-modify-write cycle per (scope, slug) —
	// without this, a QA goroutine and a quiz SubmitAnswer racing on the
	// same node both fold from the same stale state and the later upsert
	// silently drops the earlier contribution.
	mu, _ := s.foldMu.LoadOrStore(fmt.Sprintf("%d|%s|%s|%s",
		scope.TenantID, scope.SubjectID, scope.KnowledgeBaseID, slug), &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m
}

// foldOneLocked is foldOne for callers already holding lockNode.
func (s *Service) foldOneLocked(
	ctx context.Context, scope interfaces.LearningScope, slug string, event Event,
) error {
	row, err := s.repo.GetMastery(ctx, scope, slug)
	if err != nil {
		logger.Warnf(ctx, "learning: mastery read failed (slug %s): %v", slug, err)
		return err
	}
	if row == nil {
		row = &types.MasteryState{
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: scope.KnowledgeBaseID,
			Slug:            slug,
		}
	}
	state := FoldEvent(StateFromModel(row), event)
	state.ApplyTo(row)
	return s.repo.UpsertMastery(ctx, row)
}
