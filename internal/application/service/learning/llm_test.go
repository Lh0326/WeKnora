package learning

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeChatModel implements chat.Chat with a scripted response sequence and
// a call counter — the same shape as memory's stubChatModel, minus the
// parts the learning paths never touch.
type fakeChatModel struct {
	calls     atomic.Int32
	responses []*types.ChatResponse
	errs      []error
	optsSeen  []chat.ChatOptions
	msgsSeen  []chat.Message
}

func (f *fakeChatModel) Chat(_ context.Context, msgs []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	i := int(f.calls.Add(1)) - 1
	f.optsSeen = append(f.optsSeen, *opts)
	f.msgsSeen = append(f.msgsSeen, msgs...)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.responses) {
		return f.responses[i], nil
	}
	return &types.ChatResponse{Content: "{}"}, nil
}
func (f *fakeChatModel) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse)
	close(ch)
	return ch, nil
}
func (f *fakeChatModel) GetModelName() string { return "fake" }
func (f *fakeChatModel) GetModelID() string   { return "fake" }

// lastUserUnder returns the newest user message that was sent with the given
// system message (calls are always a [system, user] pair), so tests can
// assert on the exact adjudication input.
func (f *fakeChatModel) lastUserUnder(system string) (string, bool) {
	for i := len(f.msgsSeen) - 2; i >= 0; i -= 2 {
		if f.msgsSeen[i].Role == "system" && f.msgsSeen[i].Content == system {
			return f.msgsSeen[i+1].Content, true
		}
	}
	return "", false
}

// stubModelService hands out the scripted chat model.
type stubModelService struct {
	interfaces.ModelService
	model chat.Chat
	err   error
	// tenantOK records that GetChatModel saw a tenant in ctx — the
	// regression probe for the background maintenance passes, which run
	// from an empty context and must inject the KB's tenant before any
	// model call (GetChatModel panics without one).
	tenantOK bool
}

func (s *stubModelService) GetChatModel(ctx context.Context, _ string) (chat.Chat, error) {
	if _, ok := types.TenantIDFromContext(ctx); ok {
		s.tenantOK = true
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.model, nil
}

func llmService(fake *fakeChatModel) *Service {
	return NewService(newStubRepo(), &stubWikiRepo{pages: map[string][]*types.WikiPage{}},
		nil, &stubModelService{model: fake}, nil, nil)
}

var llmSchema = json.RawMessage(`{"type":"object"}`)

func TestCallLearningJSONParsesPlainAndFenced(t *testing.T) {
	for _, content := range []string{
		`{"maps":{}}`,
		"```json\n{\"maps\":{\"a\":\"b\"}}\n```",
		"Sure, here is the JSON: {\"maps\":{}} hope it helps",
	} {
		fake := &fakeChatModel{responses: []*types.ChatResponse{{Content: content, FinishReason: "stop"}}}
		svc := llmService(fake)
		var out struct{ Maps map[string]string }
		if err := svc.callLearningJSON(t.Context(), "m", "sys", "user", llmSchema, 64, 128, &out); err != nil {
			t.Errorf("content %q: %v", content, err)
		}
	}
}

func TestCallLearningJSONRetriesTruncationOnceWithLargerBudget(t *testing.T) {
	fake := &fakeChatModel{responses: []*types.ChatResponse{
		{Content: "", FinishReason: "length"},          // truncated
		{Content: `{"maps":{}}`, FinishReason: "stop"}, // retry succeeds
	}}
	svc := llmService(fake)
	var out struct{ Maps map[string]string }
	if err := svc.callLearningJSON(t.Context(), "m", "sys", "user", llmSchema, 64, 128, &out); err != nil {
		t.Fatalf("retry path failed: %v", err)
	}
	if fake.calls.Load() != 2 {
		t.Fatalf("calls = %d, want exactly one retry", fake.calls.Load())
	}
	if fake.optsSeen[1].MaxCompletionTokens != 128 {
		t.Fatalf("retry budget = %d, want the larger one", fake.optsSeen[1].MaxCompletionTokens)
	}
	// The calling convention: temperature zero, thinking off, schema passed.
	if fake.optsSeen[0].Temperature != 0 || fake.optsSeen[0].Thinking == nil || *fake.optsSeen[0].Thinking {
		t.Fatal("convention broken: temperature/thinking")
	}
	if string(fake.optsSeen[0].Format) != string(llmSchema) {
		t.Fatal("convention broken: schema not passed as format")
	}
}

func TestCallLearningJSONMalformedNotRetried(t *testing.T) {
	fake := &fakeChatModel{responses: []*types.ChatResponse{
		{Content: "complete but not json at all", FinishReason: "stop"},
	}}
	svc := llmService(fake)
	var out struct{ Maps map[string]string }
	if err := svc.callLearningJSON(t.Context(), "m", "sys", "user", llmSchema, 64, 128, &out); err != errMalformedLLM {
		t.Fatalf("err = %v, want errMalformedLLM", err)
	}
	if fake.calls.Load() != 1 {
		t.Fatalf("malformed output must not be retried, calls = %d", fake.calls.Load())
	}
}

func TestCallLearningJSONDoubleTruncationFails(t *testing.T) {
	fake := &fakeChatModel{responses: []*types.ChatResponse{
		{Content: "", FinishReason: "length"},
		{Content: "", FinishReason: "length"},
	}}
	svc := llmService(fake)
	var out struct{ Maps map[string]string }
	err := svc.callLearningJSON(t.Context(), "m", "sys", "user", llmSchema, 64, 128, &out)
	if err != errMalformedLLM && !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("err = %v, want malformed", err)
	}
}

func TestResolveLearningModelIDChain(t *testing.T) {
	if got := resolveLearningModelID(nil); got != "" {
		t.Fatal("nil KB has no model")
	}
	kb := &types.KnowledgeBase{}
	if got := resolveLearningModelID(kb); got != "" {
		t.Fatal("empty KB has no model")
	}
	kb.SummaryModelID = "summary-m"
	if got := resolveLearningModelID(kb); got != "summary-m" {
		t.Fatalf("fallback = %q, want summary-m", got)
	}
	kb.WikiConfig = &types.WikiConfig{SynthesisModelID: "wiki-m"}
	if got := resolveLearningModelID(kb); got != "wiki-m" {
		t.Fatalf("chain = %q, want wiki-m", got)
	}
}

func TestLearningKBGate(t *testing.T) {
	if learningKBGate(nil) {
		t.Fatal("nil KB must gate off")
	}
	kb := &types.KnowledgeBase{}
	if learningKBGate(kb) {
		t.Fatal("nil wiki config must gate off")
	}
	kb.WikiConfig = &types.WikiConfig{}
	if learningKBGate(kb) {
		t.Fatal("default must gate off")
	}
	kb.WikiConfig.LearningFeatures = true
	if !learningKBGate(kb) {
		t.Fatal("explicit switch must gate on")
	}
}
