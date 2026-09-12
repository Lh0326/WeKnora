package learning

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// agentReadHook decorates the agent's wiki_read_page tool so that every page
// the agent reads while answering lands as an agent_read trace on the
// asker's timeline.
//
// Channel separation (review P2-D): an agent's tool access is NOT the
// person's learning. agent_read carries zero mastery weight — it never folds,
// never refreshes the retention anchor, and never consumes the human read's
// 48-hour scoring window, so a later deliberate browser read of the same
// page still earns its full first-touch weight. What it buys is honesty in
// the timeline: "the assistant consulted this page on your behalf" stays
// visible instead of laundering a tool call into a studied read.
type agentReadHook struct {
	types.Tool
	learning interfaces.LearningService
}

// WrapWikiReadTool wraps the wiki_read_page tool with learning collection.
// Recording is fire-and-forget on a detached context: evidence must never
// stall or fail the agent's turn. The tool's structured result
// (Data["found_kbs"]: slug → KB IDs where found) is the identity source, so
// the wrapper never re-resolves slugs itself.
func WrapWikiReadTool(base types.Tool, svc interfaces.LearningService) types.Tool {
	return &agentReadHook{Tool: base, learning: svc}
}

func (h *agentReadHook) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if capture, ok := h.learning.(interfaces.LearningContextCapturer); ok {
		ctx = capture.CaptureCollectionContext(ctx)
	}
	res, err := h.Tool.Execute(ctx, args)
	if err != nil || res == nil || !res.Success {
		return res, err
	}
	foundKBs, _ := res.Data["found_kbs"].(map[string][]string)
	if len(foundKBs) == 0 {
		return res, err
	}
	recordCtx := context.WithoutCancel(ctx)
	for slug, kbIDs := range foundKBs {
		for _, kbID := range kbIDs {
			go func(kbID, slug string) {
				// Best effort by design: a context without principal (e.g.
				// a background caller) forfeits this one trace, never the
				// answer itself.
				_ = h.learning.RecordAgentRead(recordCtx, kbID, slug)
			}(kbID, slug)
		}
	}
	return res, err
}

// RecordAgentRead lands one agent wiki_read_page call as a zero-weight
// timeline trace. Guards mirror the human read path (kill switch, opt-out,
// node membership); dedup is its own channel — at most one agent_read per
// slug per re-ask window, refresh-shielded like every read — so agent
// re-reads cannot spam the timeline either. It deliberately shares nothing
// with the wiki_tool_read windows: the human's scoring first-touch is
// measured only by human reads.
func (s *Service) recordAgentRead(ctx context.Context, kbID, slug string) error {
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
	mu := s.lockNode(scope, slug)
	defer mu.Unlock()

	now := time.Now()
	prior, err := listEventWindow(ctx, s.repo, scope, now.Add(-touchLookback))
	if err != nil {
		logger.Warnf(ctx, "learning: agent-read dedup lookup failed (slug %s): %v", slug, err)
		return nil // conservative: skip rather than risk spamming
	}
	for _, ev := range prior {
		if ev.Slug != slug || ev.Type != types.LearningEventAgentRead {
			continue
		}
		if now.Sub(ev.OccurredAt) <= ReadRapidDedup {
			return nil
		}
		if now.Sub(ev.OccurredAt) <= ReAskWindowHours*time.Hour {
			return nil // one agent trace per window is enough
		}
	}
	return s.repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: scope.KnowledgeBaseID,
		Slug:            slug,
		Type:            types.LearningEventAgentRead,
		Weight:          0, // zero by contract: tool access is not learning
		OccurredAt:      now,
	})
}
