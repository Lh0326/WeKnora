package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- Stage-5 acceptance: independent assessment protocol ----

func mkSnap(pid, obj, state string, family string, result *bool, asOf, presented time.Time) AssessSnapshot {
	return AssessSnapshot{
		SnapshotID: "snap-" + pid + "-" + obj + "-" + family, ParticipantID: pid, ObjectiveID: obj,
		StateBefore: state, EvidenceFamiliesBefore: []string{},
		PolicyVersion: "test-v1", StateAsOf: asOf,
		ItemID: "item-" + obj, FamilyID: family, Phase: PhasePosttest,
		PresentedAt: presented, Result: result, GradedAt: presented.Add(time.Minute),
		Eligible: true,
	}
}

func b(v bool) *bool { return &v }

func TestAssessmentDoesNotRelabelComponentChecksAsVerifiedObjectives(t *testing.T) {
	_, err := Assess(AssessInput{TargetType: "component", DataOrigin: OriginEngineering, FrozenRules: "fixture-v1"})
	if err == nil {
		t.Fatal("component input must use the descriptive report without legacy verification claims")
	}
	_, err = Assess(AssessInput{TargetType: "objective", DataOrigin: OriginEngineering, FrozenRules: "fixture-v1"})
	if err != nil {
		t.Fatalf("explicit legacy objective target rejected: %v", err)
	}
}

// 验收1：故意插入未来标签或重叠题族的测试输入能被拒绝或明确标出。
func TestStage5_FutureLabelRejected(t *testing.T) {
	future := mkSnap("p1", "obj-1", "verified", "fam-a", b(true),
		time.Now(), time.Now())
	// graded_at BEFORE state_as_of = future label.
	future.StateAsOf = time.Now().Add(time.Hour)
	future.PresentedAt = time.Now()
	future.GradedAt = time.Now().Add(time.Minute)

	input := AssessInput{
		Participants: []AssessParticipant{{
			ParticipantID: "p1", Group: "guided", DataOrigin: OriginSynthetic,
			Snapshots: []AssessSnapshot{future},
		}},
		FrozenRules: "test", DataOrigin: OriginSynthetic,
	}
	_, err := Assess(input)
	if err == nil {
		t.Fatal("future label (graded_at < state_as_of) must be rejected")
	}
}

// 验收1b：重叠题族被检测并报告为泄漏警告。
func TestStage5_FamilyOverlapWarned(t *testing.T) {
	now := time.Now()
	// The holdout family "fam-a" also appears in the frozen evidence.
	snap := mkSnap("p1", "obj-1", "verified", "fam-a", b(true), now.Add(-time.Hour), now)
	snap.EvidenceFamiliesBefore = []string{"fam-train", "fam-a"}

	input := AssessInput{
		Participants: []AssessParticipant{{
			ParticipantID: "p1", Group: "guided", DataOrigin: OriginSynthetic,
			Snapshots: []AssessSnapshot{snap},
		}},
		FrozenRules: "test", DataOrigin: OriginSynthetic,
	}
	m, err := Assess(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.LeakageWarnings) == 0 {
		t.Fatal("family overlap must be detected and warned")
	}
	found := false
	for _, w := range m.LeakageWarnings {
		if len(w) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("leakage warning content must be non-empty")
	}
}

// 验收2：0 个 verified 时失败率为 N/A，而非 0%。
func TestStage5_ZeroVerifiedIsNA(t *testing.T) {
	now := time.Now()
	input := AssessInput{
		Participants: []AssessParticipant{{
			ParticipantID: "p1", Group: "guided", DataOrigin: OriginSynthetic,
			Snapshots: []AssessSnapshot{
				mkSnap("p1", "obj-1", "unverified", "fam-a", b(true), now.Add(-time.Hour), now),
				mkSnap("p1", "obj-2", "unverified", "fam-b", b(false), now.Add(-time.Hour), now),
			},
		}},
		FrozenRules: "test", DataOrigin: OriginSynthetic,
	}
	m, err := Assess(input)
	if err != nil {
		t.Fatal(err)
	}
	if m.VerifiedFailureRate != nil {
		t.Fatalf("0 verified challenges → failure rate must be nil (N/A), got %v", *m.VerifiedFailureRate)
	}
	if FormatNA(m.VerifiedFailureRate) != "N/A" {
		t.Fatalf("FormatNA(nil) must print N/A, got %q", FormatNA(m.VerifiedFailureRate))
	}
}

