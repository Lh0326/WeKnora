package learning

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestRecallSM2IntervalsEarlyPracticeAndLapse(t *testing.T) {
	at := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	state := advanceRecall(recallState{}, "enroll", "v1", at)
	if !state.Active || !state.DueAt.Equal(at) || state.Ease != 2.5 {
		t.Fatal(state)
	}
	state = advanceRecall(state, "good", "v1", at)
	if state.Repetitions != 1 || state.IntervalDays != 1 || state.Ease != 2.5 {
		t.Fatal(state)
	}
	early := advanceRecall(state, "easy", "v1", at.Add(time.Hour))
	if !early.EarlyPractice || early.Repetitions != 1 || early.Ease != 2.5 || !early.DueAt.Equal(state.DueAt) {
		t.Fatal(early)
	}
	state = advanceRecall(early, "hard", "v1", early.DueAt)
	if state.IntervalDays != 6 || math.Abs(state.Ease-2.36) > 1e-9 {
		t.Fatal(state)
	}
	state = advanceRecall(state, "easy", "v1", state.DueAt)
	if state.IntervalDays != 15 || math.Abs(state.Ease-2.46) > 1e-9 {
		t.Fatal(state)
	}
	at = state.DueAt.Add(-time.Hour)
	state = advanceRecall(state, "again", "v1", at)
	if state.Repetitions != 0 || state.IntervalDays != 0 || !state.DueAt.Equal(at.Add(10*time.Minute)) {
		t.Fatal(state)
	}
	state = advanceRecall(state, "good", "v1", state.DueAt)
	if state.IntervalDays != 1 || state.Repetitions != 1 {
		t.Fatal(state)
	}
	for i := 0; i < 100; i++ {
		state = advanceRecall(state, "hard", "v1", state.DueAt)
	}
	if state.IntervalDays > 365 || state.Ease < 1.3 {
		t.Fatal(state)
	}
	for i := 0; i < 100; i++ {
		state = advanceRecall(state, "easy", "v1", state.DueAt)
	}
	if state.IntervalDays != 365 || math.IsInf(state.Ease, 0) {
		t.Fatal(state)
	}
}

func TestRecallPauseSourceChangeAndDeterministicReplay(t *testing.T) {
	at := time.Now().Add(-48 * time.Hour)
	actions := []string{"enroll", "good", "pause", "enroll"}
	events := []types.LearningEvent{}
	for i, action := range actions {
		data, _ := json.Marshal(reviewRecord{Policy: LegacyReviewPolicyVersion, Sequence: i + 1})
		events = append(events, types.LearningEvent{ID: uuid.NewString(), Slug: "a", Type: reviewActionTypes[action], ContentVersion: "v1", OccurredAt: at.Add(time.Duration(i) * time.Minute), ReviewData: data})
	}
	state := projectRecall(events)
	if !state.Active || state.Repetitions != 1 || state.IntervalDays != 1 {
		t.Fatal(state)
	}
	events = append(events, events[1])
	events[0], events[3] = events[3], events[0]
	if !reflect.DeepEqual(state, projectRecall(events)) {
		t.Fatal("replay order/duplicate changed schedule")
	}
	status := recallStatus(state, "v2", time.Now())
	if !status.ContentChanged || !status.Due || state.ContentVersion != "v1" {
		t.Fatal(status)
	}
	reset := advanceRecall(*state, "good", "v2", time.Now())
	if reset.Repetitions != 1 || reset.IntervalDays != 1 || reset.Ease != 2.5 {
		t.Fatal(reset)
	}
	paused := advanceRecall(reset, "pause", "v2", time.Now())
	if recallStatus(&paused, "v3", time.Now()).Due {
		t.Fatal("paused schedule became due")
	}
	if projectRecall([]types.LearningEvent{{Type: types.LearningEventNodeKnown}}) != nil {
		t.Fatal("known enrolled automatically")
	}
}

func recallInput(slug, action string, status *interfaces.LearningReviewStatus) interfaces.LearningReviewInput {
	in := interfaces.LearningReviewInput{Slug: slug, Action: action, RequestID: uuid.NewString()}
	if status != nil {
		in.Revision = status.Revision
		in.ContentVersion = status.ContentVersion
	}
	return in
}

