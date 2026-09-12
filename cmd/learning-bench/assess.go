package main

// Stage-5 independent assessment protocol (03_评估执行与结果模板.md).
//
// This file implements the PURE assessment core: frozen-snapshot challenge
// evaluation, per-user aggregation, metric computation with honest N/A for
// zero denominators, and leakage detection. It deliberately does NOT touch
// the existing simulate/replay/verify/walk/optimize modes — those keep
// their original purposes (rule verification, behavior prediction,
// calibration); none of them ever claimed learning-effect evidence and this
// mode won't relabel them.
//
// Data provenance is declared, not authenticated: every record carries a data_origin tag
// (engineering_fixture | synthetic | expert_scenario | real_user). The
// validator rejects mixed origins; labels alone never prove real-user participation.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"
)

// DataOrigin tags (03 §4.5): each dataset records its origin; mixed
// reports are separated by origin.
const (
	OriginEngineering = "engineering_fixture"
	OriginSynthetic   = "synthetic"
	OriginExpert      = "expert_scenario"
	OriginRealUser    = "real_user"
)

// TestPhase separates training/practice/pretest/posttest/delayed
// (03 §2: 阶段分离，题族不共享).
type TestPhase string

const (
	PhaseTrain    TestPhase = "train"
	PhasePractice TestPhase = "practice"
	PhasePretest  TestPhase = "pretest"
	PhasePosttest TestPhase = "posttest"
	PhaseDelayed  TestPhase = "delayed"
)

// AssessSnapshot is the frozen state BEFORE a holdout challenge is
// presented (03 §4.2): state_as_of ≤ presented_at < graded_at.
type AssessSnapshot struct {
	SnapshotID    string `json:"snapshot_id"`
	ParticipantID string `json:"participant_id"`
	ObjectiveID   string `json:"objective_id"`
	// StateBefore is the objective state at freeze time.
	StateBefore string `json:"state_before"`
	// EvidenceFamiliesBefore lists the family_ids in the frozen evidence.
	EvidenceFamiliesBefore []string  `json:"evidence_families_before"`
	PolicyVersion          string    `json:"policy_version"`
	StateAsOf              time.Time `json:"state_as_of"`
	// ItemID/FamilyID/Phase identify the holdout challenge.
	ItemID   string    `json:"item_id"`
	FamilyID string    `json:"family_id"`
	Phase    TestPhase `json:"phase"`
	// PresentedAt is when the holdout was shown; the hard order is
	// state_as_of ≤ presented_at < graded_at.
	PresentedAt time.Time `json:"presented_at"`
	// Result is the outcome (true=pass, false=fail, missing=not completed).
	Result   *bool     `json:"result,omitempty"`
	GradedAt time.Time `json:"graded_at,omitempty"`
	// Eligible + IneligibleReason mirror the challenge protocol.
	Eligible         bool   `json:"eligible"`
	IneligibleReason string `json:"ineligible_reason,omitempty"`
}

// AssessParticipant is one user's record.
type AssessParticipant struct {
	ParticipantID string           `json:"participant_id"`
	Group         string           `json:"group"` // "guided" | "baseline"
	DataOrigin    string           `json:"data_origin"`
	Snapshots     []AssessSnapshot `json:"snapshots"`
	// Full goal universe frozen before challenge assignment; absent means coverage N/A.
	GoalStatesBefore map[string]string `json:"goal_states_before,omitempty"`
	// PosttestScore / PosttestMax: the primary outcome (0-100 normalized).
	PosttestScore *float64 `json:"posttest_score,omitempty"`
	PosttestMax   *float64 `json:"posttest_max,omitempty"`
	// PretestScore for the pre-post gain.
	PretestScore *float64 `json:"pretest_score,omitempty"`
	// Withdrawal/missing tracking: never delete failures.
	Withdrawn     bool   `json:"withdrawn,omitempty"`
	MissingPost   bool   `json:"missing_post,omitempty"`
	MissingReason string `json:"missing_reason,omitempty"`
}

