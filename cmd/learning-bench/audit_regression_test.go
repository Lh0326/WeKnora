package main

import (
	"testing"
	"time"
)

func auditAssessFixture() AssessInput {
	now := time.Now().UTC()
	yes := true
	return AssessInput{DataOrigin: OriginSynthetic, FrozenRules: "v1", Participants: []AssessParticipant{{ParticipantID: "u", Group: "guided", DataOrigin: OriginSynthetic, Snapshots: []AssessSnapshot{{SnapshotID: "s", ParticipantID: "u", ObjectiveID: "o", StateBefore: "verified", PolicyVersion: "v1", StateAsOf: now, PresentedAt: now.Add(time.Second), GradedAt: now.Add(2 * time.Second), FamilyID: "f", ItemID: "i", Phase: PhasePosttest, Result: &yes, Eligible: true}}}}}
}
func TestAuditAssessRejectsLabelBeforePresentation(t *testing.T) {
	in := auditAssessFixture()
	in.Participants[0].Snapshots[0].GradedAt = in.Participants[0].Snapshots[0].StateAsOf.Add(time.Millisecond)
	if _, err := Assess(in); err == nil {
		t.Fatal("label before presentation accepted")
	}
}
func TestAuditAssessIneligibleDoesNotEnterRates(t *testing.T) {
	in := auditAssessFixture()
	in.Participants[0].Snapshots[0].Eligible = false
	m, err := Assess(in)
	if err != nil {
		t.Fatal(err)
	}
	if m.VerifiedN != 0 {
		t.Fatal("ineligible challenge included")
	}
}
func TestAuditAssessRejectsMixedOrigins(t *testing.T) {
	in := auditAssessFixture()
	in.Participants[0].DataOrigin = OriginRealUser
	if _, err := Assess(in); err == nil {
		t.Fatal("mixed synthetic/real origin accepted in pooled metrics")
	}
}
func TestAuditAssessRejectsDuplicateParticipants(t *testing.T) {
	in := auditAssessFixture()
	in.Participants = append(in.Participants, in.Participants[0])
	if _, err := Assess(in); err == nil {
		t.Fatal("duplicate participant accepted")
	}
}

func TestAuditAssessFirstTrialKeepsItsOwnState(t *testing.T) {
	in := auditAssessFixture()
	first := in.Participants[0].Snapshots[0]
	no := false
	first.Result = &no
	later := first
	later.SnapshotID = "later"
	later.FamilyID = "new-family"
	later.StateBefore = "unverified"
	yes := true
	later.Result = &yes
	later.StateAsOf = later.StateAsOf.Add(time.Hour)
	later.PresentedAt = later.PresentedAt.Add(time.Hour)
	later.GradedAt = later.GradedAt.Add(time.Hour)
	in.Participants[0].Snapshots = []AssessSnapshot{later, first}
	m, e := Assess(in)
	if e != nil {
		t.Fatal(e)
	}
	if m.VerifiedN != 1 || m.VerifiedPasses != 0 || m.UnverifiedN != 0 || len(m.SelectedSnapshots) != 1 || m.SelectedSnapshots[0] != first.SnapshotID {
		t.Fatalf("mixed state/result or selected later trial: %+v", m)
	}
}
func TestAuditAssessCoverageUsesUnchallengedGoals(t *testing.T) {
	in := auditAssessFixture()
	m, e := Assess(in)
	if e != nil {
		t.Fatal(e)
	}
	if m.SystemVerifiedCoverage != nil {
		t.Fatal("challenge sample cannot define library coverage")
	}
	in.Participants[0].GoalStatesBefore = map[string]string{"o": "verified", "untested": "unverified"}
	m, e = Assess(in)
	if e != nil {
		t.Fatal(e)
	}
	if m.TotalObjectives != 2 || m.VerifiedObjectives != 1 || *m.SystemVerifiedCoverage != 0.5 {
		t.Fatalf("wrong full denominator: %+v", m)
	}
	if m.VerifiedFailureCI != nil || m.SystemVerifiedCoverageCI != nil {
		t.Fatal("one participant cannot supply between-user uncertainty")
	}
}
func TestAuditAssessExposureInOtherPhaseExcluded(t *testing.T) {
	in := auditAssessFixture()
	pre := in.Participants[0].Snapshots[0]
	pre.SnapshotID = "pre"
	pre.ObjectiveID = "another-objective"
	pre.Phase = PhasePretest
	pre.StateAsOf = pre.StateAsOf.Add(-time.Hour)
	pre.PresentedAt = pre.PresentedAt.Add(-time.Hour)
	pre.GradedAt = pre.GradedAt.Add(-time.Hour)
	in.Participants[0].Snapshots = append(in.Participants[0].Snapshots, pre)
	m, e := Assess(in)
	if e != nil {
		t.Fatal(e)
	}
	if m.VerifiedN != 0 || len(m.LeakageWarnings) != 1 {
		t.Fatalf("cross-phase exposure counted: %+v", m)
	}
}
func TestAuditAssessInvalidIdentityAndScores(t *testing.T) {
	for _, kind := range []string{"identity", "score", "group", "zero_time"} {
		t.Run(kind, func(t *testing.T) {
			in := auditAssessFixture()
			switch kind {
			case "identity":
				in.Participants[0].Snapshots[0].ParticipantID = "someone-else"
			case "score":
				score, max := 101.0, 100.0
				in.Participants[0].PosttestScore = &score
				in.Participants[0].PosttestMax = &max
			case "group":
				in.Participants[0].Group = "anything"
			case "zero_time":
				in.Participants[0].Snapshots[0].StateAsOf = time.Time{}
			}
			if _, e := Assess(in); e == nil {
				t.Fatal("invalid protocol accepted")
			}
		})
	}
}
