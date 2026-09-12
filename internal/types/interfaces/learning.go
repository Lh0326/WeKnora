package interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// LearningScope names one person's learning data inside one knowledge base.
// The convention mirrors MemoryScope: SubjectID is Principal.StorageID(),
// and rows outside the caller's own scope are unreachable by construction
// because every learning route derives the scope from the caller's
// principal — there is no subject parameter to forge.
type LearningScope struct {
	TenantID        uint64
	SubjectID       string
	KnowledgeBaseID string
}

func (s LearningScope) Valid() bool {
	return s.TenantID > 0 && s.SubjectID != "" && s.KnowledgeBaseID != ""
}

// LearningRepository persists the learning layer. Stage-1 surface: event
// append, mastery fold upsert/read, prefs, and the KB-shared inventory
// (edges, quiz items) plus personal attempt records. Later stages extend
// this interface with their own reads rather than widening these methods.
type LearningRepository interface {
	GetPlanPreference(context.Context, LearningScope) (*types.LearningPlanPreference, error)
	SavePlanPreference(context.Context, *types.LearningPlanPreference, string) error
	ListPlanPreferencesBySubject(context.Context, string) ([]types.LearningPlanPreference, error)
	// WithSubject serializes a complete personal operation with deletion and
	// consent changes. The callback must use its supplied context for every
	// repository call; errors roll the whole operation back. collect=false is
	// reserved for explicit preferences and repairs of existing preferences.
	WithSubject(ctx context.Context, subjectID string, expectedEpoch int64, collect bool, fn func(context.Context) error) error
	ListEventScopes(ctx context.Context) ([]LearningScope, error)
	// AppendEvent stores one immutable event row. IDs are minted here when
	// empty so callers cannot forget.
	AppendEvent(ctx context.Context, event *types.LearningEvent) error
	// ListEvents returns the subject's events for one KB, oldest first,
	// optionally bounded below by since (zero = unbounded) and above by
	// limit (0 = a large default). Backing store for replay, reconcile and
	// the timeline.
	ListEvents(ctx context.Context, scope LearningScope, since time.Time, limit int) ([]types.LearningEvent, error)
	// ListEventsPaged is the replay reader: keyset pagination in the
	// canonical deterministic order (occurred_at ASC, id ASC — the id term
	// stabilizes same-timestamp batches). Loop until a short page to fold a
	// history until the caller's deadline; an unbounded single fetch can see a
	// truncated suffix.
	ListEventsPaged(ctx context.Context, scope LearningScope, afterOccurredAt time.Time, afterID string, limit int) ([]types.LearningEvent, error)
	// GetMastery returns one folded row, or (nil, nil) when the subject has
	// never touched the node.
	GetMastery(ctx context.Context, scope LearningScope, slug string) (*types.MasteryState, error)
	// UpsertMastery inserts or updates the fold for one (subject, node),
	// keyed by the scope unique index.
	UpsertMastery(ctx context.Context, state *types.MasteryState) error
	// ListMastery returns every folded node of the subject in one KB.
	ListMastery(ctx context.Context, scope LearningScope) ([]types.MasteryState, error)
	// ListLastActivity returns slug → newest raw event time in one KB for
	// the subject, zero-weight activity included. The read side uses it for
	// the "studied recently" marker, which must reflect touches (deduped
	// re-reads, unsure answers) that deliberately do not fold into mastery.
	ListLastActivity(ctx context.Context, scope LearningScope) (map[string]time.Time, error)
	// ListSelfAssess returns the latest self-assessment within
	// SelfAssessVisibleWindow per slug: direction ("up"/"down") plus the raw
	// event type (carries the down reason) and its time. Read-side only —
	// the tier display never consumes it; hover cards and the timeline do.
	ListSelfAssess(ctx context.Context, scope LearningScope) (map[string]SelfAssessMark, error)
	// ListActiveDays returns the distinct YYYY-MM-DD days (server calendar)
	// on which the subject stored any event since the given time — the
	// streak input, so a heavy history cannot truncate it through the
	// paged event read.
	ListActiveDays(ctx context.Context, scope LearningScope, since time.Time) ([]string, error)
	// GetSubjectPrefs returns the subject's collection opt-out, or (nil,
	// nil) when the subject never set one. Subject-scoped (not
	// tenant-scoped): shared-KB requests run under the KB owner's
	// effective tenant, and the opt-out is a property of the person, not
	// of the workspace — a disabled row in any tenant wins.
	GetSubjectPrefs(ctx context.Context, subjectID string) (*types.LearningSubjectPrefs, error)
	// UpsertSubjectPrefs sets the collection opt-out.
	UpsertSubjectPrefs(ctx context.Context, prefs *types.LearningSubjectPrefs) error
	// UpsertEdge inserts or updates one KB-shared prerequisite edge.
	UpsertEdge(ctx context.Context, edge *types.LearningEdge) error
	// DeleteEdge removes one stored edge — the reconciler's repair path for
	// edges whose endpoints no longer resolve to any live page.
	DeleteEdge(ctx context.Context, tenantID uint64, knowledgeBaseID, fromSlug, toSlug string) error
	// ListEdges returns all prerequisite edges of one KB.
	ListEdges(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningEdge, error)
	// ListAllEdges returns every stored edge across KBs — edges are
	// KB-shared, so the reconciler's repair walk must discover KBs that no
	// subject's rows point at anymore (everyone's data deleted, edges left).
	ListAllEdges(ctx context.Context) ([]types.LearningEdge, error)
	// UpsertQuizItem inserts or updates one KB-shared quiz item by id.
	UpsertQuizItem(ctx context.Context, item *types.LearningQuizItem) error
	// ListQuizItems returns the items of one node, active first filter left
	// to the caller.
	ListQuizItems(ctx context.Context, tenantID uint64, knowledgeBaseID, slug string) ([]types.LearningQuizItem, error)
	// InsertAttempt stores one personal answer record.
	InsertAttempt(ctx context.Context, attempt *types.LearningQuizAttempt) error
	// ListAttempts returns the subject's answer history for one node (empty slug selects the scoped KB),
	// oldest first — the input for repeat-attempt decay.
	ListAttempts(ctx context.Context, scope LearningScope, slug string) ([]types.LearningQuizAttempt, error)
	// ListCorrectAttempts returns the subject's correct answers in one KB,
	// oldest first — the direct-evidence source the tier gate derives its
	// distinct-item facts from (read side only, never folded).
	ListCorrectAttempts(ctx context.Context, scope LearningScope) ([]types.LearningQuizAttempt, error)

	// DeleteMastery retires one folded row. Alias reconciliation uses it
	// after the migrated state is written under the live slug, so a rename
	// never leaves two rows describing the same node.
	DeleteMastery(ctx context.Context, scope LearningScope, slug string) error
	// MigrateMastery atomically moves one (subject, node) fold onto a live
	// slug: target upsert + attempt re-tagging + source retirement commit
	// as one transaction, so a crash mid-move can neither duplicate the
	// person's mastery row nor strand direct-evidence attempts under the
	// stale slug.
	MigrateMastery(ctx context.Context, scope LearningScope, fromSlug, toSlug string, state *types.MasteryState) error
	// MoveSkip atomically relocates one skip declaration onto a live slug,
	// preserving the original declaration age.
	MoveSkip(ctx context.Context, scope LearningScope, fromSlug, toSlug string, createdAt time.Time) error
	// ListAllMastery returns every folded row across subjects and KBs. The
	// daily reconcile pass is its only caller; the table is bounded by
	// (people × nodes), which keeps a full scan honest at that cadence.
	ListAllMastery(ctx context.Context) ([]types.MasteryState, error)
	// BackfillDone reports whether the scope already carries its
	// bootstrap mark — the idempotency that makes the backfill task safe to
	// re-run AND safe across profile deletion (the mark lives outside the
	// deletable event history, so a deleted profile cannot resurrect).
	BackfillDone(ctx context.Context, scope LearningScope) (bool, error)
	// MarkBackfillDone records the scope's bootstrap mark. Callers write it
	// BEFORE appending backfill events: a mark without events can only
	// under-light, an event without a mark would duplicate folded weights.
	MarkBackfillDone(ctx context.Context, scope LearningScope) error
	// ListDocAffinity reads the memory subsystem's doc-affinity rows as the
	// backfill source. Reading the table here (rather than widening the
	// memory repository) keeps the learning layer self-contained and leaves
	// the memory subsystem untouched.
	ListDocAffinity(ctx context.Context) ([]types.MemoryDocAffinity, error)
	// ListDocAffinityByScope is the scoped variant for the recommend hot path.
	ListDocAffinityByScope(ctx context.Context, tenantID uint64, subjectID string) ([]types.MemoryDocAffinity, error)
	// GetQuizItemByID resolves a single quiz item without scanning pages.
	GetQuizItemByID(ctx context.Context, tenantID uint64, itemID string) (*types.LearningQuizItem, error)

	// ListQuizItemsByKB returns every quiz item of one KB in a single query.
	ListQuizItemsByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningQuizItem, error)
	// ListObjectives returns the KB's observable learning-objective
	// definitions in deterministic order (stage-1 evidence separation).
	ListObjectives(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningObjective, error)
	// UpsertObjective stores one objective definition by id (KB-shared
	// inventory; the review/publish chain arrives in stage 2).
	UpsertObjective(ctx context.Context, objective *types.LearningObjective) error
	// StaleQuizItemsByEvidence auto-invalidates one slug's active quiz items
	// whose frozen evidence version no longer matches the current material
	// digest (legacy empty hashes count as unknown and go stale too). The
	// stale status stops serving and scoring until regeneration; returns
	// the number of rows staled.
	StaleQuizItemsByEvidence(ctx context.Context, tenantID uint64, knowledgeBaseID, slug, currentHash string) (int64, error)
	// ListTopicStats reads the memory subsystem's topic statistics as the
	// channel-B mapping source, same direct-read precedent as above.
	ListTopicStats(ctx context.Context) ([]types.MemoryTopicStat, error)
	// UpsertTopicMap stores one adjudicated topic→slug mapping; rows below
	// the confidence floor are never handed here at all.
	UpsertTopicMap(ctx context.Context, m *types.MemoryWikiMap) error
	// ListMapsBySubject returns the subject's topic mappings across KBs
	// (export payload member).
	ListMapsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.MemoryWikiMap, error)
	// ListRecentEvents returns one page of the subject's newest-first
	// events plus the total count (timeline).
	ListRecentEvents(ctx context.Context, scope LearningScope, limit, offset int) ([]types.LearningEvent, int64, error)
	// ListEventsBySubject / ListMasteryBySubject / ListAttemptsBySubject
	// are the export payload members, subject-scoped by design: shared-KB
	// learning rows land under the KB owner's effective tenant, and the
	// export must cover every row that belongs to the person.
	ListEventsBySubject(ctx context.Context, subjectID string) ([]types.LearningEvent, error)
	ListMasteryBySubject(ctx context.Context, subjectID string) ([]types.MasteryState, error)
	ListAttemptsBySubject(ctx context.Context, subjectID string) ([]types.LearningQuizAttempt, error)
	// ListAllMapsBySubject is the export-side, subject-scoped variant of
	// ListMapsBySubject (which stays per-tenant for the maintenance pass).
	ListAllMapsBySubject(ctx context.Context, subjectID string) ([]types.MemoryWikiMap, error)
	// DeleteLearningDataBySubject removes the subject's events, states,
	// mappings and attempts in one sweep, across every workspace their
	// shared KBs may have filed rows under. The KB-shared quiz bank is not
	// personal data and is never touched.
	DeleteLearningDataBySubject(ctx context.Context, subjectID string) error
	// DeleteLearningDataByKB removes EVERY subject's learning data inside
	// one knowledge base — the orphan sweep for KBs that no longer exist.
	DeleteLearningDataByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) error
	// AddSkip records one standing "已掌握，不再推荐" declaration for the
	// scope (idempotent on conflict; createdAt preserved for alias moves).
	AddSkip(ctx context.Context, scope LearningScope, slug string, createdAt time.Time) error
	// RemoveSkip revokes one skip declaration (absent rows are a no-op).
	RemoveSkip(ctx context.Context, scope LearningScope, slug string) error
	// ListSkips returns the scope's skip declarations, slug → declared at.
	ListSkips(ctx context.Context, scope LearningScope) (map[string]time.Time, error)
	// ListSkipsBySubject is the export-side, subject-scoped variant: the
	// profile export must cover skips filed under every workspace the
	// person's shared KBs belong to.
	ListSkipsBySubject(ctx context.Context, subjectID string) ([]types.LearningSkip, error)
	// ListAllSkips returns every skip row across subjects and KBs — the
	// reconcile pass's skip-alias repair walks it exactly like ListAllMastery;
	// a subject with skips but no folded mastery must still be visited.
	ListAllSkips(ctx context.Context) ([]types.LearningSkip, error)

	// GetTaskByID resolves one structured task; ListTasks lists a KB's
	// tasks deterministically; UpsertTask stores one by id; task attempts
	// follow the quiz-attempt read patterns.
	GetTaskByID(ctx context.Context, tenantID uint64, taskID string) (*types.LearningTask, error)
	ListTasks(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningTask, error)
	UpsertTask(ctx context.Context, task *types.LearningTask) error
	InsertTaskAttempt(ctx context.Context, attempt *types.LearningTaskAttempt) error
	ListTaskAttempts(ctx context.Context, scope LearningScope) ([]types.LearningTaskAttempt, error)
	ListTaskAttemptsBySubject(ctx context.Context, subjectID string) ([]types.LearningTaskAttempt, error)

	// ---- Deletion epoch fence (profile delete serialization) ----

	// GetSubjectEpoch returns the subject's current deletion epoch. An absent
	// row is epoch 0 — collection is at its first generation.
	GetSubjectEpoch(ctx context.Context, subjectID string) (int64, error)
	// DeleteProfileData is the atomic profile deletion: the personal-data
	// sweep, the optional collection opt-out and the epoch bump all commit
	// (or roll back) as one transaction, so a crash can never leave a
	// half-deleted profile whose opt-out was never recorded.
	DeleteProfileData(ctx context.Context, tenantID uint64, subjectID string, optOut bool) error
	// ApplyTopicMapping stores one adjudicated topic mapping together with
	// its optional projection event and mastery fold in a single transaction,
	// fenced on the subject's deletion epoch: when the epoch moved past
	// expectedEpoch the whole write is discarded with ErrLearningEpochAdvanced.
	// The fold closure receives the current mastery row (nil when unseen) and
	// returns the row to persist; it runs inside the transaction so the
	// read-modify-write cycle cannot interleave a concurrent delete.
	ApplyTopicMapping(
		ctx context.Context, expectedEpoch int64, scope LearningScope,
		mapping *types.MemoryWikiMap, event *types.LearningEvent,
		fold func(existing *types.MasteryState) types.MasteryState,
	) error
}

