package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// This file locks in the reduce-phase error contracts: (1) an LLM failure
// while writing a wiki page must surface as a non-nil error from
// reduceSlugUpdates — the salvage pass keys on it; (2) the salvage pass must
// retry failed writes after the cooldown and report what is still failing.
// The pre-fix code reset the error to nil, which silently dropped the page,
// finalized the document, and reported success.

// rateLimitErr carries production 429 wording minus isTransientLLMError's
// needles ("status 429"), so generateWithTemplate fails on the first attempt
// (fast test) while isLikelyRateLimitError still classifies it.
func rateLimitErr() error {
	return errors.New("create chat completion: error, rate limit exceeded, please retry later")
}

type failingChatModel struct{ err error }

func (m *failingChatModel) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, m.err
}
func (m *failingChatModel) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}
func (m *failingChatModel) GetModelName() string { return "failing" }
func (m *failingChatModel) GetModelID() string   { return "failing" }

// flakyChatModel fails the first failFirst calls, then succeeds.
type flakyChatModel struct {
	mu        sync.Mutex
	calls     int
	failFirst int
}

func (m *flakyChatModel) Chat(ctx context.Context, msgs []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	if n <= m.failFirst {
		return nil, rateLimitErr()
	}
	return &types.ChatResponse{Content: "SUMMARY: page\n# Alpha"}, nil
}
func (m *flakyChatModel) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}
func (m *flakyChatModel) GetModelName() string { return "flaky" }
func (m *flakyChatModel) GetModelID() string   { return "flaky" }

// liveKnowledgeSvc keeps every knowledge "alive" so filterLiveUpdates
// passes the updates through to reduce.
type liveKnowledgeSvc struct{ interfaces.KnowledgeService }

func (liveKnowledgeSvc) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "kid-live"}, nil
}

// pageStoreGetBySlug serves one fixed page (or none when nil); everything
// else panics via the embedded nil interface — the test fails loudly if the
// code under test strays into un-stubbed territory.
type pageStoreGetBySlug struct {
	interfaces.WikiPageService
	page *types.WikiPage
}

func (s pageStoreGetBySlug) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	if s.page == nil {
		return nil, nil // not found → reduce synthesizes a new page
	}
	return s.page, nil
}

func (s pageStoreGetBySlug) CreatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	return page, nil
}

func (s pageStoreGetBySlug) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	return page, nil
}

func propagateBatchCtx() *WikiBatchContext {
	return &WikiBatchContext{
		SlugTitleMany:               func(context.Context, []string) map[string]string { return nil },
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
	}
}

func additionUpdate() SlugUpdate {
	return SlugUpdate{
		Slug: "concept/prompt", Type: types.WikiPageTypeConcept,
		Item:     extractedItem{Name: "提示词", Description: "给模型的指令"},
		DocTitle: "学习文档", KnowledgeID: "kid-live",
		SourceRef: "kid-live|学习文档", Language: "Chinese (Simplified)",
	}
}

func TestReduceSlugUpdatesPropagatesLLMFailure(t *testing.T) {
	svc := &wikiIngestService{
		knowledgeSvc: liveKnowledgeSvc{},
		wikiService:  pageStoreGetBySlug{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	changed, _, additionFailed, err := svc.reduceSlugUpdates(
		ctx, &failingChatModel{err: rateLimitErr()}, "kb-1", "concept/prompt",
		[]SlugUpdate{additionUpdate()},
		7, propagateBatchCtx(), nil,
	)

	if err == nil {
		t.Fatal("LLM failure must propagate as a non-nil error (the salvage pass keys on it)")
	}
	if !isLikelyRateLimitError(err) {
		t.Fatalf("propagated error must stay rate-limit classifiable, got: %v", err)
	}
	if !additionFailed {
		t.Fatal("additions pending when the page write failed → additionFailed must be true")
	}
	if changed {
		t.Fatal("failed write must not report the page as changed")
	}
}

func TestReduceSlugUpdatesPropagatesRetractOnlyFailure(t *testing.T) {
	svc := &wikiIngestService{
		knowledgeSvc: liveKnowledgeSvc{},
		wikiService: pageStoreGetBySlug{page: &types.WikiPage{
			Slug: "concept/prompt", Title: "提示词", PageType: types.WikiPageTypeConcept,
			SourceRefs: types.StringArray{"kid-live|学习文档"},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	_, _, additionFailed, err := svc.reduceSlugUpdates(
		ctx, &failingChatModel{err: rateLimitErr()}, "kb-1", "concept/prompt",
		[]SlugUpdate{{
			Slug: "concept/prompt", Type: "retract",
			DocTitle: "学习文档", KnowledgeID: "kid-live",
			SourceRef: "kid-live|学习文档", RetractDocContent: "旧内容",
		}},
		7, propagateBatchCtx(), nil,
	)

	if err == nil {
		t.Fatal("retract-only LLM failure must also propagate for the salvage pass")
	}
	if additionFailed {
		t.Fatal("retract-only failure must not set additionFailed (no page addition was pending)")
	}
}

func TestSalvageFailedReducesRecoversAfterCooldown(t *testing.T) {
	prev := wikiSalvageCooldown
	wikiSalvageCooldown = 5 * time.Millisecond
	defer func() { wikiSalvageCooldown = prev }()

	svc := &wikiIngestService{
		knowledgeSvc: liveKnowledgeSvc{},
		wikiService:  pageStoreGetBySlug{}, // not found → new-page path
	}
	// The first-round failure happened before salvageFailedReduces was
	// called; here the retry meets a healthy provider (failFirst=0).
	model := &flakyChatModel{failFirst: 0}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	var recovered []string
	still := svc.salvageFailedReduces(
		ctx, model, "kb-1", 7, propagateBatchCtx(), nil,
		[]salvageItem{{slug: "concept/prompt", updates: []SlugUpdate{additionUpdate()}}},
		func(slug, affectedType string) { recovered = append(recovered, slug+"|"+affectedType) },
	)

	if len(still) != 0 {
		t.Fatalf("salvage must recover a transient failure, still failed: %v", still)
	}
	if len(recovered) != 1 || recovered[0] != "concept/prompt|ingest" {
		t.Fatalf("onSuccess must fire once with the slug and affectedType, got: %v", recovered)
	}
	if model.calls < 1 {
		t.Fatalf("salvage must retry the failed write, chat calls = %d", model.calls)
	}
}

func TestSalvageFailedReducesReportsPersistentFailures(t *testing.T) {
	prev := wikiSalvageCooldown
	wikiSalvageCooldown = 5 * time.Millisecond
	defer func() { wikiSalvageCooldown = prev }()

	svc := &wikiIngestService{
		knowledgeSvc: liveKnowledgeSvc{},
		wikiService:  pageStoreGetBySlug{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	still := svc.salvageFailedReduces(
		ctx, &failingChatModel{err: rateLimitErr()}, "kb-1", 7, propagateBatchCtx(), nil,
		[]salvageItem{{slug: "concept/prompt", updates: []SlugUpdate{additionUpdate()}}},
		nil,
	)

	if len(still) != 1 || still[0] != "concept/prompt" {
		t.Fatalf("persistently failing slug must be reported as still failed, got: %v", still)
	}
}
