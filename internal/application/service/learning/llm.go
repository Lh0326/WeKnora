package learning

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// Token budgets for the three learning write paths. The first attempt is
// sized for the normal payload; the retry budget is what a truncated
// response gets, mirroring memory extraction's one-retry-larger policy.
const (
	learningAdjudicateBudget = 2048
	learningAdjudicateRetry  = 8192
	learningQuizBudget       = 8192
	learningQuizRetry        = 16384
)

// resolveLearningModelID is the two-level model chain for learning write
// paths: the KB's wiki synthesis model, else the KB's summary model. A
// blank result is a configuration gap, not a transient error — the caller
// skips the pass with a warning (the wiki finalize precedent).
func resolveLearningModelID(kb *types.KnowledgeBase) string {
	if kb == nil {
		return ""
	}
	if kb.WikiConfig != nil && kb.WikiConfig.SynthesisModelID != "" {
		return kb.WikiConfig.SynthesisModelID
	}
	return kb.SummaryModelID
}

// callLearningJSON performs one structured-output adjudication, following
// the memory-extraction calling convention exactly: system+user messages,
// temperature zero, thinking off, the JSON schema passed as the response
// format, an explicit token budget, one truncation retry at a larger
// budget, and — deliberately — no retry for malformed-but-complete output
// (the same prompt at temperature zero produces the same garbage).
// A returned error means "skip this pass"; parse failures are logged and
// reported as errMalformedLLM so callers can distinguish skip-vs-retry.
var errMalformedLLM = errors.New("learning: malformed LLM response")

func (s *Service) callLearningJSON(
	ctx context.Context, modelID, system, user string,
	schema json.RawMessage, budget, retryBudget int, out any,
) error {
	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		return err
	}
	thinking := false
	opts := func(maxTokens int) *chat.ChatOptions {
		return &chat.ChatOptions{
			Temperature:         0,
			MaxCompletionTokens: maxTokens,
			Thinking:            &thinking,
			Format:              schema,
		}
	}
	messages := []chat.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	response, err := chatModel.Chat(ctx, messages, opts(budget))
	if err != nil {
		return err
	}
	if isTruncatedLLM(response) {
		logger.Warnf(ctx, "learning: truncated adjudication response, retrying with larger budget")
		response, err = chatModel.Chat(ctx, messages, opts(retryBudget))
		if err != nil {
			return err
		}
	}
	if response == nil || strings.TrimSpace(response.Content) == "" {
		return errMalformedLLM
	}
	if err := common.ParseLLMJsonResponse(response.Content, out); err != nil {
		// A malformed complete answer is the model's fault, not a transient
		// failure; warn and skip rather than burning budget on retries.
		logger.Warnf(ctx, "learning: unparsable adjudication response: %v", err)
		return errMalformedLLM
	}
	return nil
}

// isTruncatedLLM mirrors memory extraction's truncation probe.
func isTruncatedLLM(response *types.ChatResponse) bool {
	if response == nil {
		return true
	}
	if strings.TrimSpace(response.Content) == "" {
		return true
	}
	return response.FinishReason == "length"
}

// learningKBGate reports whether the KB has the per-KB learning switch on.
// The switch gates exactly the two sustained-LLM paths (edges and quizzes);
// topic mapping and collection stay behind the global gate only.
func learningKBGate(kb *types.KnowledgeBase) bool {
	return kb != nil && kb.WikiConfig != nil && kb.WikiConfig.LearningFeatures
}