// ErrLearningEpochAdvanced reports that the subject's deletion epoch no
// longer matches the value a writer captured before its work began: the
// profile was deleted (possibly on another server) while the write was in
// flight, and the write must be discarded instead of resurrecting deleted
// data.
var ErrLearningEpochAdvanced = errors.New("learning: subject epoch advanced, discarding stale write")

// LearningService is the write-path entry the QA handler calls after each
// completed answer, plus the idempotent history replay the reconcile
// runner triggers on startup. Failures are logged inside and never
// surface to the answer path.
type LearningService interface {
	UpdateReviewSchedule(context.Context, string, LearningReviewInput) (*LearningReviewStatus, error)
	GetPlanPreferences(context.Context, string) (*LearningPlanSettings, error)
	UpdatePlanPreferences(context.Context, string, LearningPlanSettings) (*LearningPlanSettings, error)
	// RecordAnswerTouches folds the knowledge nodes the answer's citations
	// touched into the caller's mastery. No-op unless LEARNING_ENABLE=true
	// and the subject has not opted out of collection.
	RecordAnswerTouches(ctx context.Context, assistantMessage *types.Message)
	// RecordWikiRead folds one deliberate wiki page open into the caller's
	// mastery (the §3.3.6 low-trust read signal). Deduped per slug per
	// re-ask window; rejects non-node slugs with ErrWikiReadTarget.
	// tier: "" / "normal" = the standard glance signal; "deep" = the reader
	// stayed past the deep-dwell threshold — its own event type, capped at
	// one per node per re-ask window.
	RecordWikiRead(ctx context.Context, kbID, slug, tier string) error
	// RecordAgentRead lands one agent wiki_read_page call as a zero-weight
	// timeline trace (agent_read). Tool access is activity, never the
	// person's learning: nothing folds, nothing refreshes, and the human
	// read's scoring window stays untouched. Own dedup window per slug.
	RecordAgentRead(ctx context.Context, kbID, slug string) error
	// RecordSelfAssess lands the skills-matrix self-assessment: "up" lifts
	// the frozen logit into the mastered band (the tier gate still demands
	// quiz proof), "down" demotes with a reason taxonomy (all / doc_gap /
	// doc_updated / quiz_easy) whose labels feed content maintenance.
	RecordSelfAssess(ctx context.Context, kbID, slug string, up bool, reason string) error
	// RecordSkip lands the user's standing queue-suppression declaration
	// ("已掌握，不再推荐") or revokes it. Unlike self-assessment it folds
	// nothing and expires never — the recommender simply stops offering the
	// node until the user takes the skip back.
	RecordSkip(ctx context.Context, kbID, slug string, skipped bool) error
	// RunBackfill replays doc-affinity history into learning events; safe
	// to call repeatedly (per-scope idempotent).
	RunBackfill(ctx context.Context) error
	// RunMaintenance runs the LLM write paths (topic mapping, prerequisite
	// edges, quiz generation) for every eligible scope and KB. The per-KB
	// learning_features switch gates the edge and quiz passes.
	RunMaintenance(ctx context.Context) error

	// ---- Stage-4 read paths (all deterministic, no LLM) ----

	// GetProgress assembles the tab header for one KB.
	GetProgress(ctx context.Context, kbID string) (*LearningProgress, error)
	// ListMasteryView derives every node's level and decayed probability.
	ListMasteryView(ctx context.Context, kbID string) ([]MasteryView, error)
	// Recommend produces the "look next" cards.
	Recommend(ctx context.Context, kbID string, limit int) ([]Recommendation, error)
	// ZoneMap produces the module-partitioned view: every wiki folder is a
	// knowledge zone with its own next step ("1") and follow-up ("2")
	// derived from the same multi-channel ranking, plus the full node set
	// (with zone/material context) and the prerequisite edges the
	// constellation renders as relation lines.
	ZoneMap(ctx context.Context, kbID string) (*ZoneMapResponse, error)
	// TakeQuiz serves a node's active questions without answer material.
	TakeQuiz(ctx context.Context, kbID, slug string) ([]QuizQuestion, error)
	// SubmitAnswer grades deterministically and folds the result (opted-out
	// subjects still get the verdict; nothing is stored). declaredAssistance
	// carries the trial's assistance condition ("" = the item's designed
	// mode); non-matching or assistant-helped trials record as practice and
	// never count as strict evidence.
	SubmitAnswer(ctx context.Context, kbID, itemID, chosenKey, declaredAssistance string) (*AnswerResult, error)
	// ReviewQuizItem applies a human content review to one quiz item:
	// approve publishes (with clone-safe family assignment and version
	// bumping on semantic change), reject disables with the audit trail.
	// Only authenticated human principals can review — never the LLM.
	ReviewQuizItem(ctx context.Context, kbID, itemID string, decision ReviewDecisionInput) (*types.LearningQuizItem, error)
	// ReviewObjective publishes/retires an objective definition.
	ReviewObjective(ctx context.Context, kbID, objectiveID string, decision ReviewDecisionInput) (*types.LearningObjective, error)
	// ReviewTask applies the review chain to a structured application task.
	ReviewTask(ctx context.Context, kbID, taskID string, decision ReviewDecisionInput) (*types.LearningTask, error)
	// TakeTask serves takeable published tasks WITHOUT answer keys.
	TakeTask(ctx context.Context, kbID, objectiveID string) ([]TaskTakePayload, error)
	// SubmitTaskAnswer grades a task server-side (exact critical-check
	// match); the client supplies field values only and cannot influence
	// the verdict.
	SubmitTaskAnswer(ctx context.Context, kbID, taskID string, answers map[string]string, declaredAssistance string) (*TaskSubmitPayload, error)
	// Timeline pages the caller's newest-first events.
	Timeline(ctx context.Context, kbID string, page, pageSize int) ([]TimelineItem, int64, error)
	// ExportProfile assembles the caller's full personal learning data.
	ExportProfile(ctx context.Context) (*ExportPayload, error)
	// DeleteProfile removes it, optionally recording the collection opt-out.
	DeleteProfile(ctx context.Context, optOut bool) error
	// GetSettings / UpdateSettings read and write the opt-out.
	GetSettings(ctx context.Context) (*LearningSettings, error)
	UpdateSettings(ctx context.Context, disabled bool) error
	// MasteryOverlay derives graph paint fields (level + low-confidence).
	MasteryOverlay(ctx context.Context, kbID string, slugs []string) (map[string]MasteryOverlayEntry, error)
	// PassiveChanges derives the decay-driven (non-action) state changes of
	// the caller's nodes in one KB: tiers already demoted by forgetting and
	// tiers within PassiveDueSoonDays of demotion. Entirely read-time —
	// nothing is persisted, nothing enters the event stream (the timeline
	// stays a log of real actions; passive drift is a separate channel).
	PassiveChanges(ctx context.Context, kbID string, limit int) (*PassiveChangesSummary, error)
	// ObjectiveProgress derives the separated progress header: verified
	// /total objectives with conflict/stale/partial/untested breakdowns,
	// contact coverage, self-report count. Never a probability.
	FreezeAssessment(ctx context.Context, kbID string) (*AssessmentFreeze, error)
	ObjectiveProgress(ctx context.Context, kbID string, goalIDs ...string) (*ObjectiveProgressSummary, error)
	// ShortPath derives the caller's 3–5 step cold-start/short path as
	// (node, objective, action) steps with structured reasons, evidence
	// refs, time estimates, completion conditions and conditional
	// successors. Deterministic for the same snapshot + policy version.
	ShortPath(ctx context.Context, kbID string, req ColdStartRequestPayload) (*LearningPathPlan, error)
	// ObjectiveView derives the separated evidence profile for one KB
	// (stage 1): per (node, objective) the five dimensions — exposure,
	// self_report, objective_evidence, recency, path_status — and the
	// contract-derived state. Read-time pure derivation from server-side
	// facts; scope comes from the authenticated caller alone.
	ObjectiveView(ctx context.Context, kbID string) (*ObjectiveViewResponse, error)
	SetNodeState(ctx context.Context, kbID, slug, state string) error
}

