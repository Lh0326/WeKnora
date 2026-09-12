package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type learningCompletionStream struct {
	interfaces.StreamManager
	beforeAppend func(interfaces.StreamEvent)
}

func (s *learningCompletionStream) AppendEvent(_ context.Context, _, _ string, evt interfaces.StreamEvent) error {
	s.beforeAppend(evt)
	return nil
}

func TestLearningCollectionSettlesBeforeStreamCompletion(t *testing.T) {
	for _, mode := range []string{"rag", "agent"} {
		t.Run(mode, func(t *testing.T) {
			writes, completions := 0, 0
			message := &types.Message{ID: "answer", KnowledgeReferences: types.References{
				{KnowledgeBaseID: "kb"}, {KnowledgeBaseID: "kb"}, nil,
			}}
			stream := &learningCompletionStream{beforeAppend: func(evt interfaces.StreamEvent) {
				if evt.Type != types.ResponseTypeComplete {
					return
				}
				completions++
				if writes != 1 {
					t.Fatalf("completion preceded learning collection: %d", writes)
				}
				ids := evt.Data["learning_kb_ids"].([]string)
				if len(ids) != 1 || ids[0] != "kb" {
					t.Fatalf("unexpected invalidation scope: %v", ids)
				}
			}}
			h := NewAgentStreamHandler(context.Background(), "session", "answer", "request", 1, time.Now(), message, stream, event.NewEventBus(), nil)
			h.beforeComplete = func(ctx context.Context, got *types.Message) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("collection must be bounded")
				}
				if got != message {
					t.Error("wrong answer collected")
				}
				writes++
			}
			data := event.AgentCompleteData{}
			if mode == "agent" {
				data.MessageID = "answer"
			}
			for i := 0; i < 2; i++ {
				if err := h.handleComplete(context.Background(), event.Event{Data: data}); err != nil {
					t.Fatal(err)
				}
			}
			if writes != 1 || completions != 2 {
				t.Fatalf("writes=%d completions=%d", writes, completions)
			}
		})
	}
}
