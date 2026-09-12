package learning

import (
	"math"
	"testing"
	"time"
)

func TestReconcileSlugMovesStatePreservingNumbers(t *testing.T) {
	// "concept/rag" was renamed; its alias now points at "concept/retrieval".
	events := []Event{
		cite(foldRef),
		{Type: "quiz_correct", Weight: WeightQuizCorrect, OccurredAt: foldRef.Add(time.Hour)},
	}
	state := FoldAll(FoldState{}, events)
	migrations := ReconcileSlug(
		map[string]FoldState{"concept/rag": state},
		map[string][]Event{"concept/rag": events},
		map[string]string{"concept/rag": "concept/retrieval"},
	)
	if len(migrations) != 1 {
		t.Fatalf("migrations = %d, want 1", len(migrations))
	}
	m := migrations[0]
	if m.FromSlug != "concept/rag" || m.ToSlug != "concept/retrieval" {
		t.Fatalf("route = %q -> %q", m.FromSlug, m.ToSlug)
	}
	if m.State.Logit != state.Logit || m.State.EvidenceCount != state.EvidenceCount ||
		m.State.PositiveCount != state.PositiveCount || m.State.Stability != state.Stability {
		t.Fatalf("move altered the fold: %+v vs %+v", m.State, state)
	}
	// The promise the design makes: p_eff is unchanged by a pure move.
	if math.Abs(EffectiveP(m.State, foldRef.Add(72*time.Hour))-EffectiveP(state, foldRef.Add(72*time.Hour))) > 1e-12 {
		t.Fatal("p_eff changed across a pure move")
	}
}

func TestReconcileSlugMergesByReplayNotAddition(t *testing.T) {
	// Both slugs sit at logit 3.5; a naive addition would claim 7.0, far
	// past the clamp. A replay must land exactly on LogitCap with both
	// event lists fully counted.
	evA := []Event{
		cite(foldRef),
		cite(foldRef.Add(time.Hour)),
		cite(foldRef.Add(2 * time.Hour)),
		{Type: "quiz_correct", Weight: WeightQuizCorrect, OccurredAt: foldRef.Add(3 * time.Hour)},
	} // logit = 3*1.0 + 2.2 = 5.2 -> clamped 4.0
	evB := []Event{
		cite(foldRef.Add(30 * time.Minute)),
		cite(foldRef.Add(90 * time.Minute)),
		{Type: "quiz_correct", Weight: WeightQuizCorrect, OccurredAt: foldRef.Add(4 * time.Hour)},
	} // same shape, also clamps to 4.0 on its own
	stateA := FoldAll(FoldState{}, evA)
	stateB := FoldAll(FoldState{}, evB)

	migrations := ReconcileSlug(
		map[string]FoldState{"concept/a-old": stateA, "concept/a": stateB},
		map[string][]Event{"concept/a-old": evA, "concept/a": evB},
		map[string]string{"concept/a-old": "concept/a"},
	)
	if len(migrations) != 1 {
		t.Fatalf("migrations = %d, want 1", len(migrations))
	}
	m := migrations[0]
	if m.State.Logit != LogitCap {
		t.Fatalf("replay logit = %v, want clamp %v (addition would claim %v)",
			m.State.Logit, LogitCap, stateA.Logit+stateB.Logit)
	}
	if m.State.EvidenceCount != len(evA)+len(evB) {
		t.Fatalf("merged evidence = %d, want %d", m.State.EvidenceCount, len(evA)+len(evB))
	}
	if m.State.PositiveCount != len(evA)+len(evB) {
		t.Fatalf("merged positive count = %d, want %d", m.State.PositiveCount, len(evA)+len(evB))
	}
	// Replay equals folding the interleaved history directly.
	interleaved := append(append([]Event{}, evA...), evB...)
	want := FoldAll(FoldState{}, sortedByTime(interleaved))
	if m.State.Logit != want.Logit || m.State.EvidenceCount != want.EvidenceCount ||
		m.State.Stability != want.Stability || !m.State.LastEvidenceAt.Equal(want.LastEvidenceAt) {
		t.Fatalf("replay %+v differs from direct fold %+v", m.State, want)
	}
}

func TestReconcileSlugLeavesUnmatchedRowsForNextRound(t *testing.T) {
	state := FoldAll(FoldState{}, []Event{cite(foldRef)})
	migrations := ReconcileSlug(
		map[string]FoldState{"concept/orphan": state},
		map[string][]Event{"concept/orphan": nil},
		map[string]string{"concept/other": "concept/somewhere"}, // no entry for orphan
	)
	if len(migrations) != 0 {
		t.Fatalf("unmatched slug must not migrate, got %+v", migrations)
	}
}

func TestReconcileSlugIgnoresSelfAndEmptyTargets(t *testing.T) {
	state := FoldAll(FoldState{}, []Event{cite(foldRef)})
	migrations := ReconcileSlug(
		map[string]FoldState{
			"concept/self": state,
			"concept/void": state,
		},
		map[string][]Event{},
		map[string]string{
			"concept/self": "concept/self", // self-alias, no-op
			"concept/void": "",             // empty target, refuse
		},
	)
	if len(migrations) != 0 {
		t.Fatalf("self/empty aliases must not migrate, got %+v", migrations)
	}
}

func sortedByTime(events []Event) []Event {
	out := append([]Event{}, events...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].OccurredAt.Before(out[j-1].OccurredAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
