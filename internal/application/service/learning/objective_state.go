package learning

import (
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Objective evidence derivation (stage 1, 01 §5). PURE and deterministic:
// a function of (objective definitions, attempt facts, item families) with
// NO wall-clock input — time passage never modifies a verified fact
// (recency is a separate display dimension). The same snapshot always
// derives the same states, and duplicate attempt rows collapse through the
// family-keyed independence window, so re-delivery is idempotent.
//
// The five evidence states describe what HAS BEEN SHOWN, never a
// psychological mastery percentage. With no eligible attempts the evidence
// is unknown — no sigmoid(0)=50% is fabricated.

// ObjectiveEvidence is the derived direct-evidence dimension of one
// objective: what eligible (independent, server-graded) attempts exist.
type ObjectiveEvidence struct {
	// FamiliesPassed lists distinct families carrying an eligible pass
	// under the CURRENT content version, sorted for deterministic output.
	FamiliesPassed []string
	// FamiliesPassedHistoric lists families that ever passed under ANY
	// content version — preserved history, never silently discarded.
	FamiliesPassedHistoric []string
	// EligiblePasses/EligibleFailures count independent attempts by
	// outcome under the CURRENT version (feedback-exposed retries inside
	// the family window are neither).
	EligiblePasses   int
	EligibleFailures int
	// LastPassAt/LastFailureAt are the newest eligible attempt times
	// under the current version (zero = none).
	LastPassAt    time.Time
	LastFailureAt time.Time
	// ContractMetAt is when the contract was first satisfied under the
	// current version (zero = never); a later eligible failure after it
	// marks conflicting.
	ContractMetAt time.Time
	// StalePasses counts eligible passes recorded under a different
	// content version than the objective's current one.
	StalePasses int
	// LegacyAttempts counts attempts without family/objective metadata
	// (pre-stage rows): visible, exportable, never promoting.
	LegacyAttempts int
	// PracticeAttempts counts graded trials that can never be strict
	// evidence with a known reason: LLM-draft items, feedback-exposed
	// retries, assistance-mode mismatches.
	PracticeAttempts int
	// Source marks what the evidence derives from: strict quiz linkage
	// or legacy rows lacking metadata.
	Source string
	// Unknown is true when no attempt references this objective at all.
	Unknown bool
}

// ObjectiveDerivation is the full derived state of one objective.
type ObjectiveDerivation struct {
	State    string
	Evidence ObjectiveEvidence
}

// familyIndependentAttempts filters attempts to those not exposed to
// feedback on the SAME family within the preceding window: the retry may
// already know the answer. Keyed by family (not item) so a reworded clone
// cannot dodge the exposure window by changing its item id; attempts
// without a family are legacy and never eligible. Deterministic ordering:
// time asc, failures before correct retries at identical timestamps.
func familyIndependentAttempts(attempts []types.LearningQuizAttempt) []types.LearningQuizAttempt {
	ordered := append([]types.LearningQuizAttempt(nil), attempts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].AnsweredAt.Equal(ordered[j].AnsweredAt) {
			return ordered[i].AnsweredAt.Before(ordered[j].AnsweredAt)
		}
		if ordered[i].IsCorrect != ordered[j].IsCorrect {
			return !ordered[i].IsCorrect
		}
		return ordered[i].ID < ordered[j].ID
	})
	type famKey struct {
		tenant, kb, subject, objective, family string
	}
	last := map[famKey]time.Time{}
	var out []types.LearningQuizAttempt
	for _, a := range ordered {
		if a.FamilyID == "" || a.ObjectiveID == "" {
			continue // legacy: no strict metadata, never eligible
		}
		if !a.Eligible {
			continue // practice trial with a recorded reason (draft/retry/mode)
		}
		key := famKey{formatUintKey(a.TenantID), a.KnowledgeBaseID, a.SubjectID, a.ObjectiveID, a.FamilyID}
		prev, seen := last[key]
		last[key] = a.AnsweredAt
		if seen && a.AnsweredAt.Sub(prev) < ReAskWindowHours*time.Hour {
			continue // feedback-exposed retry inside the window
		}
		out = append(out, a)
	}
	return out
}