// ObjectiveEvidenceView is the direct-evidence dimension of one
// objective, derived at read time from eligible (independent, graded)
// attempts frozen with family/objective metadata.
type ObjectiveEvidenceView struct {
	FamiliesPassed         []string  `json:"families_passed"`
	FamiliesPassedHistoric []string  `json:"families_passed_historic"`
	EligiblePasses         int       `json:"eligible_passes"`
	EligibleFailures       int       `json:"eligible_failures"`
	LastPassAt             time.Time `json:"last_pass_at,omitempty"`
	LastFailureAt          time.Time `json:"last_failure_at,omitempty"`
	ContractMetAt          time.Time `json:"contract_met_at,omitempty"`
	StalePasses            int       `json:"stale_passes"`
	LegacyAttempts         int       `json:"legacy_attempts"`
	// Source is "quiz" or "legacy_unverified" (metadata-less history:
	// visible, exportable, never promoting).
	Source string `json:"source"`
	// Unknown: no attempt references the objective at all — the honest
	// "no evidence" state, never a sigmoid(0)=50%.
	Unknown bool `json:"unknown,omitempty"`
}

// ObjectiveViewEntry is one (node, objective) row of the separated
// profile: five independent dimensions — exposure, self_report,
// objective_evidence, recency, path_status — plus the contract-derived
// state. No dimension derives another; weak signals and time passage
// never modify the evidence dimension.
type ObjectiveViewEntry struct {
	Slug            string `json:"slug"`
	ObjectiveID     string `json:"objective_id"`
	Title           string `json:"title"`
	Behavior        string `json:"behavior"`
	CapabilityType  string `json:"capability_type"`
	ContractType    string `json:"contract_type"`
	ContractVersion string `json:"contract_version"`
	ContentVersion  string `json:"content_version"`
	// ObjectiveStatus is draft/published/retired; non-published
	// objectives never derive verified (unreviewed content cannot
	// certify ability).
	ObjectiveStatus string                `json:"objective_status"`
	State           string                `json:"state"`
	Evidence        ObjectiveEvidenceView `json:"evidence"`
	// Exposure counts contact facts only.
	Exposure struct {
		Reads      int       `json:"reads"`
		Cites      int       `json:"cites"`
		AgentReads int       `json:"agent_reads"`
		LastAt     time.Time `json:"last_at,omitempty"`
	} `json:"exposure"`
	// SelfReport is the user's own claim — never a system verification.
	SelfReport struct {
		Direction string    `json:"direction,omitempty"` // up | down
		At        time.Time `json:"at,omitempty"`
		Skipped   bool      `json:"skipped"`
	} `json:"self_report"`
	// Recency reports timestamps only; it never demotes a verified fact.
	Recency struct {
		LastVerifiedAt time.Time `json:"last_verified_at,omitempty"`
		LastEvidenceAt time.Time `json:"last_evidence_at,omitempty"`
		LastExposureAt time.Time `json:"last_exposure_at,omitempty"`
	} `json:"recency"`
	// PathStatus: available | user_retired | challenge_pending.
	PathStatus string `json:"path_status"`
}