func TestRecallDatabaseLoopIdempotencyScopeAndDeletion(t *testing.T) {
	s, db := auditDatabaseFixture(t)
	exerciseRecallDatabaseLoop(t, s, db)
}
func exerciseRecallDatabaseLoop(t *testing.T, s *Service, db *gorm.DB) {
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	input := recallInput(slug, "enroll", nil)
	enrolled, err := s.UpdateReviewSchedule(ctx, testKB, input)
	if err != nil || !enrolled.Due {
		t.Fatalf("enroll: %+v %v", enrolled, err)
	}
	again, err := s.UpdateReviewSchedule(ctx, testKB, input)
	if err != nil || again.Revision != enrolled.Revision {
		t.Fatalf("retry: %+v %v", again, err)
	}
	goodInput := recallInput(slug, "good", enrolled)
	good, err := s.UpdateReviewSchedule(ctx, testKB, goodInput)
	if err != nil || good.Due || good.PolicyVersion != ReviewPolicyVersion || time.Until(good.DueAt) < 9*time.Minute || time.Until(good.DueAt) > 11*time.Minute {
		t.Fatalf("good: %+v %v", good, err)
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "easy", enrolled)); !errors.Is(err, interfaces.ErrLearningReviewConflict) {
		t.Fatalf("stale browser: %v", err)
	}
	bad := goodInput
	bad.Action = "again"
	if _, err = s.UpdateReviewSchedule(ctx, testKB, bad); !errors.Is(err, ErrInvalidLearningRequest) {
		t.Fatalf("reused request: %v", err)
	}
	view, err := s.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range view.Nodes {
		if n.Slug == slug {
			found = true
			if n.State != "self_known" || n.Review == nil || n.Review.Revision != good.Revision || n.ObjectiveVerified != 0 {
				t.Fatal(n)
			}
		}
	}
	if !found {
		t.Fatal("missing node")
	}
	for _, other := range []context.Context{collectorCtx(1, "bob"), collectorCtx(2, "alice")} {
		if _, err = s.UpdateReviewSchedule(other, testKB, recallInput(slug, "good", good)); !errors.Is(err, interfaces.ErrLearningReviewConflict) {
			t.Fatalf("scope leaked: %v", err)
		}
	}
	lapse, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "again", good))
	if err != nil {
		t.Fatal(err)
	}
	view, _ = s.ObjectiveView(ctx, testKB)
	for _, n := range view.Nodes {
		if n.Slug == slug && n.State != "review" {
			t.Fatal(n)
		}
	}
	restored, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "good", lapse))
	if err != nil || restored.EarlyPractice || restored.PolicyVersion != ReviewPolicyVersion || restored.Due || !restored.DueAt.After(lapse.DueAt) {
		t.Fatalf("early re-learn: %+v %v", restored, err)
	}
	view, _ = s.ObjectiveView(ctx, testKB)
	for _, n := range view.Nodes {
		if n.Slug == slug && n.State != "self_known" {
			t.Fatal("positive feedback did not close queue", n)
		}
	}
	paused, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "pause", restored))
	if err != nil || paused.Active {
		t.Fatal(err, paused)
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "easy", paused)); !errors.Is(err, interfaces.ErrLearningReviewConflict) {
		t.Fatalf("paused review accepted: %v", err)
	}
	exported, err := s.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(exported)
	var facts []types.LearningEvent
	if err = db.Find(&facts).Error; err != nil {
		t.Fatal(err)
	}
	if len(facts) != 5 {
		t.Fatalf("duplicate writes: %d", len(facts))
	}
	for _, e := range facts {
		if e.Weight != 0 {
			t.Fatal("recall fabricated mastery weight")
		}
	}
	if !json.Valid(raw) || len(exported.Events) != 5 || len(exported.Events[0].ReviewData) == 0 {
		t.Fatal("export omitted recall history")
	}
	stale := s.CaptureCollectionContext(ctx)
	if err = s.DeleteProfile(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateReviewSchedule(stale, testKB, recallInput(slug, "enroll", nil)); !errors.Is(err, interfaces.ErrLearningEpochAdvanced) {
		t.Fatalf("in-flight resurrected: %v", err)
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "good", paused)); !errors.Is(err, interfaces.ErrLearningReviewConflict) {
		t.Fatalf("old browser resurrected: %v", err)
	}
	db.Find(&facts)
	var count int64
	db.Model(&types.LearningEvent{}).Count(&count)
	if count != 0 {
		t.Fatal("recall survived deletion")
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "enroll", nil)); err != nil {
		t.Fatal("explicit opt-in blocked by collection opt-out", err)
	}
}