// AssessInput is the complete evaluation payload.
type AssessInput struct {
	Participants []AssessParticipant `json:"participants"`
	TargetType   string              `json:"target_type,omitempty"`
	// FrozenRules identifies the rule version for reproducibility.
	FrozenRules string `json:"frozen_rules"`
	DataOrigin  string `json:"data_origin"` // the DOMINANT origin; per-participant overrides
	// TuningSplit marks this input as TUNING data: the report prints a
	// tuning banner and the JSON carries the flag. Final evaluation must
	// set tuning_split=false (the default).
	TuningSplit bool `json:"tuning_split,omitempty"`
	// RandomSeed enables reproducible baseline path assignment (03 §9).
	RandomSeed int64 `json:"random_seed,omitempty"`
}

// CI is a confidence interval; nil fields = N/A.
type CI struct {
	Lower  *float64 `json:"lower,omitempty"`
	Upper  *float64 `json:"upper,omitempty"`
	Method string   `json:"method,omitempty"`
}

// AssessMetrics is the computed result set (03 §5).
type AssessMetrics struct {
	FrozenRules       string            `json:"frozen_rules"`
	DataOrigin        string            `json:"data_origin"`
	SelectedSnapshots []string          `json:"selected_snapshots"`
	Exclusions        map[string]string `json:"exclusions"`
	EnrolledByGroup   map[string]int    `json:"enrolled_by_group"`
	MissingByGroup    map[string]int    `json:"missing_by_group"`
	WithdrawnByGroup  map[string]int    `json:"withdrawn_by_group"`
	ProvenanceNote    string            `json:"provenance_note"`
	// Per-state challenge outcomes with raw numerators/denominators (03 §5.1).
	VerifiedN                int      `json:"verified_n"`
	VerifiedPasses           int      `json:"verified_passes"`
	VerifiedFailureRate      *float64 `json:"verified_failure_rate"`
	VerifiedSuccessRate      *float64 `json:"verified_success_rate"`
	VerifiedFailureCI        *CI      `json:"verified_failure_ci,omitempty"`
	UnverifiedN              int      `json:"unverified_n"`
	UnverifiedPasses         int      `json:"unverified_passes"`
	UnverifiedPassRate       *float64 `json:"unverified_pass_rate"`
	UnverifiedPassCI         *CI      `json:"unverified_pass_ci,omitempty"`
	UnknownN                 int      `json:"unknown_n"`
	UnknownPasses            int      `json:"unknown_passes"`
	UnknownPassRate          *float64 `json:"unknown_pass_rate"`
	UnknownPassCI            *CI      `json:"unknown_pass_ci,omitempty"`
	TotalObjectives          int      `json:"total_objectives"`
	VerifiedObjectives       int      `json:"verified_objectives"`
	SystemVerifiedCoverage   *float64 `json:"system_verified_coverage"`
	SystemVerifiedCoverageCI *CI      `json:"system_verified_coverage_ci,omitempty"`
	// Primary outcome (03 §5.2): posttest difference.
	MeanPostGuided   *float64 `json:"mean_post_guided"`
	MeanPostBaseline *float64 `json:"mean_post_baseline"`
	DeltaPost        *float64 `json:"delta_post"`
	DeltaPostCI      *CI      `json:"delta_post_ci,omitempty"`
	// Sample sizes.
	NUsersGuided   int `json:"n_users_guided"`
	NUsersBaseline int `json:"n_users_baseline"`
	// TuningSplit mirrors the input flag.
	TuningSplit bool `json:"tuning_split,omitempty"`
	// Per-state counts for the audit trail.
	StateCounts     map[string]int `json:"state_counts"`
	LeakageWarnings []string       `json:"leakage_warnings,omitempty"`
	OriginCounts    map[string]int `json:"origin_counts"`
}