// ObjectiveProgressSummary is the stage-4 separated progress header:
// explainable counts, never a "mastery probability". All three coverage
// numbers coexist: verified-objective coverage, contact coverage, and the
// full-library objective denominator — removing a goal or skipping a node
// never inflates the full-library coverage.
type ObjectiveProgressSummary struct {
	// VerifiedObjectives / TotalObjectives: the primary progress pair.
	// "已验证 2/6 目标" — never a probability.
	VerifiedObjectives int `json:"verified_objectives"`
	TotalObjectives    int `json:"total_objectives"`
	// ConflictingObjectives: currently conflicting (re-verification owed).
	ConflictingObjectives int `json:"conflicting_objectives"`
	// StaleObjectives: content re-versioned, evidence pending re-check.
	StaleObjectives int `json:"stale_objectives"`
	// PartialObjectives: one family passed, contract not yet met.
	PartialObjectives int `json:"partial_objectives"`
	// UntestedObjectives: no eligible attempt has ever landed.
	UntestedObjectives   int `json:"untested_objectives"`
	UnverifiedObjectives int `json:"unverified_objectives"`
	// ContactCoverage: nodes the user has actually opened/cited (contact
	// is exposure, not ability — displayed separately, never conflated).
	ContactNodes int `json:"contact_nodes"`
	TotalNodes   int `json:"total_nodes"`
	// SelfReportCount: standing user claims (up/down) — never verified.
	SelfReportCount int `json:"self_report_count"`
	// LegacyAttemptCount: pre-stage metadata-less attempts, visible but
	// never promoting.
	LegacyAttemptCount int `json:"legacy_attempt_count"`
	// GoalSetVersion identifies the current goal scope: changing the goal
	// set creates a new version; removing a goal changes "本次目标完成度"
	// but never inflates the full-library coverage above.
	GoalSetVersion string `json:"goal_set_version,omitempty"`
	// GoalVerifiedObjectives / GoalTotalObjectives: the SELECTED goal
	// scope's coverage pair — displayed alongside (never merged with) the
	// full-library coverage.
	GoalVerifiedObjectives int `json:"goal_verified_objectives"`
	GoalTotalObjectives    int `json:"goal_total_objectives"`
}

