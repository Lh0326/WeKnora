package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// Learning event types, appended immutably to learning_events and folded into
// mastery_states. Weights are applied at fold time and frozen on the event row
// so that a later constants change can never rewrite history: replaying the
// events must always reproduce the state that was visible at the time.
const (
	// LearningEventAnswerCite: the answer cited a chunk/document that this
	// node's ChunkRefs/SourceRefs also cover. The workhorse positive signal.
	LearningEventAnswerCite = "answer_cite"
	// LearningEventReAsk: the same node was asked about again inside the
	// re-ask window, which reads as "the previous answer did not land".
	LearningEventReAsk = "re_ask"
	// LearningEventCrossRef: the node was touched again from a different
	// question outside the re-ask window, which reads as deliberate return.
	LearningEventCrossRef = "cross_ref"
	// LearningEventTopicSignal: a memory topic was mapped onto this node by
	// channel B. Indirect, so it is discounted at fold time.
	LearningEventTopicSignal = "topic_signal"
	// LearningEventQuizCorrect: the person answered a grounded quiz item on
	// this node correctly. The only direct mastery evidence in the system.
	LearningEventQuizCorrect = "quiz_correct"
	// LearningEventQuizWrong: the person answered a grounded quiz item on
	// this node wrongly. The only direct negative mastery evidence.
	LearningEventQuizWrong = "quiz_wrong"
	// LearningEventQuizUnsure: the person declared "not sure" instead of
	// guessing. Carries zero weight — an honest admission is neither
	// evidence of mastery nor of failure — but lands in the timeline as a
	// metacognitive trace and counts toward repeat-attempt decay so
	// unsure-then-answer within the window still earns decayed weight.
	LearningEventQuizUnsure = "quiz_unsure"
	// LearningEventBackfillCite: historical doc affinity replayed as an
	// event. Document-grained, so it carries the backfill discount.
	LearningEventBackfillCite = "backfill_cite"
	// LearningEventWikiDeepRead: the reader stayed on the node page past
	// the deep-dwell threshold (frontend tier "deep") — a studied read,
	// not a glance. Capped at one per node per re-ask window; weight is
	// the tier constant WeightWikiDeepRead.
	LearningEventWikiDeepRead = "wiki_deep_read"

	// LearningEventWikiToolRead: an agent wiki_read_page call, collected
	// opportunistically where a hook exists. May never be produced.
	LearningEventWikiToolRead = "wiki_tool_read"
	// LearningEventSelfAssessUp: the person self-assessed "I know this
	// better than my tier" (skills-matrix self-assessment track). The write
	// path lifts the frozen logit straight into the mastered band so the
	// progress is instantly visible — but self-assessment is indirect
	// evidence, so the direct-evidence gate still caps the displayed tier
	// until quiz facts arrive: the system is saying "prove it".
	LearningEventSelfAssessUp = "self_assess_up"
	// LearningEventSelfAssessDownAll: "not proficient at all — I mis-clicked
	// earlier". Resets the fold to the logit floor; a full restart.
	LearningEventSelfAssessDownAll = "self_assess_down_all"
	// LearningEventSelfAssessDownDocGap: "the docs don't cover the part I'm
	// weak on". Demotes one band and records a content-gap label.
	LearningEventSelfAssessDownDocGap = "self_assess_down_doc_gap"
	// LearningEventSelfAssessDownDocUpdated: "the docs gained new content I
	// haven't learned". Demotes one band and records a staleness label.
	LearningEventSelfAssessDownDocUpdated = "self_assess_down_doc_updated"
	// LearningEventSelfAssessDownQuizEasy: "I only passed because the quiz
	// was too easy". Demotes one band and records a quiz-validity label.
	LearningEventSelfAssessDownQuizEasy = "self_assess_down_quiz_easy"
)

// learning_edges stores only prerequisite relations; related/none pairs are
// adjudicated away and never persisted.
const LearningEdgePrerequisite = "prerequisite"

// Edge provenance: heuristic candidates confirmed by the LLM adjudicator, or
// a human edit through the management surface.
const (
	LearningEdgeSourceHeuristicLLM = "heuristic+llm"
	LearningEdgeSourceManual       = "manual"
)

// Quiz item lifecycle: generated items are active until a human disables
// them; disabling keeps history (attempts stay) while removing the item from
// serving.
const (
	LearningQuizStatusActive   = "active"
	LearningQuizStatusDisabled = "disabled"
)