// Assess validates the input, computes metrics, and reports leakage.
// It returns the metrics plus a list of validation errors (non-fatal
// issues are warnings; fatal protocol violations return an error).
func Assess(input AssessInput) (*AssessMetrics, error) {
	if input.TargetType != "" && input.TargetType != "objective" {
		return nil, fmt.Errorf("this group assessment evaluates legacy objectives; use learning_assessment.py describe for raw component states")
	}
	validOrigin := func(v string) bool {
		return v == OriginEngineering || v == OriginSynthetic || v == OriginExpert || v == OriginRealUser
	}
	if !validOrigin(input.DataOrigin) || strings.TrimSpace(input.FrozenRules) == "" {
		return nil, fmt.Errorf("explicit known data_origin and frozen_rules required")
	}
	m := &AssessMetrics{FrozenRules: input.FrozenRules, DataOrigin: input.DataOrigin, TuningSplit: input.TuningSplit,
		StateCounts: map[string]int{}, OriginCounts: map[string]int{}, Exclusions: map[string]string{},
		EnrolledByGroup: map[string]int{}, MissingByGroup: map[string]int{}, WithdrawnByGroup: map[string]int{},
		ProvenanceNote: "Origin tags are declarations, not proof of real participants; retain consent, source records and frozen artifact hashes separately."}
	users, snapshots := map[string]bool{}, map[string]bool{}
	// Each cluster is one participant: preserve within-user dependence in confidence intervals.
	clusters := map[string][][2]int{"verified": {}, "unverified": {}, "unknown": {}, "coverage": {}}
	var guided, baseline []float64
	coverageComplete := len(input.Participants) > 0
	for _, p := range input.Participants {
		if p.ParticipantID == "" || users[p.ParticipantID] {
			return nil, fmt.Errorf("missing or duplicate participant %q", p.ParticipantID)
		}
		users[p.ParticipantID] = true
		origin := p.DataOrigin
		if origin == "" {
			origin = input.DataOrigin
		}
		if origin != input.DataOrigin {
			return nil, fmt.Errorf("mixed data origins must be assessed separately")
		}
		if p.Group != "guided" && p.Group != "baseline" {
			return nil, fmt.Errorf("invalid group for %s", p.ParticipantID)
		}
		m.OriginCounts[origin]++
		m.EnrolledByGroup[p.Group]++
		if p.Withdrawn {
			m.WithdrawnByGroup[p.Group]++
		}
		if p.PosttestScore == nil || p.PosttestMax == nil || p.MissingPost || p.Withdrawn {
			m.MissingByGroup[p.Group]++
		} else {
			score, max := *p.PosttestScore, *p.PosttestMax
			if math.IsNaN(score) || math.IsInf(score, 0) || math.IsNaN(max) || math.IsInf(max, 0) || max <= 0 || score < 0 || score > max {
				return nil, fmt.Errorf("invalid posttest score for %s", p.ParticipantID)
			}
			score = 100 * score / max
			if p.Group == "guided" {
				guided = append(guided, score)
			} else {
				baseline = append(baseline, score)
			}
		}
		validState := func(st string) bool {
			switch st {
			case "verified", "unverified", "unknown", "partial", "conflicting", "stale", "stale_content":
				return true
			}
			return false
		}
		if len(p.GoalStatesBefore) == 0 {
			coverageComplete = false
		} else {
			v := 0
			for id, st := range p.GoalStatesBefore {
				if id == "" || !validState(st) {
					return nil, fmt.Errorf("invalid frozen goal state")
				}
				if st == "verified" {
					v++
				}
			}
			clusters["coverage"] = append(clusters["coverage"], [2]int{v, len(p.GoalStatesBefore)})
		}
		ordered := append([]AssessSnapshot(nil), p.Snapshots...)
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].PresentedAt.Equal(ordered[j].PresentedAt) {
				return ordered[i].SnapshotID < ordered[j].SnapshotID
			}
			return ordered[i].PresentedAt.Before(ordered[j].PresentedAt)
		})
		seenFamily, selected := map[string]bool{}, map[string]bool{}
		counts := map[string][2]int{}
		for _, s := range ordered {
			if s.SnapshotID == "" || snapshots[s.SnapshotID] || s.ParticipantID != p.ParticipantID || s.ObjectiveID == "" || s.FamilyID == "" || s.ItemID == "" || s.PolicyVersion == "" || !validState(s.StateBefore) {
				return nil, fmt.Errorf("invalid/duplicate snapshot %q", s.SnapshotID)
			}
			snapshots[s.SnapshotID] = true
			if s.StateAsOf.IsZero() || s.PresentedAt.IsZero() || s.StateAsOf.After(s.PresentedAt) || (s.Result != nil && (s.GradedAt.IsZero() || !s.GradedAt.After(s.PresentedAt))) {
				return nil, fmt.Errorf("snapshot %s requires state_as_of <= presented_at < graded_at", s.SnapshotID)
			}
			switch s.Phase {
			case PhaseTrain, PhasePractice, PhasePretest, PhasePosttest, PhaseDelayed:
			default:
				return nil, fmt.Errorf("invalid phase on %s", s.SnapshotID)
			}
			leaked := seenFamily[s.FamilyID]
			for _, f := range s.EvidenceFamiliesBefore {
				if f == s.FamilyID {
					leaked = true
				}
			}
			seenFamily[s.FamilyID] = true // even an unanswered/practice presentation exposes the family
			reason := ""
			switch {
			case leaked:
				reason = "family_overlap"
				m.LeakageWarnings = append(m.LeakageWarnings, "excluded "+s.SnapshotID+": family overlap")
			case s.Phase != PhasePosttest:
				reason = "not_primary_posttest"
			case !s.Eligible:
				reason = "ineligible:" + s.IneligibleReason
			case s.Result == nil:
				reason = "missing_result"
			case p.Withdrawn:
				reason = "withdrawn"
			case selected[s.ObjectiveID]:
				reason = "later_user_objective_trial"
			}
			if reason != "" {
				m.Exclusions[s.SnapshotID] = reason
				continue
			}
			selected[s.ObjectiveID] = true
			m.SelectedSnapshots = append(m.SelectedSnapshots, s.SnapshotID)
			m.StateCounts[s.StateBefore]++
			state := s.StateBefore
			if state != "verified" && state != "unverified" {
				state = "unknown"
			}
			c := counts[state]
			c[1]++
			if *s.Result {
				c[0]++
			}
			counts[state] = c
		}
		for st, c := range counts {
			clusters[st] = append(clusters[st], c)
		}
	}
	// Stable ordering is needed for bootstrap reproducibility regardless of input participant order.
	for st, cs := range clusters {
		sort.Slice(cs, func(i, j int) bool {
			if cs[i][0] == cs[j][0] {
				return cs[i][1] < cs[j][1]
			}
			return cs[i][0] < cs[j][0]
		})
		clusters[st] = cs
	}
	total := func(st string) (int, int) {
		p, n := 0, 0
		for _, c := range clusters[st] {
			p += c[0]
			n += c[1]
		}
		return p, n
	}
	rate := func(p, n int) *float64 {
		if n == 0 {
			return nil
		}
		v := float64(p) / float64(n)
		return &v
	}
	m.VerifiedPasses, m.VerifiedN = total("verified")
	m.VerifiedSuccessRate = rate(m.VerifiedPasses, m.VerifiedN)
	m.VerifiedFailureRate = rate(m.VerifiedN-m.VerifiedPasses, m.VerifiedN)
	if ci := clusterRateCI(clusters["verified"], input.RandomSeed); ci != nil {
		lo, hi := 1-*ci.Upper, 1-*ci.Lower
		m.VerifiedFailureCI = &CI{Lower: &lo, Upper: &hi, Method: ci.Method}
	}
	m.UnverifiedPasses, m.UnverifiedN = total("unverified")
	m.UnverifiedPassRate = rate(m.UnverifiedPasses, m.UnverifiedN)
	m.UnverifiedPassCI = clusterRateCI(clusters["unverified"], input.RandomSeed)
	m.UnknownPasses, m.UnknownN = total("unknown")
	m.UnknownPassRate = rate(m.UnknownPasses, m.UnknownN)
	m.UnknownPassCI = clusterRateCI(clusters["unknown"], input.RandomSeed)
	if coverageComplete {
		m.VerifiedObjectives, m.TotalObjectives = total("coverage")
		m.SystemVerifiedCoverage = rate(m.VerifiedObjectives, m.TotalObjectives)
		m.SystemVerifiedCoverageCI = clusterRateCI(clusters["coverage"], input.RandomSeed)
	}
	mean := func(v []float64) *float64 {
		if len(v) == 0 {
			return nil
		}
		sum := 0.0
		for _, x := range v {
			sum += x
		}
		r := sum / float64(len(v))
		return &r
	}
	sort.Float64s(guided)
	sort.Float64s(baseline)
	sort.Strings(m.SelectedSnapshots)
	sort.Strings(m.LeakageWarnings)
	m.NUsersGuided = len(guided)
	m.NUsersBaseline = len(baseline)
	m.MeanPostGuided = mean(guided)
	m.MeanPostBaseline = mean(baseline)
	if m.MeanPostGuided != nil && m.MeanPostBaseline != nil {
		d := *m.MeanPostGuided - *m.MeanPostBaseline
		m.DeltaPost = &d
		m.DeltaPostCI = BootstrapDeltaCI(guided, baseline, input.RandomSeed, 2000)
	}
	return m, nil
}