// ColdStartRequestPayload is the short-path request: goal objectives,
// depth, time budget and the VOLUNTARY fast-track challenge.
type ColdStartRequestPayload struct {
	UseMemory      *bool    `json:"use_memory,omitempty"`
	GoalSlugs      []string `json:"goal_slugs,omitempty"`
	ExcludedSlugs  []string `json:"excluded_slugs,omitempty"`
	GoalObjectives []string `json:"goal_objectives"`
	Depth          string   `json:"depth"`
	TimeBudgetMin  int      `json:"time_budget_minutes"`
	FastTrack      bool     `json:"fast_track"`
}

// LearningPathPlan mirrors the service plan for transport.
type LearningPathPlan struct {
	Personalization string             `json:"personalization,omitempty"`
	PolicyVersion   string             `json:"policy_version"`
	Steps           []LearningPathStep `json:"steps"`
	Degrade         string             `json:"degrade,omitempty"`
	FocusSlug       string             `json:"focus_slug,omitempty"`
	FocusTitle      string             `json:"focus_title,omitempty"`
	Progression     string             `json:"progression,omitempty"`
	Limitations     []string           `json:"limitations,omitempty"`
}

// LearningPathStep is one (node, objective, action) step.
type LearningPathStep struct {
	ID          string `json:"id"`
	Completed   bool   `json:"completed"`
	Slug        string `json:"slug"`
	Title       string `json:"title,omitempty"`
	Objective   string `json:"objective,omitempty"`
	Action      string `json:"action"`
	Minutes     int    `json:"minutes"`
	DoneWhen    string `json:"done_when"`
	Eligibility string `json:"eligibility"`
	Requires    string `json:"requires,omitempty"`
	Next        string `json:"next,omitempty"`
	Reason      struct {
		Code     string   `json:"code"`
		Detail   string   `json:"detail,omitempty"`
		Evidence []string `json:"evidence,omitempty"`
	} `json:"reason"`
}

// ReviewDecisionInput is the review API payload (mirrors the service's
// ReviewDecision for transport).
type ReviewDecisionInput struct {
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
	Note        string `json:"note"`
	ChangeKind  string `json:"change_kind"`
	ObjectiveID string `json:"objective_id,omitempty"`
	FamilyID    string `json:"family_id,omitempty"`
}

// TaskTakePayload is one takeable task without the answer key.
type TaskTakePayload struct {
	FamilyID       string          `json:"family_id"`
	Mode           string          `json:"mode"`
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Scenario       string          `json:"scenario"`
	Fields         json.RawMessage `json:"fields"`
	CriticalChecks json.RawMessage `json:"critical_checks,omitempty"`
	AssistanceMode string          `json:"assistance_mode"`
	ContentVersion string          `json:"content_version"`
	RubricVersion  string          `json:"rubric_version"`
	ObjectiveID    string          `json:"objective_id"`
}

// TaskCheckResultPayload is one critical check's verdict (no correct
// values are revealed).
type TaskCheckResultPayload struct {
	ID     string `json:"id"`
	Passed bool   `json:"passed"`
}

// TaskSubmitPayload is the server-computed verdict of one task submission.
type TaskSubmitPayload struct {
	Passed      bool                     `json:"passed"`
	Checks      []TaskCheckResultPayload `json:"checks"`
	Assistance  string                   `json:"assistance_mode"`
	Eligible    bool                     `json:"eligible"`
	GradeReason string                   `json:"grade_reason"`
}

