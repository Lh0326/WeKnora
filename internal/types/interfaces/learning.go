package interfaces

import (
	"context"
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
	// AppendEvent stores one immutable event row. IDs are minted here when
	// empty so callers cannot forget.
	AppendEvent(ctx context.Context, event *types.LearningEvent) error
	// ListEvents returns the subject's events for one KB, oldest first,
	// optionally bounded below by since (zero = unbounded) and above by
	// limit (0 = a large default). Backing store for replay, reconcile and
	// the timeline.
	ListEvents(ctx context.Context, scope LearningScope, since time.Time, limit int) ([]types.LearningEvent, error)
	// GetMastery returns one folded row, or (nil, nil) when the subject has
	// never touched the node.
	GetMastery(ctx context.Context, scope LearningScope, slug string) (*types.MasteryState, error)
	// UpsertMastery inserts or updates the fold for one (subject, node),
	// keyed by the scope unique index.
	UpsertMastery(ctx context.Context, state *types.MasteryState) error
	// ListMastery returns every folded node of the subject in one KB.
	ListMastery(ctx context.Context, scope LearningScope) ([]types.MasteryState, error)
	// GetSubjectPrefs returns the collection opt-out, or (nil, nil) when
	// the subject never set one.
	GetSubjectPrefs(ctx context.Context, tenantID uint64, subjectID string) (*types.LearningSubjectPrefs, error)
	// UpsertSubjectPrefs sets the collection opt-out.
	UpsertSubjectPrefs(ctx context.Context, prefs *types.LearningSubjectPrefs) error
	// UpsertEdge inserts or updates one KB-shared prerequisite edge.
	UpsertEdge(ctx context.Context, edge *types.LearningEdge) error
	// ListEdges returns all prerequisite edges of one KB.
	ListEdges(ctx context.Context, tenantID uint64, knowledgeBaseID string) ([]types.LearningEdge, error)
	// UpsertQuizItem inserts or updates one KB-shared quiz item by id.
	UpsertQuizItem(ctx context.Context, item *types.LearningQuizItem) error
	// ListQuizItems returns the items of one node, active first filter left
	// to the caller.
	ListQuizItems(ctx context.Context, tenantID uint64, knowledgeBaseID, slug string) ([]types.LearningQuizItem, error)
	// InsertAttempt stores one personal answer record.
	InsertAttempt(ctx context.Context, attempt *types.LearningQuizAttempt) error
	// ListAttempts returns the subject's answer history for one node,
	// oldest first — the input for repeat-attempt decay.
	ListAttempts(ctx context.Context, scope LearningScope, slug string) ([]types.LearningQuizAttempt, error)

	// DeleteMastery retires one folded row. Alias reconciliation uses it
	// after the migrated state is written under the live slug, so a rename
	// never leaves two rows describing the same node.
	DeleteMastery(ctx context.Context, scope LearningScope, slug string) error
	// ListAllMastery returns every folded row across subjects and KBs. The
	// daily reconcile pass is its only caller; the table is bounded by
	// (people × nodes), which keeps a full scan honest at that cadence.
	ListAllMastery(ctx context.Context) ([]types.MasteryState, error)
	// ListBackfilledSlugs returns the node slugs that already carry a
	// backfill event for this scope — the idempotency set that makes the
	// backfill task safe to re-run.
	ListBackfilledSlugs(ctx context.Context, scope LearningScope) ([]string, error)
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
	// are the export payload members, cross-KB by design.
	ListEventsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.LearningEvent, error)
	ListMasteryBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.MasteryState, error)
	ListAttemptsBySubject(ctx context.Context, tenantID uint64, subjectID string) ([]types.LearningQuizAttempt, error)
	// DeleteLearningDataBySubject removes the subject's events, states,
	// mappings and attempts in one sweep. The KB-shared quiz bank is not
	// personal data and is never touched.
	DeleteLearningDataBySubject(ctx context.Context, tenantID uint64, subjectID string) error
	// DeleteLearningDataByKB removes EVERY subject's learning data inside
	// one knowledge base — the orphan sweep for KBs that no longer exist.
	DeleteLearningDataByKB(ctx context.Context, tenantID uint64, knowledgeBaseID string) error
}

