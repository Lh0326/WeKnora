package learning

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// stubWikiReadTool mimics wiki_read_page's contract: the structured result
// carries found_kbs (slug → KB IDs where found), which is the wrapper's
// identity source.
type stubWikiReadTool struct {
	fail     bool
	foundKBs map[string][]string
}

func (s *stubWikiReadTool) Name() string                  { return "wiki_read_page" }
func (s *stubWikiReadTool) Description() string           { return "stub" }
func (s *stubWikiReadTool) Parameters() json.RawMessage   { return nil }
func (s *stubWikiReadTool) Execute(_ context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	if s.fail {
		return &types.ToolResult{Success: false, Error: "not found"}, nil
	}
	return &types.ToolResult{
		Success: true,
		Output:  "(pages)",
		Data:    map[string]interface{}{"found_kbs": s.foundKBs},
	}, nil
}

func readHookService(t *testing.T) (*Service, *stubLearningRepo) {
	t.Helper()
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, testWikiPage("concept/rag", []string{"c1"}, nil))
	wiki.addPage(testKB, testWikiPage("entity/weknora", nil, nil))
	return testService(repo, wiki), repo
}

// waitForEvents polls the async fire-and-forget recorder until n events land
// (or the deadline fails the test): the wrapper must never block the tool
// call, so assertions have to tolerate the goroutine scheduling delay.
func waitForEvents(t *testing.T, repo *stubLearningRepo, n int) []types.LearningEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events := repo.snapshotEvents()
		if len(events) >= n {
			return events
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected %d events within deadline, got %d", n, len(repo.snapshotEvents()))
	return nil
}

// TestAgentReadHookRecordsTouches: a successful agent read of two node pages
// lands one wiki_tool_read per slug on the asker's mastery — the capture gap
// the audit found (agent answers built from wiki reads carry no chunk
// citations, so answer_cite never fired for them).
func TestAgentReadHookRecordsTouches(t *testing.T) {
	svc, repo := readHookService(t)
	t.Setenv("LEARNING_ENABLE", "true")

	wrapped := WrapWikiReadTool(&stubWikiReadTool{foundKBs: map[string][]string{
		"concept/rag":  {testKB},
		"entity/weknora": {testKB},
	}}, svc)
	res, err := wrapped.Execute(collectorCtx(1, "alice"), json.RawMessage(`{"slugs":["concept/rag"]}`))
	if err != nil || res == nil || !res.Success {
		t.Fatalf("wrapper must pass the tool result through untouched: res=%+v err=%v", res, err)
	}

	events := waitForEvents(t, repo, 2)
	bySlug := map[string]types.LearningEvent{}
	for _, e := range events {
		if e.Type != types.LearningEventWikiToolRead {
			t.Fatalf("unexpected event type %s for %s", e.Type, e.Slug)
		}
		bySlug[e.Slug] = e
	}
	if len(bySlug) != 2 {
		t.Fatalf("expected touches for both slugs, got %+v", bySlug)
	}
	// §3.3.6: an opportunistic agent read rides the topic-signal weight.
	if w := bySlug["concept/rag"].Weight; w != WeightTopicSignal {
		t.Fatalf("agent read weight = %v, want %v", w, WeightTopicSignal)
	}
}

// TestAgentReadHookSkipsFailures: failed lookups record nothing.
func TestAgentReadHookSkipsFailures(t *testing.T) {
	svc, repo := readHookService(t)
	t.Setenv("LEARNING_ENABLE", "true")

	wrapped := WrapWikiReadTool(&stubWikiReadTool{fail: true}, svc)
	res, err := wrapped.Execute(collectorCtx(1, "alice"), json.RawMessage(`{}`))
	if err != nil || res.Success {
		t.Fatalf("failure must pass through: res=%+v err=%v", res, err)
	}
	time.Sleep(120 * time.Millisecond)
	if n := len(repo.snapshotEvents()); n != 0 {
		t.Fatalf("failed read must not record events, got %d", n)
	}
}

// TestAgentReadHookRefreshShield: the agent re-reading the same page inside
// the rapid-duplicate window records nothing extra — a chatty agent earns
// exactly one touch per window, same as a human reader.
func TestAgentReadHookRefreshShield(t *testing.T) {
	svc, repo := readHookService(t)
	t.Setenv("LEARNING_ENABLE", "true")

	wrapped := WrapWikiReadTool(&stubWikiReadTool{foundKBs: map[string][]string{
		"concept/rag": {testKB},
	}}, svc)
	for i := 0; i < 3; i++ {
		if _, err := wrapped.Execute(collectorCtx(1, "alice"), json.RawMessage(`{}`)); err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}

	waitForEvents(t, repo, 1)
	// Wait for QUIESCENCE, not a fixed window: under -race the three
	// fire-and-forget recorders can be scheduled far apart, and a fixed
	// sleep after the FIRST event is exactly what made this test flaky
	// (a straggler goroutine landing after the window read as a shield
	// failure). Quiescence = the count stops changing for a sustained
	// interval; a real shield break always surfaces inside the deadline.
	last, stable := -1, 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n := len(repo.snapshotEvents())
		if n == last {
			stable++
		} else {
			last, stable = n, 0
		}
		if stable >= 20 { // ~400ms with no new event
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last != 1 {
		t.Fatalf("rapid re-reads must collapse to one event, got %d", last)
	}
}