// ObjectiveViewResponse is the stage-1 separated evidence profile of one
// KB for the authenticated caller.
type LearningNodeView struct {
	Estimate          *LearningEstimate     `json:"estimate,omitempty"`
	Review            *LearningReviewStatus `json:"review,omitempty"`
	Slug              string                `json:"slug"`
	Title             string                `json:"title"`
	FolderID          string                `json:"folder_id"`
	FolderName        string                `json:"folder_name"`
	State             string                `json:"state"` // unseen | learning | self_known | verified | review
	Reads             int                   `json:"reads"`
	Cites             int                   `json:"cites"`
	LastReadAt        time.Time             `json:"last_read_at,omitempty"`
	DeclaredAt        time.Time             `json:"declared_at,omitempty"`
	Updated           bool                  `json:"updated"`
	ObjectiveTotal    int                   `json:"objective_total"`
	ObjectiveVerified int                   `json:"objective_verified"`
}

type ObjectiveViewResponse struct {
	Nodes             []LearningNodeView   `json:"nodes"`
	ProjectionVersion string               `json:"projection_version"`
	Entries           []ObjectiveViewEntry `json:"entries"`
	// LegacyByNode counts metadata-less attempts per node: visible and
	// exported, but never strict verification evidence.
	LegacyByNode map[string]int `json:"legacy_by_node,omitempty"`
}

// ZoneMapResponse is the module-partitioned recommendation surface: the
// constellation's nodes-and-edges payload plus one "next step" card per
// knowledge zone (wiki folder). Zones are ordered best-next-first so the
// sidebar reads as a set of parallel entry points, one per module.
type ZoneMapResponse struct {
	Zones []ZoneSummary `json:"zones"`
	Nodes []ZoneNode    `json:"nodes"`
	Edges []ZoneEdge    `json:"edges"`
}

// ZoneSummary is one module knowledge zone and its next steps. Next/Second
// come from the SAME global ranking as the linear recommendation — the
// zone view partitions that ranking per folder instead of mixing modules.
type ZoneSummary struct {
	FolderID   string `json:"folder_id"`
	FolderName string `json:"folder_name"`
	Total      int    `json:"total"`
	Lit        int    `json:"lit"`
	// Next is the zone's current "1" (nil when the zone is complete —
	// everything mastered or user-retired). Second is its "2", if any.
	Next   *Recommendation `json:"next,omitempty"`
	Second *Recommendation `json:"second,omitempty"`
}

// ZoneNode is one node of the constellation payload: the mastery-view
// fields plus the zone/material context the 3D layout and hover states
// consume (folder, chapter label, material position).
type ZoneNode struct {
	Slug           string    `json:"slug"`
	Title          string    `json:"title"`
	Level          string    `json:"level"`
	PEff           float64   `json:"p_eff"`
	EvidenceCount  int       `json:"evidence_count"`
	LowConfidence  bool      `json:"low_confidence"`
	LastActivityAt time.Time `json:"last_activity_at"`
	Recent         bool      `json:"recent,omitempty"`
	Skipped        bool      `json:"skipped,omitempty"`
	Faded          bool      `json:"faded,omitempty"`
	FolderID       string    `json:"folder_id"`
	FolderName     string    `json:"folder_name,omitempty"`
	Section        string    `json:"section,omitempty"`
	DocTitle       string    `json:"doc_title,omitempty"`
	DocRank        int       `json:"doc_rank,omitempty"`
}

// ZoneEdge is one prerequisite relation for the constellation's relation
// lines. Cross-zone links (from/to in different folders) render dashed —
// the inter-module dependency the zones cannot hide.
type ZoneEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind: "prereq" (LLM-adjudicated prerequisite, solid line) or
	// "wikilink" (wiki OutLink between node pages, lighter curve).
	Kind string `json:"kind,omitempty"`
}

// LearningProgress is the tab header: node totals per tier and per folder.
type LearningProgress struct {
	TotalNodes int                    `json:"total_nodes"`
	LitNodes   int                    `json:"lit_nodes"`
	Levels     map[string]int         `json:"levels"`
	Units      []LearningUnitProgress `json:"units"`
	// Today is the daily-engagement digest: answers, correct count, nodes
	// first lit today, and the consecutive-day learning streak. Derived
	// from the same events the timeline shows (no new collection), the
	// Duolingo-style daily loop without its game economy.
	Today *TodaySummary `json:"today,omitempty"`
}

// TodaySummary is the per-day engagement digest on the progress header.
type TodaySummary struct {
	Answers      int `json:"answers"`
	CorrectCount int `json:"correct_count"`
	LitToday     int `json:"lit_today"`
	StreakDays   int `json:"streak_days"`
}

// LearningUnitProgress is one folder's rolled-up progress.
type LearningUnitProgress struct {
	FolderID string `json:"folder_id"`
	// FolderName is the folder's human-readable name from wiki_folders;
	// empty for the wiki root, which the client renders as "root".
	FolderName string `json:"folder_name"`
	Total      int    `json:"total"`
	Lit        int    `json:"lit"`
}

// MasteryView is one node's read-time derived mastery.
type MasteryView struct {
	Slug           string    `json:"slug"`
	Level          string    `json:"level"`
	PEff           float64   `json:"p_eff"`
	EvidenceCount  int       `json:"evidence_count"`
	LowConfidence  bool      `json:"low_confidence"`
	LastEvidenceAt time.Time `json:"last_evidence_at"`
	// LastActivityAt is the newest raw event time for the node, including
	// zero-weight activity (deduped re-reads, quiz_unsure answers) that
	// deliberately does not fold into the mastery state. The constellation
	// reads it for the "studied within 48h" twinkle: a node the learner
	// touched today must glow even when the touch carried no score. Zero
	// when the node has no events at all (consumers fall back to
	// LastEvidenceAt).
	LastActivityAt time.Time `json:"last_activity_at"`
	// Title is the node page's human-readable title, resolved in one batch
	// at read time (empty when the page no longer resolves).
	Title string `json:"title,omitempty"`
	// TierProgress/NextTierHint: same semantics as on Recommendation — the
	// continuous progress bar inside the tier band and the transparent
	// path to the next promotion.
	TierProgress float64 `json:"tier_progress,omitempty"`
	NextTierHint string  `json:"next_tier_hint,omitempty"`
	// SelfAssess is the latest self-assessment inside the visibility window,
	// nil when the person never challenged this node. Display-only: it never
	// feeds the tier math (the fold already consumed the set-point weight).
	SelfAssess *SelfAssessMark `json:"self_assess,omitempty"`
	// Skipped marks a standing user declaration ("已掌握，不再推荐"): the
	// recommender excludes the node and the client badges it; SkippedAt is
	// when the declaration was made (nil when not skipped — a bare
	// time.Time would serialize the zero value on every row).
	Skipped   bool       `json:"skipped,omitempty"`
	SkippedAt *time.Time `json:"skipped_at,omitempty"`
}