// 验收3：相同用户多次试次不会被当作多个独立用户。
func TestStage5_UserLevelAggregation(t *testing.T) {
	now := time.Now()
	score := 80.0
	max := 100.0
	input := AssessInput{
		Participants: []AssessParticipant{
			{
				ParticipantID: "user-1", Group: "guided", DataOrigin: OriginSynthetic,
				PosttestScore: &score, PosttestMax: &max,
				// 3 challenges of the SAME user-objective: counted once.
				Snapshots: []AssessSnapshot{
					mkSnap("user-1", "obj-1", "verified", "fam-a", b(true), now.Add(-3*time.Hour), now.Add(-2*time.Hour)),
					mkSnap("user-1", "obj-1", "verified", "fam-b", b(true), now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
					mkSnap("user-1", "obj-1", "verified", "fam-c", b(true), now.Add(-1*time.Hour), now),
				},
			},
		},
		FrozenRules: "test", DataOrigin: OriginSynthetic,
	}
	m, err := Assess(input)
	if err != nil {
		t.Fatal(err)
	}
	if m.NUsersGuided != 1 {
		t.Fatalf("3 trials of one user must count as 1 user, got %d", m.NUsersGuided)
	}
}

// 验收4：报告的每个结果可追到记录和冻结规则。
func TestStage5_Traceability(t *testing.T) {
	now := time.Now()
	input := AssessInput{
		Participants: []AssessParticipant{{
			ParticipantID: "trace-1", Group: "guided", DataOrigin: OriginSynthetic,
			Snapshots: []AssessSnapshot{mkSnap("trace-1", "obj-1", "verified", "fam-a", b(false), now.Add(-time.Hour), now)},
		}},
		FrozenRules: "rules-v42", DataOrigin: OriginSynthetic,
	}
	m, err := Assess(input)
	if err != nil {
		t.Fatal(err)
	}
	// The frozen rules version is carried in the input and should appear in the output.
	if m.FrozenRules != "rules-v42" {
		t.Fatal("frozen rules must be preserved in the input")
	}
	// State counts are traceable to the snapshot entries.
	if m.StateCounts["verified"] != 1 {
		t.Fatalf("state count for verified should be 1, got %d", m.StateCounts["verified"])
	}
	// The origin breakdown is traceable.
	if m.OriginCounts[OriginSynthetic] != 1 {
		t.Fatalf("origin count for synthetic should be 1, got %d", m.OriginCounts[OriginSynthetic])
	}
}

// 验收5：相同输入与环境版本可以重跑得到一致计数与结果。
func TestStage5_DeterministicAssessment(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	score1, max1 := 85.0, 100.0
	score2, max2 := 70.0, 100.0
	input := AssessInput{
		Participants: []AssessParticipant{
			{ParticipantID: "u1", Group: "guided", DataOrigin: OriginSynthetic,
				PosttestScore: &score1, PosttestMax: &max1,
				Snapshots: []AssessSnapshot{
					mkSnap("u1", "obj-1", "verified", "fam-a", b(true), now.Add(-time.Hour), now),
					mkSnap("u1", "obj-2", "unverified", "fam-b", b(false), now.Add(-time.Hour), now),
				}},
			{ParticipantID: "u2", Group: "baseline", DataOrigin: OriginSynthetic,
				PosttestScore: &score2, PosttestMax: &max2,
				Snapshots: []AssessSnapshot{
					mkSnap("u2", "obj-1", "verified", "fam-c", b(true), now.Add(-time.Hour), now),
				}},
		},
		FrozenRules: "det-v1", DataOrigin: OriginSynthetic,
	}
	a, err1 := Assess(input)
	b2, err2 := Assess(input)
	if err1 != nil || err2 != nil {
		t.Fatalf("assess errors: %v %v", err1, err2)
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b2)
	if string(aj) != string(bj) {
		t.Fatalf("same input must produce identical JSON output:\n%s\nvs\n%s", aj, bj)
	}
}

// 验收4b：缺失后测保留在计数中，不删除失败者。
func TestStage5_MissingPostPreserved(t *testing.T) {
	now := time.Now()
	goodScore, goodMax := 90.0, 100.0
	input := AssessInput{
		Participants: []AssessParticipant{
			{ParticipantID: "completer", Group: "guided", DataOrigin: OriginSynthetic,
				PosttestScore: &goodScore, PosttestMax: &goodMax},
			{ParticipantID: "dropped", Group: "guided", DataOrigin: OriginSynthetic,
				Withdrawn: true, MissingPost: true, MissingReason: "withdrew_midway",
				Snapshots: []AssessSnapshot{
					mkSnap("dropped", "obj-1", "verified", "fam-a", nil, now.Add(-time.Hour), now),
				}},
		},
		FrozenRules: "test", DataOrigin: OriginSynthetic,
	}
	m, err := Assess(input)
	if err != nil {
		t.Fatal(err)
	}
	// Only 1 user has posttest data; the dropped user is NOT deleted from the
	// input or the origin counts.
	if m.NUsersGuided != 1 {
		t.Fatalf("only 1 user with posttest data expected, got %d", m.NUsersGuided)
	}
	if m.OriginCounts[OriginSynthetic] != 2 {
		t.Fatalf("both participants must be in origin counts, got %d", m.OriginCounts[OriginSynthetic])
	}
}

// 验收：数据来源不冒充 — synthetic 不会自动变成 real_user。
func TestStage5_OriginNotUpgraded(t *testing.T) {
	now := time.Now()
	input := AssessInput{
		Participants: []AssessParticipant{{
			ParticipantID: "synth-1", Group: "guided", DataOrigin: OriginSynthetic,
			Snapshots: []AssessSnapshot{mkSnap("synth-1", "obj-1", "verified", "fam-a", b(true), now.Add(-time.Hour), now)},
		}},
		FrozenRules: "test", DataOrigin: OriginSynthetic,
	}
	m, err := Assess(input)
	if err != nil {
		t.Fatal(err)
	}
	if m.OriginCounts[OriginRealUser] > 0 {
		t.Fatal("synthetic data must never be counted as real_user")
	}
	if m.OriginCounts[OriginSynthetic] != 1 {
		t.Fatalf("origin should be synthetic, got %+v", m.OriginCounts)
	}
}

// 验收：端到端命令行运行（合成演示输入→报告输出）。
func TestStage5_RunAssessEndToEnd(t *testing.T) {
	now := time.Now()
	pass, fail := true, false
	score, max := 75.0, 100.0
	input := AssessInput{
		Participants: []AssessParticipant{
			{ParticipantID: "g1", Group: "guided", DataOrigin: OriginSynthetic,
				PosttestScore: &score, PosttestMax: &max,
				Snapshots: []AssessSnapshot{
					{SnapshotID: "s1", ParticipantID: "g1", ObjectiveID: "obj-1",
						StateBefore: "verified", EvidenceFamiliesBefore: []string{"f-train"},
						PolicyVersion: "v1", StateAsOf: now.Add(-2 * time.Hour),
						ItemID: "i1", FamilyID: "f-hold", Phase: PhasePosttest,
						PresentedAt: now.Add(-time.Hour), Result: &pass, GradedAt: now.Add(-50 * time.Minute), Eligible: true},
					{SnapshotID: "s2", ParticipantID: "g1", ObjectiveID: "obj-2",
						StateBefore: "unverified", EvidenceFamiliesBefore: []string{},
						PolicyVersion: "v1", StateAsOf: now.Add(-2 * time.Hour),
						ItemID: "i2", FamilyID: "f-hold2", Phase: PhasePosttest,
						PresentedAt: now.Add(-time.Hour), Result: &fail, GradedAt: now.Add(-50 * time.Minute), Eligible: true},
				}},
		},
		FrozenRules: "demo-v1", DataOrigin: OriginSynthetic,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "assess-in.json")
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := runAssess(tmp); err != nil {
		t.Fatalf("runAssess end-to-end: %v", err)
	}
}
