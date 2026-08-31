package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// overlayFakeLearning serves a fixed mastery overlay.
type overlayFakeLearning struct {
	interfaces.LearningService
	overlay map[string]interfaces.MasteryOverlayEntry
	calls   int
	err     error
}

func (f *overlayFakeLearning) MasteryOverlay(_ context.Context, _ string, _ []string) (map[string]interfaces.MasteryOverlayEntry, error) {
	f.calls++
	return f.overlay, f.err
}

func baseGraphData() *types.WikiGraphData {
	return &types.WikiGraphData{
		Nodes: []types.WikiGraphNode{
			{Slug: "concept/rag", Title: "RAG", PageType: "concept", LinkCount: 3},
			{Slug: "concept/decay", Title: "Decay", PageType: "concept"},
		},
		Edges: []types.WikiGraphEdge{{Source: "concept/rag", Target: "concept/decay"}},
		Meta:  types.WikiGraphMeta{Mode: "overview", Total: 2, Returned: 2},
	}
}

// TestGraphMasteryOverlayOffLeavesResponseByteIdentical is the
// default-path proof the design contract demands: without the explicit
// request flag — or with a failing overlay — the graph payload serializes
// exactly as it did before the field existed.
func TestGraphMasteryOverlayOffLeavesResponseByteIdentical(t *testing.T) {
	fake := &overlayFakeLearning{overlay: map[string]interfaces.MasteryOverlayEntry{
		"concept/rag": {Level: "familiar"},
	}}
	h := NewWikiPageHandler(nil, nil, nil, nil, nil, fake)

	before, err := json.Marshal(baseGraphData())
	require.NoError(t, err)

	// Case 1: flag unset.
	data := baseGraphData()
	h.overlayGraphMastery(context.Background(), data, "kb-1", false)
	after, err := json.Marshal(data)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.Equal(t, string(before), string(after), "byte-identical, not merely equal")
	require.Equal(t, 0, fake.calls, "flag unset must not consult the learning service")

	// Case 2: nil learning service (lite deployments).
	h2 := NewWikiPageHandler(nil, nil, nil, nil, nil, nil)
	data2 := baseGraphData()
	h2.overlayGraphMastery(context.Background(), data2, "kb-1", true)
	after2, err := json.Marshal(data2)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after2))

	// Case 3: overlay error — decorative, must not leak.
	fakeErr := &overlayFakeLearning{err: context.Canceled}
	h3 := NewWikiPageHandler(nil, nil, nil, nil, nil, fakeErr)
	data3 := baseGraphData()
	h3.overlayGraphMastery(context.Background(), data3, "kb-1", true)
	after3, err := json.Marshal(data3)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after3))
}

// TestGraphMasteryOverlayOnPaintsNodes: with the flag set and the service
// healthy, nodes gain the tier fields and untouched nodes stay clean.
func TestGraphMasteryOverlayOnPaintsNodes(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	fake := &overlayFakeLearning{overlay: map[string]interfaces.MasteryOverlayEntry{
		"concept/rag":   {Level: "mastered"},
		"concept/decay": {Level: "touched", LowConfidence: true},
	}}
	h := NewWikiPageHandler(nil, nil, nil, nil, nil, fake)

	data := baseGraphData()
	h.overlayGraphMastery(context.Background(), data, "kb-1", true)
	require.Equal(t, 1, fake.calls)
	require.Equal(t, "mastered", data.Nodes[0].MasteryLevel)
	require.False(t, data.Nodes[0].LowConfidence)
	require.Equal(t, "touched", data.Nodes[1].MasteryLevel)
	require.True(t, data.Nodes[1].LowConfidence)

	// A node the caller never touched carries no overlay fields at all.
	fake.overlay = map[string]interfaces.MasteryOverlayEntry{"concept/rag": {Level: "touched"}}
	data2 := baseGraphData()
	h.overlayGraphMastery(context.Background(), data2, "kb-1", true)
	require.Empty(t, data2.Nodes[1].MasteryLevel)
	require.False(t, data2.Nodes[1].LowConfidence)
}