// memory_wiki_map provenance.
const (
	LearningMapDecidedByLLM    = "llm"
	LearningMapDecidedByManual = "manual"
)

// QuizOptions holds the four options of a quiz item keyed "A".."D". Stored as
// a JSON object so the payload survives unchanged on both PostgreSQL (JSONB)
// and SQLite (TEXT).
type QuizOptions map[string]string

func (o QuizOptions) Value() (driver.Value, error) {
	if o == nil {
		return json.Marshal(map[string]string{})
	}
	return json.Marshal(o)
}

func (o *QuizOptions) Scan(value interface{}) error {
	if value == nil {
		*o = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*o = nil
		return nil
	}
	if len(b) == 0 {
		*o = nil
		return nil
	}
	return json.Unmarshal(b, o)
}

// RefList holds chunk (or document) reference ids as a JSON array, used for
// the quiz grounding contract: every produced item must cite chunk ids that
// are a subset of the page's own ChunkRefs.
type RefList []string

func (r RefList) Value() (driver.Value, error) {
	if r == nil {
		return json.Marshal([]string{})
	}
	return json.Marshal(r)
}

func (r *RefList) Scan(value interface{}) error {
	if value == nil {
		*r = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*r = nil
		return nil
	}
	if len(b) == 0 {
		*r = nil
		return nil
	}
	return json.Unmarshal(b, r)
}

// LearningEvent is the append-only fact source of the mastery layer. Every
// state can be rebuilt by replaying these rows, which is what makes
// reconciliation, backfill and constants retuning safe operations.
type LearningEvent struct {
	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// The scope is declared as an index on the model, not only in the
	// migration, so near-window re-ask lookups stay index-backed on every
	// database the model is auto-migrated onto.
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_learning_events_scope,priority:1;index:idx_learning_events_subject_time,priority:1"`
	// SubjectID is Principal.StorageID(), matching the memory subsystem's
	// convention so the two scopes compose without a mapping.
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;index:idx_learning_events_scope,priority:2;index:idx_learning_events_subject_time,priority:2"`
	// KnowledgeBaseID scopes events to one wiki graph.
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_events_scope,priority:3"`
	// Slug carries the type prefix (e.g. "concept/rag") and is the node
	// identity everywhere in the learning layer.
	Slug string `json:"slug" gorm:"type:varchar(512);not null;index:idx_learning_events_scope,priority:4"`
	// Type is one of the LearningEvent* constants.
	Type string `json:"event_type" gorm:"column:event_type;type:varchar(32);not null"`
	// Weight is frozen at append time; see the event-type constants block.
	Weight float64 `json:"weight" gorm:"not null;default:0"`
	// SessionID/MessageID trace the event back to the conversation that
	// produced it, keeping every mastery number explainable.
	SessionID  string    `json:"session_id" gorm:"type:varchar(36)"`
	MessageID  string    `json:"message_id" gorm:"type:varchar(36)"`
	OccurredAt time.Time `json:"occurred_at" gorm:"not null;index:idx_learning_events_scope,priority:5;index:idx_learning_events_subject_time,priority:3"`
	CreatedAt  time.Time `json:"created_at"`
}

func (LearningEvent) TableName() string { return "learning_events" }