// LearningService is the write-path entry the QA handler calls after each
// completed answer, plus the idempotent history replay the reconcile
// runner triggers on startup. Failures are logged inside and never
// surface to the answer path.
type LearningService interface {
	// RecordAnswerTouches folds the knowledge nodes the answer's citations
	// touched into the caller's mastery. No-op unless LEARNING_ENABLE=true
	// and the subject has not opted out of collection.
	RecordAnswerTouches(ctx context.Context, assistantMessage *types.Message)
	// RecordWikiRead folds one deliberate wiki page open into the caller's
	// mastery (the §3.3.6 low-trust read signal). Deduped per slug per
	// re-ask window; rejects non-node slugs with ErrWikiReadTarget.
	RecordWikiRead(ctx context.Context, kbID, slug string) error
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
	// TakeQuiz serves a node's active questions without answer material.
	TakeQuiz(ctx context.Context, kbID, slug string) ([]QuizQuestion, error)
	// SubmitAnswer grades deterministically and folds the result (opted-out
	// subjects still get the verdict; nothing is stored).
	SubmitAnswer(ctx context.Context, kbID, itemID, chosenKey string) (*AnswerResult, error)
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
}

// LearningProgress is the tab header: node totals per tier and per folder.
type LearningProgress struct {
	TotalNodes int                    `json:"total_nodes"`
	LitNodes   int                    `json:"lit_nodes"`
	Levels     map[string]int         `json:"levels"`
	Units      []LearningUnitProgress `json:"units"`
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
	// Title is the node page's human-readable title, resolved in one batch
	// at read time (empty when the page no longer resolves).
	Title string `json:"title,omitempty"`
}

// QuizQuestion is the served question shape (no answer material).
type QuizQuestion struct {
	ID        string            `json:"id"`
	Question  string            `json:"question"`
	Options   map[string]string `json:"options"`
	ChunkRefs []string          `json:"chunk_refs"`
	// SourceDocs resolves the cited chunks back to their source documents
	// (id + title + how many of the item's chunks live there), so the
	// client can link "this question came from that document". Derived
	// deterministically at serve time; empty when the evidence cannot be
	// resolved (e.g. chunks deleted since generation).
	SourceDocs []QuizSourceDoc `json:"source_docs,omitempty"`
}

// QuizSourceDoc is one source document behind a quiz item.
type QuizSourceDoc struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title,omitempty"`
	ChunkCount  int    `json:"chunk_count"`
}

// AnswerResult is the response after a submission.
type AnswerResult struct {
	Correct     bool     `json:"correct"`
	CorrectKey  string   `json:"correct_key"`
	Explanation string   `json:"explanation"`
	ChunkRefs   []string `json:"chunk_refs"`
	// NextReviewDays is the deterministic review schedule: how many days
	// the freshly folded state stays above its tier's demotion threshold
	// ("下次复习约在 N 天后"). Nil when there is nothing to retain yet or
	// the state already sits below the threshold (review now).
	NextReviewDays *float64 `json:"next_review_days,omitempty"`
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
	ExportedAt time.Time `json:"exported_at"`
	// KBSummary groups the payload by knowledge base at the top so the
	// exported file reads as "what I did where" at a glance; KBs that no
	// longer exist (deleted after the data was collected) are marked with
	// Exists=false so their group is self-explanatory.
	KBSummary []ExportKBSummary           `json:"kb_summary"`
	Events    []types.LearningEvent       `json:"events"`
	Mastery   []types.MasteryState        `json:"mastery"`
	TopicMaps []types.MemoryWikiMap       `json:"topic_maps"`
	Attempts  []types.LearningQuizAttempt `json:"quiz_attempts"`
}

// ExportKBSummary is one knowledge base's roll-up inside the export.
type ExportKBSummary struct {
	KbID         string `json:"kb_id"`
	KbName       string `json:"kb_name,omitempty"`
	Exists       bool   `json:"exists"`
	Events       int    `json:"events"`
	MasteryNodes int    `json:"mastery_nodes"`
	Attempts     int    `json:"attempts"`
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
	PEff           float64 `json:"p_eff,omitempty"`
	EvidenceCount  int     `json:"evidence_count,omitempty"`
	PositiveCount  int     `json:"positive_count,omitempty"`
	NegativeCount  int     `json:"negative_count,omitempty"`
	Faded          bool    `json:"faded,omitempty"`
}
