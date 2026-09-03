package learning

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// agentReadHook decorates the agent's wiki_read_page tool so that every page
// the agent reads while answering also lands as an opportunistic
// wiki_tool_read touch on the asker's mastery — the producer this event type
// was designed for (§3.3.6: agent reads ride the topic-signal weight), and
// the capture gap the topic-4 evidence audit found: an agent-mode answer
// assembled from wiki page reads carries no chunk citations, so channel A
// (answer_cite) never fired for it.
//
// All policy stays inside RecordWikiRead: kill switch, opt-out, node
// membership (entity/concept pages of that KB only), the 60-second refresh
// shield and the 48-hour scoring window — so an agent that re-reads the same
// page five times inside one turn earns exactly one scored touch, exactly
// like a human reader.
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
				// a background caller) forfeits this one touch, never the
				// answer itself.
				_ = h.learning.RecordWikiRead(recordCtx, kbID, slug)
			}(kbID, slug)
		}
	}
	return res, err
}