// formatUintKey renders a tenant id as a stable map-key string.
func formatUintKey(v uint64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// objectiveAttempts collects the attempts that belong to one objective:
// identity is the objective id FROZEN on the attempt row (slug drift and
// later item edits never move history), plus — display-only — legacy rows
// without frozen metadata whose item currently maps to this objective via
// legacyByItem. The legacy association never converts: those rows still
// count as LegacyAttempts and never promote.
func objectiveAttempts(attempts []types.LearningQuizAttempt, objectiveID string, legacyByItem map[string]string) []types.LearningQuizAttempt {
	// Most callers already partition their snapshot by objective. Derivation
	// only reads these rows, so reuse that slice instead of copying the entire
	// history a second time. Unpartitioned callers retain the filtering below.
	allMatch := true
	for _, a := range attempts {
		if a.ObjectiveID != objectiveID && !(a.ObjectiveID == "" && legacyByItem != nil && legacyByItem[a.QuizItemID] == objectiveID) {
			allMatch = false
			break
		}
	}
	if allMatch {
		return attempts
	}
	var out []types.LearningQuizAttempt
	for _, a := range attempts {
		if a.ObjectiveID == objectiveID {
			out = append(out, a)
			continue
		}
		if a.ObjectiveID == "" && legacyByItem != nil && legacyByItem[a.QuizItemID] == objectiveID {
			out = append(out, a)
		}
	}
	return out
}

// DeriveObjectiveState applies the frozen verification contract to one
// objective's attempt facts. Rules (objective-contract-v1):
//
//   - concept_two_family: ≥2 distinct families with an eligible pass under
//     the CURRENT content version → verified; exactly 1 → partial; a later
//     eligible failure after the contract was met → conflicting; the
//     contract met only via stale-version passes → stale_content.
//   - task_checks: all critical checks passed in one eligible, version-bound task.
//   - Legacy attempts (no family metadata) never contribute families;
//     when only they exist the source reads legacy_unverified.
//   - Zero attempts → unknown evidence, state unverified. No probability
//     is synthesized.
func DeriveObjectiveState(o types.LearningObjective, attempts []types.LearningQuizAttempt, legacyByItem map[string]string, taskAttempts []types.LearningTaskAttempt) ObjectiveDerivation {
	ev := ObjectiveEvidence{Source: types.ObjectiveSourceQuiz, FamiliesPassed: []string{}, FamiliesPassedHistoric: []string{}}
	type fact struct {
		id, family, version string
		at                  time.Time
		pass                bool
	}
	var facts []fact
	subjects := map[string]bool{}
	for _, a := range objectiveAttempts(attempts, o.ID, legacyByItem) {
		if a.TenantID != o.TenantID || a.KnowledgeBaseID != o.KnowledgeBaseID {
			continue
		}
		subjects[a.SubjectID] = true
		if a.ObjectiveID == "" || a.FamilyID == "" || a.ContractVersion == "" || a.ScorerVersion == "" || a.RubricVersion == "" || a.ContentVersion == "" {
			ev.LegacyAttempts++
			continue
		}
		if !a.Eligible || a.ContractVersion != o.ContractVersion || a.ScorerVersion != types.MCQScorerVersion || a.ItemStatus != types.LearningQuizStatusPublished || a.AssistanceMode != types.AssistanceClosedBook {
			ev.PracticeAttempts++
			continue
		}
		if o.ContractType != types.ObjectiveContractConceptTwoFamily {
			continue
		}
		facts = append(facts, fact{a.ID, a.FamilyID, a.ContentVersion, a.AnsweredAt, a.IsCorrect})
	}
	for _, a := range taskAttempts {
		if a.ObjectiveID != o.ID || a.TenantID != o.TenantID || a.KnowledgeBaseID != o.KnowledgeBaseID {
			continue
		}
		subjects[a.SubjectID] = true
		if a.FamilyID == "" || a.ObjectiveVersion == "" || a.ContractVersion == "" || a.ScorerVersion == "" || a.RubricVersion == "" {
			ev.LegacyAttempts++
			continue
		}
		if !a.Eligible || a.ContractVersion != o.ContractVersion || a.ScorerVersion != types.TaskScorerVersion || a.AssistanceMode == types.AssistanceAssistantHelp {
			ev.PracticeAttempts++
			continue
		}
		if o.ContractType != types.ObjectiveContractTaskChecks {
			continue
		}
		facts = append(facts, fact{a.ID, a.FamilyID, a.ObjectiveVersion, a.SubmittedAt, a.IsPassed})
	}
	ev.Unknown = len(facts) == 0 && ev.LegacyAttempts == 0 && ev.PracticeAttempts == 0
	if ev.LegacyAttempts > 0 && len(facts) == 0 {
		ev.Source = types.ObjectiveSourceLegacy
	}
	if len(o.ContractParams) > 0 || len(subjects) > 1 || o.Status != types.LearningObjectiveStatusPublished || o.ContractVersion != types.ObjectiveContractVersion || o.ContentVersion == "" {
		return ObjectiveDerivation{State: types.ObjectiveStateUnverified, Evidence: ev}
	}
	required := 2
	if o.ContractType == types.ObjectiveContractTaskChecks {
		required = 1
	} else if o.ContractType != types.ObjectiveContractConceptTwoFamily {
		return ObjectiveDerivation{State: types.ObjectiveStateUnverified, Evidence: ev}
	}
	sort.Slice(facts, func(i, j int) bool {
		if !facts[i].at.Equal(facts[j].at) {
			return facts[i].at.Before(facts[j].at)
		}
		if facts[i].pass != facts[j].pass {
			return !facts[i].pass
		}
		return facts[i].id < facts[j].id
	})
	seenIDs := map[string]bool{}
	seenFamilies := map[string]bool{}
	historic := map[string]bool{}
	fresh := map[string]time.Time{}
	old := map[string]map[string]bool{}
	for _, a := range facts {
		if a.id == "" || a.at.IsZero() || seenIDs[a.id] {
			continue
		}
		seenIDs[a.id] = true
		// Once feedback for a family exists it can be practice, never another new family observation.
		key := a.version + "\x00" + a.family
		if seenFamilies[key] {
			continue
		}
		seenFamilies[key] = true
		if a.pass {
			historic[a.family] = true
		}
		if a.version != o.ContentVersion {
			if a.pass {
				ev.StalePasses++
				if old[a.version] == nil {
					old[a.version] = map[string]bool{}
				}
				old[a.version][a.family] = true
			}
			continue
		}
		if a.pass {
			ev.EligiblePasses++
			fresh[a.family] = a.at
			ev.LastPassAt = a.at
		} else {
			ev.EligibleFailures++
			ev.LastFailureAt = a.at
		}
	}
	for f := range historic {
		ev.FamiliesPassedHistoric = append(ev.FamiliesPassedHistoric, f)
	}
	sort.Strings(ev.FamiliesPassedHistoric)
	for f := range fresh {
		ev.FamiliesPassed = append(ev.FamiliesPassed, f)
	}
	sort.Strings(ev.FamiliesPassed)
	met, ok := nthEarliest(fresh, required)
	state := types.ObjectiveStateUnverified
	if ok {
		ev.ContractMetAt = met
		state = types.ObjectiveStateVerified
		if !ev.LastFailureAt.Before(met) && !ev.LastFailureAt.IsZero() {
			n := 0
			for _, at := range fresh {
				if at.After(ev.LastFailureAt) {
					n++
				}
			}
			if n < required {
				state = types.ObjectiveStateConflicting
			}
		}
	} else if len(fresh) > 0 {
		state = types.ObjectiveStatePartial
	} else {
		for _, fs := range old {
			if len(fs) >= required {
				state = types.ObjectiveStateStale
				break
			}
		}
	}
	return ObjectiveDerivation{State: state, Evidence: ev}
}

// nthEarliest returns the moment the n-th distinct family had passed (the
// n-th earliest of the family first-pass times), i.e. when a
// "n distinct families" contract first became satisfied.
func nthEarliest(familyFirstPass map[string]time.Time, n int) (time.Time, bool) {
	if n <= 0 || len(familyFirstPass) < n {
		return time.Time{}, false
	}
	times := make([]time.Time, 0, len(familyFirstPass))
	for _, t := range familyFirstPass {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times[n-1], true
}