func clusterRateCI(clusters [][2]int, seed int64) *CI {
	if len(clusters) < 2 {
		return nil
	}
	rng := rand.New(rand.NewSource(seed))
	values := make([]float64, 2000)
	for i := range values {
		p, n := 0, 0
		for range clusters {
			c := clusters[rng.Intn(len(clusters))]
			p += c[0]
			n += c[1]
		}
		values[i] = float64(p) / float64(n)
	}
	sort.Float64s(values)
	lo, hi := values[49], values[1949]
	return &CI{Lower: &lo, Upper: &hi, Method: "bootstrap-participant-clusters"}
}

// FormatNA renders a *float64 as "N/A" when nil (验收2: 0 verified → N/A).
func FormatNA(v *float64) string {
	if v == nil {
		return "N/A"
	}
	return fmt.Sprintf("%.4f", *v)
}

// ValidateAndReport runs the assessment and prints the formal report
// following 03 §7/§8 templates.
func ValidateAndReport(input AssessInput) error {
	m, err := Assess(input)
	if err != nil {
		return fmt.Errorf("assessment protocol violation: %w", err)
	}

	fmt.Println("=== Independent Assessment Report ===")
	fmt.Printf("Frozen rules: %s\n", input.FrozenRules)
	fmt.Printf("Data origin: %s\n", input.DataOrigin)
	fmt.Printf("Origin breakdown: %+v\n", m.OriginCounts)

	if input.TuningSplit {
		fmt.Println("\n⚠⚠⚠ TUNING SPLIT — NOT FINAL EVALUATION ⚠⚠⚠")
		fmt.Println("  This input is marked tuning_split=true. Results here are for parameter")
		fmt.Println("  selection only. Reporting these as holdout results after seeing them")
		fmt.Println("  violates the frozen protocol (03 §1).")
	}

	fmt.Println("\n--- State Challenge Rates (per user-objective) ---")
	fmt.Printf("  Verified: n=%d passes=%d failure=%s", m.VerifiedN, m.VerifiedPasses, FormatNA(m.VerifiedFailureRate))
	if m.VerifiedFailureCI != nil {
		fmt.Printf(" CI95=[%.3f, %.3f]", *m.VerifiedFailureCI.Lower, *m.VerifiedFailureCI.Upper)
	}
	fmt.Println()
	fmt.Printf("  Unverified: n=%d passes=%d pass_rate=%s", m.UnverifiedN, m.UnverifiedPasses, FormatNA(m.UnverifiedPassRate))
	if m.UnverifiedPassCI != nil {
		fmt.Printf(" CI95=[%.3f, %.3f]", *m.UnverifiedPassCI.Lower, *m.UnverifiedPassCI.Upper)
	}
	fmt.Println()
	fmt.Printf("  Unknown: n=%d passes=%d pass_rate=%s", m.UnknownN, m.UnknownPasses, FormatNA(m.UnknownPassRate))
	if m.UnknownPassCI != nil {
		fmt.Printf(" CI95=[%.3f, %.3f]", *m.UnknownPassCI.Lower, *m.UnknownPassCI.Upper)
	}
	fmt.Println()
	fmt.Printf("  Coverage: verified=%d/%d=%s", m.VerifiedObjectives, m.TotalObjectives, FormatNA(m.SystemVerifiedCoverage))
	if m.SystemVerifiedCoverageCI != nil {
		fmt.Printf(" CI95=[%.3f, %.3f]", *m.SystemVerifiedCoverageCI.Lower, *m.SystemVerifiedCoverageCI.Upper)
	}
	fmt.Println()
	fmt.Printf("  State counts: %+v\n", m.StateCounts)

	fmt.Println("\n--- Primary Outcome (per user) ---")
	fmt.Printf("  Guided   n=%d mean_post=%s\n", m.NUsersGuided, FormatNA(m.MeanPostGuided))
	fmt.Printf("  Baseline n=%d mean_post=%s\n", m.NUsersBaseline, FormatNA(m.MeanPostBaseline))
	fmt.Printf("  Delta (guided - baseline): %s", FormatNA(m.DeltaPost))
	if m.DeltaPostCI != nil {
		fmt.Printf(" CI95=[%.2f, %.2f] (%s)", *m.DeltaPostCI.Lower, *m.DeltaPostCI.Upper, m.DeltaPostCI.Method)
	}
	fmt.Println()

	if len(m.LeakageWarnings) > 0 {
		fmt.Println("\n--- Leakage Warnings (detected and reported) ---")
		for _, w := range m.LeakageWarnings {
			fmt.Printf("  ⚠ %s\n", w)
		}
	} else {
		fmt.Println("\n  No leakage warnings detected.")
	}

	// Honest limitation statement (03 §8).
	fmt.Println("\n--- Interpretation ---")
	if input.DataOrigin != OriginRealUser {
		fmt.Printf("  Data origin is %s, NOT real_user. ", input.DataOrigin)
		fmt.Println("This is a demonstration of the protocol, not evidence of learning effectiveness.")
		fmt.Println("  No real-user learning outcomes are claimed.")
	}
	if m.VerifiedFailureRate == nil {
		fmt.Println("  Verified failure rate is N/A (0 verified objectives challenged). Cannot evaluate measurement quality.")
	}
	if m.NUsersGuided == 0 || m.NUsersBaseline == 0 {
		fmt.Println("  Primary outcome incomplete: one or both groups have 0 users with posttest data.")
	}

	return nil
}