// MasteryState is the folded materialisation of the events for one
// (person, node) pair. It deliberately stores neither the level nor the
// decayed probability: both are derived at read time so a stability or
// threshold change applies to history without a rewrite pass.
type MasteryState struct {
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_states_scope,priority:1"`
	// SubjectID is Principal.StorageID().
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_mastery_states_scope,priority:2"`
	// KnowledgeBaseID scopes mastery to one wiki graph.
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_mastery_states_scope,priority:3"`
	// Slug carries the type prefix; alias reconciliation migrates stale
	// values here when a page is renamed or merged.
	Slug string `json:"slug" gorm:"type:varchar(512);not null;uniqueIndex:idx_mastery_states_scope,priority:4"`
	// Logit is the clamped running sum of event weights; p = sigmoid(logit).
	Logit float64 `json:"logit" gorm:"not null;default:0"`
	// EvidenceCount counts every folded event regardless of sign.
	EvidenceCount int `json:"evidence_count" gorm:"not null;default:0"`
	// PositiveCount/NegativeCount split evidence by weight sign; stability
	// grows only with positive evidence.
	PositiveCount  int       `json:"positive_count" gorm:"not null;default:0"`
	NegativeCount  int       `json:"negative_count" gorm:"not null;default:0"`
	Stability      float64   `json:"stability" gorm:"not null;default:0"`
	LastEvidenceAt time.Time `json:"last_evidence_at"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (MasteryState) TableName() string { return "mastery_states" }

// MemoryWikiMap is channel B: a memory topic projected onto a wiki node. The
// mapping lives at topic granularity (not per memory item) because the topic
// key is the stable identity items are normalised under, and rows below the
// confidence floor are never stored at all.
type MemoryWikiMap struct {
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_memory_wiki_map_scope,priority:1"`
	// SubjectID is Principal.StorageID().
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_memory_wiki_map_scope,priority:2"`
	// KnowledgeBaseID scopes the mapping to one wiki graph.
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_memory_wiki_map_scope,priority:3"`
	// NormalizedTopicKey is memory_topic_stats' topic key form.
	NormalizedTopicKey string `json:"normalized_topic_key" gorm:"type:varchar(255);not null;uniqueIndex:idx_memory_wiki_map_scope,priority:4"`
	// Slug carries the type prefix of the mapped wiki page.
	Slug string `json:"slug" gorm:"type:varchar(512);not null;uniqueIndex:idx_memory_wiki_map_scope,priority:5"`
	// TopicLabel keeps the human-readable topic for the profile view.
	TopicLabel string  `json:"topic_label" gorm:"type:varchar(255);not null;default:''"`
	Confidence float64 `json:"confidence" gorm:"not null;default:0"`
	// DecidedBy distinguishes LLM adjudication from manual curation.
	DecidedBy string    `json:"decided_by" gorm:"type:varchar(16);not null;default:'llm'"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (MemoryWikiMap) TableName() string { return "memory_wiki_map" }

// LearningEdge is a KB-shared prerequisite relation: "before learning `to`,
// master `from`". Only prerequisite pairs adjudicated above the confidence
// floor are stored; the row carries no per-subject state.
type LearningEdge struct {
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_learning_edges_scope,priority:1"`
	// KnowledgeBaseID scopes edges to one wiki graph.
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_learning_edges_scope,priority:2"`
	// FromSlug is the prerequisite node.
	FromSlug string `json:"from_slug" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_edges_scope,priority:3"`
	// ToSlug is the node that benefits from having FromSlug mastered.
	ToSlug string `json:"to_slug" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_edges_scope,priority:4"`
	// Relation is always LearningEdgePrerequisite today; the column keeps
	// the vocabulary open for related-pairs storage later.
	Relation   string  `json:"relation" gorm:"type:varchar(16);not null;default:'prerequisite'"`
	Confidence float64 `json:"confidence" gorm:"not null;default:0"`
	// Source records how the edge came to be, so human edits are never
	// silently overwritten by a re-run of the adjudicator.
	Source    string    `json:"source" gorm:"type:varchar(16);not null;default:'heuristic+llm'"`
	CreatedAt time.Time `json:"created_at"`
}

func (LearningEdge) TableName() string { return "learning_edges" }

// LearningQuizItem is a KB-shared, grounded single-choice question for one
// node. Grounding is enforced at generation time: ChunkRefs must be a subset
// of the page's own ChunkRefs, which is what keeps quiz evidence honest.
type LearningQuizItem struct {
	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// TenantID/KnowledgeBaseID scope the item to one wiki graph; the item is
	// shared personal-data-free inventory, so deletes never touch it.
	TenantID        uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_learning_quiz_items_scope,priority:1"`
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_quiz_items_scope,priority:2"`
	// Slug is the node the question examines.
	Slug string `json:"slug" gorm:"type:varchar(512);not null;index:idx_learning_quiz_items_scope,priority:3"`
	// Question/Options/CorrectKey/Explanation form the item payload.
	Question    string      `json:"question" gorm:"type:text;not null"`
	Options     QuizOptions `json:"options" gorm:"column:options;type:jsonb"`
	CorrectKey  string      `json:"correct_key" gorm:"type:varchar(4);not null"`
	Explanation string      `json:"explanation" gorm:"type:text"`
	// ChunkRefs cites the evidence the item was generated from.
	ChunkRefs RefList `json:"chunk_refs" gorm:"column:chunk_refs;type:jsonb"`
	// Status is active or disabled; see the LearningQuizStatus constants.
	Status    string    `json:"status" gorm:"type:varchar(16);not null;default:'active';index:idx_learning_quiz_items_scope,priority:4"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (LearningQuizItem) TableName() string { return "learning_quiz_items" }

// LearningQuizAttempt is the personal answer record. It is subject-scoped
// personal data, so it is included in profile delete/export while the
// KB-shared items are not.
type LearningQuizAttempt struct {
	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// TenantID/SubjectID/KnowledgeBaseID scope the attempt to one person in
	// one workspace in one wiki graph.
	TenantID        uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_learning_quiz_attempts_scope,priority:1;index:idx_learning_quiz_attempts_item,priority:1"`
	SubjectID       string `json:"subject_id" gorm:"type:varchar(512);not null;index:idx_learning_quiz_attempts_scope,priority:2;index:idx_learning_quiz_attempts_item,priority:2"`
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_quiz_attempts_scope,priority:3"`
	// QuizItemID points at the shared item; Slug is denormalised so delete
	// and per-node stats never need the join.
	QuizItemID string `json:"quiz_item_id" gorm:"type:varchar(36);not null;index:idx_learning_quiz_attempts_item,priority:3"`
	Slug       string `json:"slug" gorm:"type:varchar(512);not null;index:idx_learning_quiz_attempts_scope,priority:4"`
	// ChosenKey/IsCorrect freeze the deterministic grade result.
	ChosenKey  string    `json:"chosen_key" gorm:"type:varchar(4);not null"`
	IsCorrect  bool      `json:"is_correct" gorm:"not null;default:false"`
	AnsweredAt time.Time `json:"answered_at" gorm:"not null;index:idx_learning_quiz_attempts_item,priority:4"`
	CreatedAt  time.Time `json:"created_at"`
}

