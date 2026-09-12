package handler

import (
	"context"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type componentLegacyReaderFake struct {
	fakeLearningService
	interfaces.LearningComponentService
	legacyCalls int
	kb          string
	budget      int
	goal        string
}

func (f *componentLegacyReaderFake) ComponentView(_ context.Context, kb string, budget int, goal string) (*interfaces.ComponentView, error) {
	f.legacyCalls++
	f.kb, f.budget, f.goal = kb, budget, goal
	return &interfaces.ComponentView{}, nil
}

type componentScopeReaderFake struct {
	componentLegacyReaderFake
	scopeCalls int
	topic      string
}

func (f *componentScopeReaderFake) ComponentViewWithTopic(_ context.Context, kb string, budget int, goal, topic string) (*interfaces.ComponentView, error) {
	f.scopeCalls++
	f.kb, f.budget, f.goal, f.topic = kb, budget, goal, topic
	return &interfaces.ComponentView{Plan: interfaces.ComponentPlanInfo{TopicScope: topic}}, nil
}

func TestComponentHandlerPreservesLegacyReadWithoutTopic(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	fake := &componentLegacyReaderFake{}
	c, w := learningTestContext(t, true)
	c.Params = gin.Params{{Key: "kb_id", Value: "kb-one"}}
	c.Request.URL.RawQuery = "goal=target"
	NewLearningHandler(fake).ComponentView(c)
	require.Empty(t, c.Errors)
	require.Equal(t, 1, fake.legacyCalls)
	require.Equal(t, "kb-one", fake.kb)
	require.Equal(t, 15, fake.budget)
	require.Equal(t, "target", fake.goal)
	require.Equal(t, 200, w.Code)
}

func TestComponentHandlerForwardsModuleAndGoalWithoutDroppingScope(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	fake := &componentScopeReaderFake{}
	c, w := learningTestContext(t, true)
	c.Params = gin.Params{{Key: "kb_id", Value: "kb-one"}}
	c.Request.URL.RawQuery = url.Values{"topic": {"工具调用"}, "goal": {"target"}}.Encode()
	NewLearningHandler(fake).ComponentView(c)
	require.Empty(t, c.Errors)
	require.Equal(t, 1, fake.scopeCalls)
	require.Zero(t, fake.legacyCalls)
	require.Equal(t, "工具调用", fake.topic)
	require.Equal(t, "target", fake.goal)
	require.Equal(t, 15, fake.budget)
	require.Contains(t, w.Body.String(), `"topic_scope":"工具调用"`)
}

func TestComponentHandlerDoesNotSilentlyIgnoreUnsupportedModuleScope(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	fake := &componentLegacyReaderFake{}
	c, _ := learningTestContext(t, true)
	c.Request.URL.RawQuery = "topic=module"
	NewLearningHandler(fake).ComponentView(c)
	require.NotEmpty(t, c.Errors)
	require.Zero(t, fake.legacyCalls)
}
