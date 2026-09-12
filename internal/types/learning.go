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
	// Original-document exposure, scoped to a versioned source occurrence.
	// It counts as human activity but never as concept mastery evidence.
	LearningEventSourceRead = "source_read"
	LearningEventNodeRead   = "node_read"
	LearningEventNodeKnown  = "node_known"
	LearningEventNodeReview = "node_review"
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

	// LearningEventWikiToolRead: the person deliberately opened the node's
	// wiki page in the browser — a human read signal. (The type name is
	// historical: it predates the channel split from agent reads.)
	LearningEventWikiToolRead = "wiki_tool_read"
	// LearningEventAgentRead: an agent wiki_read_page call made while
	// answering for the person. Zero-weight by contract — a tool access is
	// activity trace ("the assistant consulted this page on your behalf"),
	// never the person's own learning: it must not fold mastery, must not
	// refresh the retention anchor, and must not consume the human read's
	// 48-hour scoring window.
	LearningEventAgentRead = "agent_read"
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
// serving. Stale is the automatic evidence-invalidated state: the page or
// its cited chunks changed since generation, so the question may no longer
// be answerable from the current material — serving and scoring stop until
// the next maintenance pass re-adjudicates against the new evidence.
const (
	LearningQuizStatusActive   = "active"
	LearningQuizStatusDisabled = "disabled"
	LearningQuizStatusStale    = "stale"
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

// JSONColumn is a raw JSON payload column (form definitions, check lists,
// answer maps) kept verbatim; the service layer unmarshals what it needs.
type JSONColumn []byte

func (j JSONColumn) Value() (driver.Value, error) {
	if j == nil {
		return json.Marshal([]byte{})
	}
	return string(j), nil
}

func (j *JSONColumn) Scan(src interface{}) error {
	switch v := src.(type) {
	case []byte:
		*j = append((*j)[0:0], v...)
	case string:
		*j = JSONColumn(v)
	}
	return nil
}

func (j JSONColumn) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

func (j *JSONColumn) UnmarshalJSON(data []byte) error {
	*j = append((*j)[0:0], data...)
	return nil
}

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
	ReviewData     JSONColumn `json:"review_data,omitempty" gorm:"type:jsonb"`
	ContentVersion string     `json:"content_version,omitempty" gorm:"type:varchar(64);not null;default:''"`
	// OriginalSlug preserves the first node name when canonical identity is migrated.
	OriginalSlug string `json:"original_slug,omitempty" gorm:"type:varchar(512);not null;default:''"`
	ID           string `json:"id" gorm:"primaryKey;type:varchar(36)"`
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
	ProjectionVersion string `json:"projection_version" gorm:"type:varchar(64);not null;default:''"`
	ReplayHash        string `json:"replay_hash,omitempty" gorm:"type:varchar(64);not null;default:''"`
	TenantID          uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_states_scope,priority:1"`
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
	// Frozen verification metadata; empty legacy values cannot certify new objectives.
	ObjectiveVersion string `json:"objective_version,omitempty" gorm:"type:varchar(128);not null;default:''"`

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
	// EvidenceHash freezes the evidence version: a digest of the page
	// content and cited chunk contents at generation time. When the
	// maintenance pass recomputes a different digest, the item goes stale —
	// serving and scoring stop until questions are regenerated from the
	// new material. Empty on legacy rows means "version unknown" and is
	// treated as stale on the first pass that can compute a digest.
	EvidenceHash string `json:"evidence_hash" gorm:"type:varchar(64);not null;default:''"`
	// ObjectiveID links the item to the observable learning objective it
	// verifies (stage-1 evidence separation). Empty on legacy items.
	ObjectiveID string `json:"objective_id" gorm:"type:varchar(36);not null;default:''"`
	// FamilyID is the item-family identity: items sharing an answer
	// memory, reasoning pattern or core scenario. Independence of evidence
	// is judged per FAMILY, not per item id — a reworded clone carries the
	// same family and never counts as new independent coverage. Empty on
	// legacy items (their attempts degrade to legacy_unverified).
	FamilyID string `json:"family_id" gorm:"type:varchar(64);not null;default:''"`
	// ContentVersion freezes the item content's version lineage; a
	// semantic change bumps it, a typographical fix may keep it (the
	// reviewer decides and records ChangeKind).
	ContentVersion string `json:"content_version" gorm:"type:varchar(64);not null;default:''"`
	// RubricVersion/ScorerVersion freeze the grading rule identity at
	// publish time; GradeQuiz is scorer "mcq-exact-v1".
	RubricVersion string `json:"rubric_version" gorm:"type:varchar(64);not null;default:''"`
	ScorerVersion string `json:"scorer_version" gorm:"type:varchar(64);not null;default:''"`
	// AssistanceMode is the condition the item is valid under:
	// closed_book (default for concept MCQs), open_book (doc-assisted
	// application). Attempts under a different mode never mix into the
	// same strict evidence.
	AssistanceMode string `json:"assistance_mode" gorm:"type:varchar(16);not null;default:'closed_book'"`
	// FamilyFingerprint is the deterministic clone detector: a digest of
	// the objective plus the NORMALIZED correct-answer text. A reworded
	// stem or reshuffled options keep the answer text and therefore the
	// fingerprint — clones inherit the existing family instead of
	// founding a new one.
	FamilyFingerprint string `json:"family_fingerprint" gorm:"type:varchar(64);not null;default:''"`
	// Reviewer/PublishedAt/ReviewNote/ChangeKind are the human review
	// audit record. LLM drafts carry empty Reviewer; ONLY the review API
	// (an authenticated human principal) stamps them. ChangeKind records
	// typographic vs semantic on re-review.
	Reviewer    string     `json:"reviewer" gorm:"type:varchar(512);not null;default:''"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	ReviewNote  string     `json:"review_note" gorm:"type:text"`
	ChangeKind  string     `json:"change_kind" gorm:"type:varchar(16);not null;default:''"`
	// Status: draft (LLM draft, practice only), active (legacy serving,
	// strict-equivalent only when it also carries family+review),
	// published (human-reviewed, strict verification), disabled, stale.
	Status    string    `json:"status" gorm:"type:varchar(16);not null;default:'active';index:idx_learning_quiz_items_scope,priority:4"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (LearningQuizItem) TableName() string { return "learning_quiz_items" }

// Item/task lifecycle statuses (stage 2). draft = LLM-authored practice
// material that can never promote the strict profile; published = passed
// human content review; active is the pre-review-era serving status kept
// for compatibility — its attempts only count strictly when the row also
// carries family metadata and a review record.
const (
	LearningQuizStatusDraft     = "draft"
	LearningQuizStatusPublished = "published"
)

// Assistance modes. closed_book and open_book are DESIGNED conditions
// (which one applies is part of the item/task definition); assistant_helped
// is a trial-level declaration that never counts as strict evidence.
const (
	AssistanceClosedBook    = "closed_book"
	AssistanceOpenBook      = "open_book"
	AssistanceAssistantHelp = "assistant_helped"
)

// GradeQuiz's scorer identity, frozen on attempts for traceability.
const MCQScorerVersion = "mcq-exact-v1"

// Task scorer identity: every critical check is an exact field match
// against the frozen answer key — no free-text grading, no keyword
// matching, no vector similarity anywhere in this chain.
const TaskScorerVersion = "task-checks-exact-v1"

// LearningQuizAttempt is the personal answer record. It is subject-scoped
// personal data, so it is included in profile delete/export while the
// KB-shared items are not.
type LearningQuizAttempt struct {
	// Frozen verification metadata; empty legacy values cannot certify new objectives.
	ContractVersion    string `json:"contract_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	RubricVersion      string `json:"rubric_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	ScorerVersion      string `json:"scorer_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	ItemContentVersion string `json:"item_content_version,omitempty" gorm:"type:varchar(128);not null;default:''"`

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
	// OriginalSlug preserves historical identity across canonical node renames.
	OriginalSlug string `json:"original_slug,omitempty" gorm:"type:varchar(512);not null;default:''"`
	// ObjectiveID/FamilyID freeze the item's objective and family linkage
	// AT ANSWER TIME (server-side, from the item row): objective evidence
	// is derived from these frozen fields, so later item edits never
	// rewrite what a historical attempt verified. Empty values mark
	// legacy attempts taken before the linkage existed — they surface as
	// legacy_unverified and never silently promote.
	ObjectiveID string `json:"objective_id,omitempty" gorm:"type:varchar(36);not null;default:''"`
	FamilyID    string `json:"family_id,omitempty" gorm:"type:varchar(64);not null;default:''"`
	// ContentVersion freezes the objective's content version at answer
	// time; a later semantic change makes the passing evidence stale.
	ContentVersion string `json:"content_version,omitempty" gorm:"type:varchar(64);not null;default:''"`
	// AssistanceMode freezes the trial condition (the item's designed
	// mode, or assistant_helped when the user declared it). Modes never
	// mix into one strict evidence stream.
	AssistanceMode string `json:"assistance_mode,omitempty" gorm:"type:varchar(16);not null;default:''"`
	// ItemStatus freezes the item's lifecycle status at answer time:
	// strict evidence requires published (or reviewed active) — draft
	// practice attempts stay visible but never promote.
	ItemStatus string `json:"item_status,omitempty" gorm:"type:varchar(16);not null;default:''"`
	// Eligible + GradeReason freeze the server's verdict on whether this
	// attempt can ever count as strict evidence, and why (practice_draft,
	// practice_legacy, feedback_retry, assistance_not_strict,
	// eligible_independent). Client input cannot set these.
	Eligible    bool   `json:"eligible" gorm:"not null;default:false"`
	GradeReason string `json:"grade_reason,omitempty" gorm:"type:varchar(32);not null;default:''"`
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

// LearningSubjectEpoch is the deletion generation fence: one row per
// subject, bumped inside the same transaction that deletes the profile.
// Background write paths capture the epoch before their long-running work
// (typically a model call) and re-check it inside the write transaction —
// a stale epoch means the profile was deleted mid-flight and the write is
// discarded, so an in-flight result cannot resurrect deleted data even
// when the opt-out switch alone still reads "collecting" (e.g. the person
// re-enabled collection right after deleting, or the check raced another
// server's delete). Subject-scoped, not tenant-scoped, for the same reason
// as the prefs row: shared-KB rows land under the KB owner's tenant while
// deletion is a property of the person.
type LearningSubjectEpoch struct {
	// SubjectID is Principal.StorageID().
	SubjectID string `json:"subject_id" gorm:"primaryKey;type:varchar(512)"`
	// Epoch counts profile deletions; 0 (row absent) is the virgin state.
	Epoch     int64     `json:"epoch" gorm:"not null;default:0"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (LearningSubjectEpoch) TableName() string { return "learning_subject_epochs" }

// ---- Stage 1: observable objectives and verification contracts ----

// Objective lifecycle: draft while being authored, published when its
// content is approved (stage 2 builds the review chain; stage 1 persists
// the vocabulary), retired when it no longer applies. Only published
// objectives participate in strict verification derivation.
const (
	LearningObjectiveStatusDraft     = "draft"
	LearningObjectiveStatusPublished = "published"
	LearningObjectiveStatusRetired   = "retired"
)

// Verification contract vocabulary (01 §5.3). The contract is a frozen,
// versioned rule — code deterministic, no runtime model judgement.
const (
	// ObjectiveContractConceptTwoFamily: a concept objective is verified
	// when at least two DIFFERENT eligible item families carry an
	// independent passing attempt. One family pass reads partial.
	ObjectiveContractConceptTwoFamily = "concept_two_family"
	// ObjectiveContractTaskChecks: an operational objective is verified
	// when one pre-decomposed structured task passes ALL of its named
	// critical checks. Check scoring is defined per task before publish
	// (stage 2); stage 1 persists the schema and honestly derives
	// unverified until task results exist.
	ObjectiveContractTaskChecks = "task_checks"
	// ObjectiveContractVersion freezes the contract semantics this
	// projection interprets; a change in interpretation bumps it.
	ObjectiveContractVersion = "objective-contract-v1"
)

// Objective evidence states (01 §5.4). States describe EVIDENCE, never a
// psychological mastery percentage; unknown stays unknown (no sigmoid(0)).
const (
	ObjectiveStateUnverified  = "unverified"
	ObjectiveStatePartial     = "partial"
	ObjectiveStateVerified    = "verified"
	ObjectiveStateConflicting = "conflicting"
	ObjectiveStateStale       = "stale_content"
)

// Evidence source markers for the objective projection. Legacy attempts
// (pre-objective rows without family metadata) are visible and exportable
// but never silently promote to strict verification.
const (
	ObjectiveSourceQuiz   = "quiz"
	ObjectiveSourceLegacy = "legacy_unverified"
)

// ObjectiveProjectionVersion identifies the derivation rule set; replays
// record it alongside the result so "replayed under old rules" and
// "re-projected under new rules" stay distinguishable.
const ObjectiveProjectionVersion = "objective-projection-v2"

// LearningObjective is one observable learning goal under a wiki node:
// "can do X under condition Y", the unit strict verification reasons about.
// Nodes keep their page identity; objectives add the measurable layer.
type LearningObjective struct {
	// Frozen verification metadata; empty legacy values cannot certify new objectives.
	EvidenceHash string `json:"evidence_hash,omitempty" gorm:"type:varchar(128);not null;default:''"`

	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// TenantID/KnowledgeBaseID scope the objective to one wiki graph. The
	// definition is KB-shared personal-data-free inventory.
	TenantID        uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_learning_objectives_scope,priority:1"`
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_objectives_scope,priority:2"`
	// Slug is the wiki node the objective belongs to.
	Slug string `json:"slug" gorm:"type:varchar(512);not null;index:idx_learning_objectives_scope,priority:3"`
	// Title is the short human label; Behavior states the observable
	// behavior ("can choose a retrieval strategy under given constraints").
	Title    string `json:"title" gorm:"type:varchar(255);not null"`
	Behavior string `json:"behavior" gorm:"type:text;not null"`
	// CapabilityType classifies the ability (concept discrimination,
	// operational application, diagnostic analysis) — it decides the
	// sensible assistance mode and contract shape.
	CapabilityType string `json:"capability_type" gorm:"type:varchar(32);not null;default:'concept'"`
	// ContractType selects the frozen verification contract; Params
	// carries its parameters (e.g. required family count, named critical
	// checks) as JSON.
	ContractType    string  `json:"contract_type" gorm:"type:varchar(32);not null;default:'concept_two_family'"`
	ContractParams  RefList `json:"contract_params" gorm:"column:contract_params;type:jsonb"`
	ContractVersion string  `json:"contract_version" gorm:"type:varchar(64);not null;default:''"`
	// SourceRefs anchors the objective to source material (page anchors,
	// chunk ids) so content review can trace it.
	SourceRefs RefList `json:"source_refs" gorm:"column:source_refs;type:jsonb"`
	// ContentVersion freezes the objective's DEFINITION version. A pass
	// recorded under a different version is preserved as history but no
	// longer satisfies the current contract without re-verification
	// (stale_content), and typographical re-publishes may keep the version.
	ContentVersion string `json:"content_version" gorm:"type:varchar(64);not null;default:''"`
	// Review audit (stage 2): reviewer identity, publish time, note and
	// change kind — same discipline as quiz items and tasks.
	Reviewer    string     `json:"reviewer" gorm:"type:varchar(512);not null;default:''"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	ReviewNote  string     `json:"review_note" gorm:"type:text"`
	ChangeKind  string     `json:"change_kind" gorm:"type:varchar(16);not null;default:''"`
	// Status is draft/published/retired; see the constants.
	Status string `json:"status" gorm:"type:varchar(16);not null;default:'draft'"`
	// PrereqObjectiveID optionally names another objective that should be
	// verified first (guidance-layer use only; never blocks reading).
	PrereqObjectiveID string    `json:"prereq_objective_id" gorm:"type:varchar(36);not null;default:''"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (LearningObjective) TableName() string { return "learning_objectives" }

// LearningTask is one structured application task (01 §5.3 contract B):
// a scenario, a set of form fields the learner fills, and a frozen
// answer key of critical checks — every check is an EXACT field match,
// graded server-side. The answer key never leaves the server: the take
// payload carries scenario + field definitions only. Free-text answers
// are not graded by keywords or similarity — tasks are selections by
// construction.
type LearningTask struct {
	// Frozen verification metadata; empty legacy values cannot certify new objectives.
	ObjectiveVersion string `json:"objective_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	EvidenceHash     string `json:"evidence_hash,omitempty" gorm:"type:varchar(128);not null;default:''"`

	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// TenantID/KnowledgeBaseID scope the task; KB-shared inventory.
	TenantID        uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_learning_tasks_scope,priority:1"`
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_tasks_scope,priority:2"`
	// Slug is the node the task applies to; ObjectiveID the objective it
	// verifies (contract task_checks).
	Slug string `json:"slug" gorm:"type:varchar(512);not null;index:idx_learning_tasks_scope,priority:3"`
	// FamilyID groups task variants; one passed task satisfies contract B.
	ObjectiveID string `json:"objective_id" gorm:"type:varchar(36);not null;default:''"`
	FamilyID    string `json:"family_id" gorm:"type:varchar(64);not null;default:''"`
	// Title/Scenario describe the applied situation.
	Title    string `json:"title" gorm:"type:varchar(255);not null"`
	Scenario string `json:"scenario" gorm:"type:text;not null"`
	// Fields is the form definition: [{id,label,type:"select",options:[…]}].
	Fields JSONColumn `json:"fields" gorm:"column:fields;type:jsonb"`
	// AnswerKey maps field id → correct value (server-only).
	AnswerKey map[string]string `json:"-" gorm:"column:answer_key;type:jsonb;serializer:json"`
	// CriticalChecks names the fields that must ALL match: [{id,description}].
	CriticalChecks JSONColumn `json:"critical_checks" gorm:"column:critical_checks;type:jsonb"`
	// SourceRefs anchors the task to source material.
	SourceRefs JSONColumn `json:"source_refs" gorm:"column:source_refs;type:jsonb"`
	// Versioning + review audit, same semantics as quiz items. Tasks are
	// open_book by design (doc-assisted application).
	ContentVersion string     `json:"content_version" gorm:"type:varchar(64);not null;default:''"`
	RubricVersion  string     `json:"rubric_version" gorm:"type:varchar(64);not null;default:''"`
	ScorerVersion  string     `json:"scorer_version" gorm:"type:varchar(64);not null;default:''"`
	AssistanceMode string     `json:"assistance_mode" gorm:"type:varchar(16);not null;default:'open_book'"`
	Reviewer       string     `json:"reviewer" gorm:"type:varchar(512);not null;default:''"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	ReviewNote     string     `json:"review_note" gorm:"type:text"`
	ChangeKind     string     `json:"change_kind" gorm:"type:varchar(16);not null;default:''"`
	Status         string     `json:"status" gorm:"type:varchar(16);not null;default:'draft';index:idx_learning_tasks_scope,priority:4"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (LearningTask) TableName() string { return "learning_tasks" }

// LearningTaskAttempt is one graded task submission: the learner's
// answers, the per-check verdicts, and the frozen trial conditions.
// Personal data — profile delete/export include it.
type LearningTaskAttempt struct {
	// Frozen verification metadata; empty legacy values cannot certify new objectives.
	ObjectiveVersion string `json:"objective_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	ContractVersion  string `json:"contract_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	RubricVersion    string `json:"rubric_version,omitempty" gorm:"type:varchar(128);not null;default:''"`
	ScorerVersion    string `json:"scorer_version,omitempty" gorm:"type:varchar(128);not null;default:''"`

	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// TenantID/SubjectID/KnowledgeBaseID scope the attempt to one person.
	TenantID        uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_learning_task_attempts_scope,priority:1"`
	SubjectID       string `json:"subject_id" gorm:"type:varchar(512);not null;index:idx_learning_task_attempts_scope,priority:2"`
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_learning_task_attempts_scope,priority:3"`
	// TaskID points at the shared task; ObjectiveID/FamilyID/Slug are
	// frozen at submit time so later task edits never move history.
	TaskID      string `json:"task_id" gorm:"type:varchar(36);not null"`
	ObjectiveID string `json:"objective_id" gorm:"type:varchar(36);not null;default:''"`
	FamilyID    string `json:"family_id" gorm:"type:varchar(64);not null;default:''"`
	Slug        string `json:"slug" gorm:"type:varchar(512);not null"`
	// Answers: the learner's field values; Checks: per-check pass/fail;
	// IsPassed = every critical check passed (deterministic exact match).
	Answers  map[string]string `json:"answers" gorm:"column:answers;type:jsonb;serializer:json"`
	Checks   JSONColumn        `json:"checks" gorm:"column:checks;type:jsonb"`
	IsPassed bool              `json:"is_passed" gorm:"not null;default:false"`
	// Frozen trial conditions and server verdict, same semantics as quiz
	// attempts (assistance mode, task status at submit, eligibility and
	// its reason).
	AssistanceMode string    `json:"assistance_mode" gorm:"type:varchar(16);not null;default:''"`
	TaskStatus     string    `json:"task_status" gorm:"type:varchar(16);not null;default:''"`
	ContentVersion string    `json:"content_version" gorm:"type:varchar(64);not null;default:''"`
	Eligible       bool      `json:"eligible" gorm:"not null;default:false"`
	GradeReason    string    `json:"grade_reason" gorm:"type:varchar(32);not null;default:''"`
	SubmittedAt    time.Time `json:"submitted_at" gorm:"not null"`
	CreatedAt      time.Time `json:"created_at"`
}

func (LearningTaskAttempt) TableName() string { return "learning_task_attempts" }

// HumanLearningEventTypes is the explicit activity vocabulary. Background
// projections and agent traces never count as human recency, streak or targets.
func HumanLearningEventTypes() []string {
	return []string{LearningEventComponent, LearningEventReviewAgain, LearningEventReviewHard, LearningEventReviewGood, LearningEventReviewEasy, LearningEventNodeRead, LearningEventNodeKnown, LearningEventNodeReview, LearningEventAnswerCite, LearningEventCrossRef, LearningEventReAsk,
		LearningEventSourceRead, LearningEventWikiToolRead, LearningEventWikiDeepRead, LearningEventQuizCorrect,
		LearningEventQuizWrong, LearningEventQuizUnsure, LearningEventSelfAssessUp,
		LearningEventSelfAssessDownAll, LearningEventSelfAssessDownDocGap,
		LearningEventSelfAssessDownDocUpdated, LearningEventSelfAssessDownQuizEasy}
}
func IsHumanLearningEvent(kind string) bool {
	for _, v := range HumanLearningEventTypes() {
		if kind == v {
			return true
		}
	}
	return false
}