func (LearningQuizAttempt) TableName() string { return "learning_quiz_attempts" }

// LearningSubjectPrefs is the per-person collection opt-out. Deleting a
// profile may set it, and every collection entry point checks it first, so
// "deleted" cannot silently come back to life on the next question — the
// same resurrection guard philosophy as memory's tombstones.
type LearningSubjectPrefs struct {
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_learning_subject_prefs_scope,priority:1"`
	// SubjectID is Principal.StorageID().
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_subject_prefs_scope,priority:2"`
	// CollectDisabled is the opt-out flag.
	CollectDisabled bool      `json:"collect_disabled" gorm:"not null;default:false"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (LearningSubjectPrefs) TableName() string { return "learning_subject_prefs" }

// LearningBackfillMark records that one (subject, KB) scope has already been
// bootstrapped from doc-affinity history. It deliberately survives
// DeleteLearningDataBySubject — like the opt-out prefs, it is a processing
// tombstone, not learning data — because backfill idempotency previously
// lived in learning_events (the backfill_cite rows themselves), which profile
// deletion removes: without an independent mark the next startup backfill
// would re-derive and re-fold the deleted history, silently resurrecting the
// profile the user asked to be gone. One row per scope, written before any
// event is appended so a crash can under-light but never duplicate weights.
type LearningBackfillMark struct {
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_learning_backfill_marks_scope,priority:1"`
	// SubjectID is Principal.StorageID().
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_backfill_marks_scope,priority:2"`
	// KnowledgeBaseID scopes the bootstrap to one wiki graph.
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_learning_backfill_marks_scope,priority:3"`
	CompletedAt     time.Time `json:"completed_at"`
}

func (LearningBackfillMark) TableName() string { return "learning_backfill_marks" }

// LearningSkip is the user-declared skip list ("已掌握，不再推荐"): a
// standing, revocable statement that the subject considers themselves done
// with one node. It is deliberately NOT foldable evidence — the mastery
// math never sees it — and NOT an event: view windows would age an event
// out, while a skip must hold until revoked. Reads consume it as queue
// suppression (the recommender's exclusion set) and as a badge on the
// node's view. Personal data: profile delete and the KB orphan sweep
// remove it.
type LearningSkip struct {
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_learning_skips_scope,priority:1"`
	// SubjectID is Principal.StorageID().
	// The plain index mirrors the migration's idx_learning_skips_scope_subject
	// so the subject-scoped export read stays index-backed on AutoMigrate'd
	// databases too.
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_skips_scope,priority:2;index:idx_learning_skips_scope_subject"`
	// KnowledgeBaseID scopes the skip to one wiki graph.
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_learning_skips_scope,priority:3"`
	// Slug carries the type prefix; alias reconciliation migrates stale
	// values here when a page is renamed or merged, same as mastery rows.
	Slug      string    `json:"slug" gorm:"type:varchar(512);not null;uniqueIndex:idx_learning_skips_scope,priority:4"`
	CreatedAt time.Time `json:"created_at"`
}

func (LearningSkip) TableName() string { return "learning_skips" }
