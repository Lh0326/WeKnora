// Package learning implements the mastery layer of the knowledge-network
// feature: event folding, lazy decay, level hysteresis, slug reconciliation
// and deterministic quiz grading. Everything in this package is pure — no
// I/O, no clocks, no randomness — so each rule is exactly as testable as the
// claim it encodes, and the repository/service layers above stay thin.
package learning

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Level is the four-tier display grade derived from p_eff. It is never
// stored: levels and probabilities are read-time views of the folded state,
// which is what lets thresholds be retuned without a rewrite pass.
type Level string

const (
	LevelUnseen   Level = "unseen"
	LevelTouched  Level = "touched"
	LevelFamiliar Level = "familiar"
	LevelMastered Level = "mastered"
)

// FoldState is the fold of every event for one (person, node) pair. It is
// the value FormOf the persisted row: FoldEvent consumes and produces it,
// EffectiveP and LevelOf read it.
type FoldState struct {
	Logit          float64
	EvidenceCount  int
	PositiveCount  int
	NegativeCount  int
	Stability      float64 // days
	LastEvidenceAt time.Time
	FirstSeenAt    time.Time
}

// StateFromModel lifts a persisted mastery row into the fold value. An empty
// model yields the zero fold, which every fold helper treats as "first
// evidence initialises".
func StateFromModel(m *types.MasteryState) FoldState {
	if m == nil {
		return FoldState{}
	}
	return FoldState{
		Logit:          m.Logit,
		EvidenceCount:  m.EvidenceCount,
		PositiveCount:  m.PositiveCount,
		NegativeCount:  m.NegativeCount,
		Stability:      m.Stability,
		LastEvidenceAt: m.LastEvidenceAt,
		FirstSeenAt:    m.FirstSeenAt,
	}
}

// ApplyTo writes the fold back onto a persisted row. Scope fields are left
// untouched; the caller owns identity.
func (s FoldState) ApplyTo(m *types.MasteryState) {
	m.Logit = s.Logit
	m.EvidenceCount = s.EvidenceCount
	m.PositiveCount = s.PositiveCount
	m.NegativeCount = s.NegativeCount
	m.Stability = s.Stability
	m.LastEvidenceAt = s.LastEvidenceAt
	m.FirstSeenAt = s.FirstSeenAt
}

// Event is one foldable piece of evidence. Weight is already resolved (event
// type × decay/discount factors) and is what gets frozen on the persisted
// learning_events row.
type Event struct {
	Type       string // one of the types.LearningEvent* constants
	Weight     float64
	OccurredAt time.Time
}

// FoldAll folds events in the given order. Callers holding unsorted history
// must sort by OccurredAt first; folding is commutative over logit and
// counts, but first/last-seen bookkeeping is order-sensitive for the
// timestamp bookkeeping only.
func FoldAll(state FoldState, events []Event) FoldState {
	for _, e := range events {
		state = FoldEvent(state, e)
	}
	return state
}
