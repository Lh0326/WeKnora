package learning

// Self-assessment tests: the skills-matrix challenge track. The load-bearing
// scenario is the full gate loop — claim mastery, score lifted instantly,
// tier still capped, then two cross-gap quiz answers finally unlock the
// claimed band (exactly what the UI's "prove it" flow promises).

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func seedFold(t *testing.T, svc *Service, repo *stubLearningRepo, ctx context.Context, slug string, evs ...Event) {
	t.Helper()
	for _, ev := range evs {
		_ = repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: slug, Type: ev.Type, Weight: ev.Weight, OccurredAt: ev.OccurredAt,
		})
		svc.foldOne(ctx, newReadScope(1, "web_user:alice"), slug, ev)
	}
}

// TestSelfAssessUpLiftsScoreButGateCapsTier: "I know this" moves the frozen
// logit straight into the mastered band — visible in p_eff immediately —
// while the displayed tier stays capped at touched with the quiz-unlock
// hint. The system demands proof before it believes the claim.
func TestSelfAssessUpLiftsScoreButGateCapsTier(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	seedFold(t, svc, repo, ctx, slug,
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-49 * time.Hour)})

	if err := svc.RecordSelfAssess(ctx, testKB, slug, true, ""); err != nil {
		t.Fatal(err)
	}
	row, err := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
	if err != nil || row == nil {
		t.Fatalf("mastery read: %v %v", row, err)
	}
	state := StateFromModel(row)
	want := logitOf(LevelMasteredUp)
	if state.Logit < want-1e-9 {
		t.Fatalf("logit = %.4f, want lifted to ≥ %.4f (mastered band)", state.Logit, want)
	}
	lv := gatedAnchoredLevel(state, nil, time.Now())
	if lv.Level != LevelTouched {
		t.Fatalf("gated tier = %s, want touched — self-assessment must not unlock without quiz proof", lv.Level)
	}
	p := EffectiveP(state, time.Now())
	if p < LevelMasteredUp-0.02 {
		t.Fatalf("p_eff = %.3f, want ≈ mastered-band level", p)
	}
}

// TestSelfAssessUpIdempotentInWindow: repeating the same claim inside the
// re-ask window is a no-op — one lift, not a compounding one.
func TestSelfAssessUpIdempotentInWindow(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	if err := svc.RecordSelfAssess(ctx, testKB, slug, true, ""); err != nil {
		t.Fatal(err)
	}
	row1, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
	if err := svc.RecordSelfAssess(ctx, testKB, slug, true, ""); err != nil {
		t.Fatal(err)
	}
	row2, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
	if row2.Logit != row1.Logit {
		t.Fatalf("second up assessment moved logit %.4f → %.4f, want idempotent", row1.Logit, row2.Logit)
	}
}

// TestSelfAssessUpNoopWhenAlreadyInBand: claiming up on an already-mastered
// node changes nothing.
func TestSelfAssessUpNoopWhenAlreadyInBand(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	seedFold(t, svc, repo, ctx, slug,
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-50 * time.Hour)},
		Event{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: time.Now().Add(-49 * time.Hour)},
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-2 * time.Hour)})
	if err := svc.RecordSelfAssess(ctx, testKB, slug, true, ""); err != nil {
		t.Fatal(err)
	}
	row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
	// Two cites + one correct ≈ 1.0+1.0+2.2 = 4.2 → clamped to 4.0, already above target.
	if row.Logit != LogitCap {
		t.Fatalf("logit = %.4f, want clamp %.1f (assessment must be a no-op above band)", row.Logit, LogitCap)
	}
}