func TestRecallDoesNotDemoteObjectiveEvidenceOnScheduleDue(t *testing.T) {
	page := &types.WikiPage{Slug: "a", Content: "content"}
	data, _ := json.Marshal(reviewRecord{Policy: ReviewPolicyVersion, Sequence: 1})
	events := []types.LearningEvent{{ID: "r1", Slug: "a", Type: types.LearningEventReviewEnroll, ContentVersion: nodeContentVersion(page), ReviewData: data, OccurredAt: time.Now().Add(-time.Hour)}}
	objectives := []interfaces.ObjectiveViewEntry{{Slug: "a", ObjectiveStatus: types.LearningObjectiveStatusPublished, State: types.ObjectiveStateVerified}}
	n := deriveLearningNodes([]*types.WikiPage{page}, objectives, events, nil)[0]
	if n.State != "verified" || n.Review == nil || !n.Review.Due || n.ObjectiveVerified != 1 {
		t.Fatal(n)
	}
}

func TestRecallSourceChangeCannotUseWarmNavigationCache(t *testing.T) {
	s, _ := auditDatabaseFixture(t)
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	enrolled, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "enroll", nil))
	if err != nil {
		t.Fatal(err)
	}
	s.ObjectiveView(ctx, testKB) // warm old content
	wiki := s.wikiRepo.(*stubWikiRepo)
	for key, pages := range wiki.pages {
		for i, p := range pages {
			if p.Slug == slug {
				replacement := *p
				replacement.Content = "new content"
				wiki.pages[key][i] = &replacement
			}
		}
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "good", enrolled)); !errors.Is(err, interfaces.ErrLearningReviewConflict) {
		t.Fatalf("cached source accepted: %v", err)
	}
	view, err := s.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	var changed *interfaces.LearningReviewStatus
	for _, n := range view.Nodes {
		if n.Slug == slug {
			changed = n.Review
		}
	}
	if changed == nil || !changed.ContentChanged || !changed.Due {
		t.Fatal("reload missed new material", changed)
	}
	saved, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "good", changed))
	if err != nil || saved.ContentChanged || saved.PolicyVersion != ReviewPolicyVersion || time.Until(saved.DueAt) < 9*time.Minute || time.Until(saved.DueAt) > 11*time.Minute || saved.Repetitions != 1 {
		t.Fatalf("new material did not restart series: %+v %v", saved, err)
	}
	wiki.addPage("another-kb", testWikiPage("concept/foreign", nil, nil))
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput("concept/foreign", "enroll", nil)); !errors.Is(err, ErrWikiReadTarget) {
		t.Fatalf("foreign page accepted: %v", err)
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, interfaces.LearningReviewInput{Slug: slug, Action: "enroll", RequestID: "invalid"}); !errors.Is(err, ErrInvalidLearningRequest) {
		t.Fatalf("bad request id: %v", err)
	}
}

func TestRecallAtomicRollbackAndConcurrentFeedback(t *testing.T) {
	s, db := auditDatabaseFixture(t)
	exerciseRecallAtomicFeedback(t, s, db)
}
func exerciseRecallAtomicFeedback(t *testing.T, s *Service, db *gorm.DB) {
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	initial, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "enroll", nil))
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback recall")
	err = s.repo.WithSubject(ctx, "web_user:alice", 0, false, func(tx context.Context) error {
		if _, err := s.UpdateReviewSchedule(tx, testKB, recallInput(slug, "good", initial)); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var skips, events int64
	db.Model(&types.LearningSkip{}).Count(&skips)
	db.Model(&types.LearningEvent{}).Count(&events)
	if skips != 0 || events != 1 {
		t.Fatalf("transaction escaped: skips=%d events=%d", skips, events)
	}
	gate := make(chan struct{})
	results := make(chan error, 2)
	for _, action := range []string{"good", "easy"} {
		in := recallInput(slug, action, initial)
		go func() { <-gate; _, err := s.UpdateReviewSchedule(ctx, testKB, in); results <- err }()
	}
	close(gate)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, interfaces.ErrLearningReviewConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("lost update: success=%d conflict=%d", success, conflict)
	}
	db.Model(&types.LearningEvent{}).Count(&events)
	if events != 2 {
		t.Fatalf("concurrent duplicate events: %d", events)
	}
}