// SelfAssessMark is one self-assessment as the read side surfaces it.
type SelfAssessMark struct {
	Direction string `json:"direction"` // "up" | "down"
	// EventType is the raw event type; for "down" it encodes the reason
	// (…_all / …_doc_gap / …_doc_updated / …_quiz_easy).
	EventType string    `json:"event_type"`
	At        time.Time `json:"occurred_at"`
}

// SelfAssessVisibleWindow bounds how long a self-assessment stays visible
// on hover cards — fresher than the twinkle window by design: a challenge
// is a conversation, not a state.
const SelfAssessVisibleWindow = 7 * 24 * time.Hour

// PassiveChange is one node's decay-driven (non-action) state change,
// derived entirely at read time. The Anki deck-list split, transposed:
// passive drift (forgetting) is summarized as counts plus a due queue and
// never interleaved into the activity history, so passive volume can never
// flood out the user's own actions in the timeline.
type PassiveChange struct {
	Slug string `json:"slug"`
	// Title is the node page's human-readable title (empty when the page
	// no longer resolves; the client falls back to the slug).
	Title string `json:"title,omitempty"`
	// AnchorLevel is the tier the accumulated evidence earned, ignoring
	// decay; ViewLevel is what forgetting has worn it down to right now.
	AnchorLevel string `json:"anchor_level"`
	ViewLevel   string `json:"view_level"`
	// BaseP/PEff show the same split numerically: p without decay vs p_eff.
	BaseP float64 `json:"base_p"`
	PEff  float64 `json:"p_eff"`
	// DaysIdle is days since the last evidence (0 for fresh nodes).
	DaysIdle float64 `json:"days_idle"`
	// StabilityDays is the node's current stability s (read as "days until
	// retrievability falls to 90%").
	StabilityDays float64 `json:"stability_days"`
	// NextReviewDays is how many days the node still holds its anchor tier
	// before decay crosses the demotion gate; nil when already demoted
	// (the item's action line then reads "review now").
	NextReviewDays *float64 `json:"next_review_days,omitempty"`
	// Demoted marks nodes whose forgetting already crossed a demotion gate
	// (anchor tier strictly above the decayed view).
	Demoted bool `json:"demoted"`
	// LowConfidence mirrors the mastery honesty flag (< 3 evidence).
	LowConfidence  bool      `json:"low_confidence"`
	LastEvidenceAt time.Time `json:"last_evidence_at"`
}

// PassiveChangesSummary aggregates the passive channel for one KB: full-set
// counts plus the most urgent items (demotions first, then soonest due).
type PassiveChangesSummary struct {
	DemotedCount int             `json:"demoted_count"`
	DueSoonCount int             `json:"due_soon_count"`
	Items        []PassiveChange `json:"items"`
}

// QuizQuestion is the served question shape (no answer material).
type QuizQuestion struct {
	ObjectiveID    string            `json:"objective_id,omitempty"`
	FamilyID       string            `json:"family_id,omitempty"`
	AssistanceMode string            `json:"assistance_mode,omitempty"`
	ID             string            `json:"id"`
	Question       string            `json:"question"`
	Options        map[string]string `json:"options"`
	ChunkRefs      []string          `json:"chunk_refs"`
	// SourceDocs resolves the cited chunks back to their source documents
	// (id + title + how many of the item's chunks live there), so the
	// client can link "this question came from that document". Derived
	// deterministically at serve time; empty when the evidence cannot be
	// resolved (e.g. chunks deleted since generation).
	SourceDocs []QuizSourceDoc `json:"source_docs,omitempty"`
	// Mode labels the serving tier: "verification" (human-reviewed) or
	// "practice" (LLM draft / legacy). The answer key is never present.
	Mode string `json:"mode,omitempty"`
}

// QuizSourceDoc is one source document behind a quiz item.
type QuizSourceDoc struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title,omitempty"`
	ChunkCount  int    `json:"chunk_count"`
}

// AnswerResult is the response after a submission.
type AnswerResult struct {
	Eligible    bool     `json:"eligible"`
	GradeReason string   `json:"grade_reason"`
	Correct     bool     `json:"correct"`
	CorrectKey  string   `json:"correct_key"`
	Explanation string   `json:"explanation"`
	ChunkRefs   []string `json:"chunk_refs"`
	// NextReviewDays is the deterministic review schedule: how many days
	// the freshly folded state stays above its tier's demotion threshold
	// ("下次复习约在 N 天后"). Nil when there is nothing to retain yet or
	// the state already sits below the threshold (review now).
	NextReviewDays *float64 `json:"next_review_days,omitempty"`
	// Unsure marks an "I'm not sure" declaration: graded as neither right
	// nor wrong (zero-weight event), so the client can phrase the feedback
	// honestly instead of scolding an admitted guess.
	Unsure bool `json:"unsure,omitempty"`
	// PEffBefore/PEffAfter bracket the submission's effect on the node's
	// decayed mastery (the fast feedback: "45% → 58%"). Nil for unsure
	// answers (no fold happens) and when opted out of collection.
	PEffBefore *float64 `json:"p_eff_before,omitempty"`
	PEffAfter  *float64 `json:"p_eff_after,omitempty"`
}

// TimelineItem is one event on the lighting timeline.
type TimelineItem struct {
	EventType string `json:"event_type"`
	Slug      string `json:"slug"`
	// Title is the touched page's human-readable title, resolved in one
	// batch at read time; empty when the page no longer resolves (the
	// client falls back to the slug).
	Title      string    `json:"title"`
	PageType   string    `json:"page_type,omitempty"`
	Weight     float64   `json:"weight"`
	OccurredAt time.Time `json:"occurred_at"`
	SessionID  string    `json:"session_id"`
	MessageID  string    `json:"message_id"`
}