// TestSelfAssessDownReasons: the demotion set-points — "all" resets to the
// floor; the three partial reasons land exactly one band lower.
func TestSelfAssessDownReasons(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		reason     string
		setup      []Event
		facts      []DirectQuizFact
		wantTarget float64
	}{
		{
			name:   "all resets to floor",
			reason: "all",
			setup: []Event{
				{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-96 * time.Hour)},
				{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-48 * time.Hour)},
				{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-24 * time.Hour)},
			},
			facts: []DirectQuizFact{
				{ItemID: "q1", FirstCorrectAt: now.Add(-96 * time.Hour)},
				{ItemID: "q2", FirstCorrectAt: now.Add(-48 * time.Hour)},
			},
			wantTarget: LogitFloor,
		},
		{
			name:   "doc_gap demotes mastered one band",
			reason: "doc_gap",
			setup: []Event{
				{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-96 * time.Hour)},
				{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-48 * time.Hour)},
				{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-24 * time.Hour)},
			},
			facts: []DirectQuizFact{
				{ItemID: "q1", FirstCorrectAt: now.Add(-96 * time.Hour)},
				{ItemID: "q2", FirstCorrectAt: now.Add(-48 * time.Hour)},
			},
			wantTarget: logitOf(LevelFamiliarUp),
		},
		{
			name:   "doc_updated demotes familiar one band",
			reason: "doc_updated",
			setup: []Event{
				{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-48 * time.Hour)},
				{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: now.Add(-24 * time.Hour)},
			},
			facts:      []DirectQuizFact{{ItemID: "q1", FirstCorrectAt: now.Add(-48 * time.Hour)}},
			wantTarget: logitOf(LevelTouchedUp),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _ := readFixture(t)
			t.Setenv("LEARNING_ENABLE", "true")
			ctx := collectorCtx(1, "alice")
			const slug = "concept/rag"
			seedFold(t, svc, repo, ctx, slug, tc.setup...)
			// Seed the attempts the direct facts read from.
			for _, f := range tc.facts {
				_ = repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
					TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
					Slug: slug, QuizItemID: f.ItemID, IsCorrect: true, AnsweredAt: f.FirstCorrectAt,
				})
			}
			if err := svc.RecordSelfAssess(ctx, testKB, slug, false, tc.reason); err != nil {
				t.Fatal(err)
			}
			row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
			state := StateFromModel(row)
			if state.Logit > tc.wantTarget+1e-9 {
				t.Fatalf("logit = %.4f, want ≤ %.4f (target set-point)", state.Logit, tc.wantTarget)
			}
			if tc.reason == "all" && state.Logit != LogitFloor {
				t.Fatalf("all-reason logit = %.4f, want exactly the floor", state.Logit)
			}
		})
	}
}

// TestSelfAssessDownInvalidReason: unknown reasons are rejected outright.
func TestSelfAssessDownInvalidReason(t *testing.T) {
	svc, _, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := svc.RecordSelfAssess(ctx, testKB, "concept/rag", false, "vibes"); err == nil {
		t.Fatal("invalid reason must be rejected")
	}
}

// TestSelfAssessUpThenProofUnlocksMastered: the promised loop end-to-end —
// claim mastery, score lifted, two distinct cross-gap correct answers land,
// and only then does the gate let the mastered tier through.
func TestSelfAssessUpThenProofUnlocksMastered(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	if err := svc.RecordSelfAssess(ctx, testKB, slug, true, ""); err != nil {
		t.Fatal(err)
	}
	row, _ := repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
	state := StateFromModel(row)
	lv := gatedAnchoredLevel(state, nil, time.Now())
	if lv.Level == LevelMastered {
		t.Fatal("claim alone must not unlock mastered")
	}

	now := time.Now()
	// The two proof answers, spanning the session gap.
	for i, item := range []string{"q1", "q2"} {
		at := now.Add(time.Duration(-72+i*48) * time.Hour) // -72h then -24h
		_ = repo.InsertAttempt(ctx, &types.LearningQuizAttempt{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: slug, QuizItemID: item, IsCorrect: true, AnsweredAt: at,
		})
		seedFold(t, svc, repo, ctx, slug,
			Event{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: at})
	}
	row, _ = repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), slug)
	state = StateFromModel(row)
	facts := CollectDirectFacts([]types.LearningQuizAttempt{
		{Slug: slug, QuizItemID: "q1", IsCorrect: true, AnsweredAt: now.Add(-72 * time.Hour)},
		{Slug: slug, QuizItemID: "q2", IsCorrect: true, AnsweredAt: now.Add(-24 * time.Hour)},
	})
	if lv := gatedAnchoredLevel(state, facts[slug], time.Now()); lv.Level != LevelMastered {
		t.Fatalf("claim + two cross-gap proofs = %s, want mastered", lv.Level)
	}
}

// TestSelfAssessMarkExposedInView: the read side surfaces the latest mark
// (direction + reason-carrying event type) for hover display.
func TestSelfAssessMarkExposedInView(t *testing.T) {
	svc, _, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	if err := svc.RecordSelfAssess(ctx, testKB, slug, false, "quiz_easy"); err != nil {
		t.Fatal(err)
	}
	views, err := svc.ListMasteryView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Slug != slug {
			continue
		}
		if v.SelfAssess == nil || v.SelfAssess.Direction != "down" ||
			v.SelfAssess.EventType != types.LearningEventSelfAssessDownQuizEasy {
			t.Fatalf("self-assess mark = %+v, want down/quiz_easy", v.SelfAssess)
		}
		return
	}
	t.Fatalf("no view for %s", slug)
}

var _ = interfaces.SelfAssessVisibleWindow // keep the import honest for future assertions