func TestRecallServicePlanClosesForKnownNodeWithoutQuestionBank(t *testing.T) {
	s, db := auditDatabaseFixture(t)
	if err := db.AutoMigrate(&types.LearningEdge{}); err != nil {
		t.Fatal(err)
	}
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"
	if err := s.SetNodeState(ctx, testKB, slug, "known"); err != nil {
		t.Fatal(err)
	}
	enrolled, err := s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "enroll", nil))
	if err != nil {
		t.Fatal(err)
	}
	useMemory := false
	req := interfaces.ColdStartRequestPayload{GoalSlugs: []string{slug}, TimeBudgetMin: 5, UseMemory: &useMemory}
	plan, err := s.ShortPath(ctx, testKB, req)
	if err != nil || len(plan.Steps) != 1 || plan.Steps[0].Action != ActionRecall || plan.Steps[0].Slug != slug || plan.Steps[0].Reason.Detail == "" {
		view, viewErr := s.ObjectiveView(ctx, testKB)
		t.Fatalf("known node did not enter voluntary recall: %+v %v; enrolled=%+v; view=%+v %v", plan, err, enrolled, view.Nodes, viewErr)
	}
	if _, err = s.UpdateReviewSchedule(ctx, testKB, recallInput(slug, "good", enrolled)); err != nil {
		t.Fatal(err)
	}
	plan, err = s.ShortPath(ctx, testKB, req)
	if err != nil || len(plan.Steps) != 0 {
		t.Fatalf("completed recall repeated immediately: %+v %v", plan, err)
	}
}

func TestRecallPathWorksWithoutBankAndRespectsScopeBudgetAndExclusion(t *testing.T) {
	in := planInput{IncludeExploration: true, GoalsResolved: true, Pages: map[string]string{"a": "A", "b": "B", "c": "C"}, Skips: map[string]bool{"a": true, "b": true}, RecallDue: map[string]PathReason{"a": {Code: "plan_reason_scheduled_recall"}, "b": {Code: "plan_reason_scheduled_recall"}}, PageMinutes: map[string]int{"c": 1}, TimeBudgetMinutes: 5}
	plan := planShortPath(in)
	recalls, minutes := 0, 0
	for _, s := range plan.Steps {
		minutes += s.Minutes
		if s.Action == ActionRecall {
			recalls++
			if s.Objective != "" {
				t.Fatal("needs bank")
			}
		}
	}
	if recalls != 1 || minutes > 5 || len(plan.Steps) < 2 {
		t.Fatal(plan)
	}
	in.PageScope = map[string]bool{"b": true}
	plan = planShortPath(in)
	if len(plan.Steps) != 1 || plan.Steps[0].Slug != "b" {
		t.Fatal(plan)
	}
	in.ExcludedSlugs = map[string]bool{"b": true}
	if p := planShortPath(in); len(p.Steps) != 0 {
		t.Fatal(p)
	}
	in.ExcludedSlugs = nil
	in.TimeBudgetMinutes = 1
	if p := planShortPath(in); len(p.Steps) != 0 {
		t.Fatal(p)
	}
}

func TestFSRSStatusCountsShortStepRecallsWithoutLegacyEase(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	events := []types.LearningEvent{}
	for i, action := range []string{"enroll", "good", "hard", "again", "pause"} {
		data, _ := json.Marshal(reviewRecord{Policy: ReviewPolicyVersion, Sequence: i + 1})
		events = append(events, types.LearningEvent{ID: uuid.NewString(), Type: reviewActionTypes[action], ContentVersion: "v1", OccurredAt: at.Add(time.Duration(i) * time.Minute), ReviewData: data})
	}
	got := projectRecall(events)
	if got == nil || got.Active || got.Repetitions != 3 || got.Ease != 0 || got.PolicyVersion != ReviewPolicyVersion || got.EarlyPractice || !got.DueAt.After(at.Add(3*time.Minute)) {
		t.Fatalf("FSRS status retained SM-2 metadata or lost short-step reviews: %+v", got)
	}
	data, _ := json.Marshal(reviewRecord{Policy: ReviewPolicyVersion, Sequence: 6})
	events = append(events, types.LearningEvent{ID: uuid.NewString(), Type: types.LearningEventReviewEnroll, ContentVersion: "v2", OccurredAt: at.Add(time.Hour), ReviewData: data})
	got = projectRecall(events)
	if !got.Active || got.Repetitions != 0 || got.Ease != 0 || !got.DueAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("changed content inherited old recall evidence: %+v", got)
	}
}