// ExportPayload is the data-sovereignty export.
type ExportPayload struct {
	PlanPreferences []types.LearningPlanPreference `json:"plan_preferences"`
	ExportedAt      time.Time                      `json:"exported_at"`
	// KBSummary groups the payload by knowledge base at the top so the
	// exported file reads as "what I did where" at a glance; KBs that no
	// longer exist (deleted after the data was collected) are marked with
	// Exists=false so their group is self-explanatory.
	KBSummary []ExportKBSummary           `json:"kb_summary"`
	Events    []types.LearningEvent       `json:"events"`
	Mastery   []types.MasteryState        `json:"mastery"`
	TopicMaps []types.MemoryWikiMap       `json:"topic_maps"`
	Attempts  []types.LearningQuizAttempt `json:"quiz_attempts"`
	// Skips are the standing "已掌握，不再推荐" declarations — a preference
	// the user set, hence part of the profile they can view and export.
	Skips []types.LearningSkip `json:"skips"`
	// TaskAttempts are the structured-task submissions — personal data,
	// exported with the same containment as quiz attempts.
	TaskAttempts []types.LearningTaskAttempt `json:"task_attempts,omitempty"`
	// Objectives is the derived separated evidence profile (stage 1):
	// per (node, objective) state + evidence dimensions at export time.
	// Legacy attempts without objective metadata stay visible here and in
	// Attempts; they never fabricate strict verification states.
	Objectives []ObjectiveExportRow `json:"objectives,omitempty"`
}

// ObjectiveExportRow is one objective's derived profile inside the export.
type ObjectiveExportRow struct {
	types.LearningObjective
	State    string                `json:"state"`
	Evidence ObjectiveEvidenceView `json:"evidence"`
	Source   string                `json:"source"`
}

// ExportKBSummary is one knowledge base's roll-up inside the export.
type ExportKBSummary struct {
	KbID         string `json:"kb_id"`
	KbName       string `json:"kb_name,omitempty"`
	Exists       bool   `json:"exists"`
	Events       int    `json:"events"`
	MasteryNodes int    `json:"mastery_nodes"`
	Attempts     int    `json:"attempts"`
	Skips        int    `json:"skips"`
}

// LearningSettings is the collection opt-out surface.
type LearningSettings struct {
	CollectDisabled bool `json:"collect_disabled"`
}

// MasteryOverlayEntry is what the wiki graph handler paints nodes with.
type MasteryOverlayEntry struct {
	Level         string `json:"mastery_level"`
	LowConfidence bool   `json:"low_confidence"`
}

// Recommendation is one "what to look at next" card: a node, why it is
// relevant, and whether practice material exists for it.
type Recommendation struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Reason  string `json:"reason"`
	HasQuiz bool   `json:"has_quiz"`
	// Display extras derived once at read time in the service layer, so the
	// pure recommender stays free of them: the node's wiki folder, its
	// active quiz count, and its current derived tier.
	FolderName string `json:"folder_name,omitempty"`
	QuizCount  int    `json:"quiz_count,omitempty"`
	Level      string `json:"level,omitempty"`
	// Explainability numbers (deterministic, read-path): the decayed
	// mastery probability, the evidence split, and Faded — an unseen-tier
	// node that HAS learning history (wrong answers / decay sank it below
	// the lit threshold), which the client renders as 已淡化 rather than
	// the misleading 未接触.
	PEff          float64 `json:"p_eff,omitempty"`
	EvidenceCount int     `json:"evidence_count,omitempty"`
	PositiveCount int     `json:"positive_count,omitempty"`
	NegativeCount int     `json:"negative_count,omitempty"`
	Faded         bool    `json:"faded,omitempty"`
	// TierProgress is the fast feedback variable: p_eff's position inside
	// the current tier band (0..1). It moves with every answer even when
	// the (gated) tier does not, so progress is continuous while promotion
	// is earned.
	TierProgress float64 `json:"tier_progress,omitempty"`
	// NextTierHint names the single most useful next action toward the next
	// tier (e.g. answer one new quiz item / another item ≥48h later),
	// making the gate's requirements a visible goal instead of hidden
	// arithmetic.
	NextTierHint string `json:"next_tier_hint,omitempty"`
	// DocRank is the node's 1-based position in the source material (the
	// 从浅入深 channel): 1 = the material's opening content. Zero when the
	// position could not be resolved from chunk/document evidence — the
	// client hides the marker then. Display-only.
	DocRank int `json:"doc_rank,omitempty"`
	// Section is the node's chapter-scale label from the source material's
	// heading structure ("" when the material carries no headings).
	Section string `json:"section,omitempty"`
	// DocTitle is the node's home document title ("" when unresolvable).
	DocTitle string `json:"doc_title,omitempty"`
	// Why is the narrative key for the one-line learning reason
	// (承上启下): continues_prereq | same_section | continues_prev |
	// first_stop | material_start | chapter_of | in_doc. Empty = no line.
	Why string `json:"why,omitempty"`
	// WhyRef backs the narrative: the REFERENCED node's slug for the
	// continues_*/same_section keys (the service layer translates it to a
	// title before serving; never shipped raw). Display-only.
	WhyRef string `json:"why_ref,omitempty"`
}

// ErrLearningCollectionDisabled rejects telemetry inside the durable consent boundary.
var ErrLearningCollectionDisabled = errors.New("learning: collection disabled")

// LearningContextCapturer is optional for integrations and test doubles. Capture
// before starting a model/tool, never after its detached callback is scheduled.
type LearningContextCapturer interface {
	CaptureCollectionContext(context.Context) context.Context
}

// AssessmentFreeze contains no challenge or labels. Persist it before presenting a held-out family.
type AssessmentFreeze struct {
	LearningModelVersion   string                       `json:"learning_model_version"`
	NodeEstimates          map[string]*LearningEstimate `json:"node_estimates"`
	SnapshotID             string                       `json:"snapshot_id"`
	KnowledgeBaseID        string                       `json:"knowledge_base_id"`
	PolicyVersion          string                       `json:"policy_version"`
	StateAsOf              time.Time                    `json:"state_as_of"`
	GoalStatesBefore       map[string]string            `json:"goal_states_before"`
	ObjectiveVersions      map[string]string            `json:"objective_versions"`
	EvidenceFamiliesBefore []string                     `json:"evidence_families_before"`
	// Component fields preserve the current KC level; familiar is check support,
	// not the legacy objective contract's verified state.
	ComponentModelVersion           string              `json:"component_model_version,omitempty"`
	ComponentStatesBefore           map[string]string   `json:"component_states_before,omitempty"`
	ComponentVersions               map[string]string   `json:"component_versions,omitempty"`
	ComponentAvailable              map[string]bool     `json:"component_available,omitempty"`
	ComponentEvidenceFamiliesBefore []string            `json:"component_evidence_families_before"`
	ComponentCheckIDsBefore         map[string][]string `json:"component_check_ids_before"`
	// Submitted checks are observable, including old versions and practice.
	// Merely displaying a check or seeing it elsewhere is not recorded.
	ComponentExposureComplete bool   `json:"component_exposure_complete"`
	ComponentExposureNote     string `json:"component_exposure_note"`
}