// sortStringSlice is a deterministic helper for reproducible output.
func sortStringSlice(v []string) {
	sort.Strings(v)
}

// runAssess loads the input JSON and runs the assessment.
// WilsonInterval computes the Wilson score interval for a binomial
// proportion. Returns nil when n=0. z=1.96 for 95%.
func WilsonInterval(passes, total int) *CI {
	if total == 0 {
		return nil
	}
	const z = 1.96
	n := float64(total)
	p := float64(passes) / n
	z2 := z * z
	denom := 1 + z2/n
	centre := p + z2/(2*n)
	margin := z * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	lo := (centre - margin) / denom
	hi := (centre + margin) / denom
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return &CI{Lower: &lo, Upper: &hi, Method: "wilson"}
}

// BootstrapDeltaCI computes a user-level bootstrap CI for the guided-
// baseline posttest difference. Deterministic with the given seed.
func BootstrapDeltaCI(guided, baseline []float64, seed int64, iterations int) *CI {
	if len(guided) < 2 || len(baseline) < 2 {
		return nil
	}
	if iterations <= 0 {
		iterations = 2000
	}
	rng := rand.New(rand.NewSource(seed))
	deltas := make([]float64, iterations)
	for i := 0; i < iterations; i++ {
		deltas[i] = bootstrapMean(rng, guided) - bootstrapMean(rng, baseline)
	}
	sort.Float64s(deltas)
	lo := deltas[int(0.025*float64(iterations))]
	hi := deltas[int(0.975*float64(iterations))-1]
	return &CI{Lower: &lo, Upper: &hi, Method: "bootstrap-user"}
}

func bootstrapMean(rng *rand.Rand, data []float64) float64 {
	var sum float64
	for i := 0; i < len(data); i++ {
		sum += data[rng.Intn(len(data))]
	}
	return sum / float64(len(data))
}

func runAssess(path string) error {
	return runAssessOut(path, "")
}

// runAssessOut is runAssess with an optional output path for the
// persistent report JSON deliverable.
func runAssessOut(path, outPath string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var input AssessInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return fmt.Errorf("assess input parse: %w", err)
	}
	if len(input.Participants) == 0 {
		return fmt.Errorf("assess input needs ≥1 participant")
	}

	if err := ValidateAndReport(input); err != nil {
		return err
	}
	if outPath != "" {
		m, err := Assess(input)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return err
		}
		fmt.Printf("\nReport persisted to: %s\n", outPath)
	}
	return nil
}
