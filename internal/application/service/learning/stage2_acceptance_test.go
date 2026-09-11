package learning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---- Stage-2 acceptance tests: strict verification item bank and
// scoring boundaries ----

func reviewedObjFixture(repo *stubLearningRepo, id, slug string) error {
	o := objFixture(id, slug)
	return repo.UpsertObjective(context.Background(), &o)
}

// seedDraftItem stores an LLM-draft item bound to the fixture's live
// evidence, ready for review. The hash comes from the SAME live-evidence
// path the grader uses, so review-time validation passes by construction.
func seedDraftItem(t *testing.T, svc *Service, repo *stubLearningRepo, id, objective string) *types.LearningQuizItem {
	t.Helper()
	_, _, hash, err := svc.currentQuizEvidence(context.Background(), 1, testKB, "concept/rag")
	if err != nil || hash == "" {
		t.Fatalf("live evidence hash unavailable: %v %q", err, hash)
	}
	item := &types.LearningQuizItem{
		ID: id, TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "Q-" + id + "?", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "alpha", "B": "beta", "C": "gamma", "D": "delta"},
		Explanation: "from evidence", ChunkRefs: types.RefList{"c1"},
		Status:       types.LearningQuizStatusDraft,
		EvidenceHash: hash,
		ObjectiveID:  objective,
	}
	if err := repo.UpsertQuizItem(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

func approveItem(t *testing.T, svc *Service, ctx context.Context, id, objective string) *types.LearningQuizItem {
	t.Helper()
	item, err := svc.ReviewQuizItem(ctx, testKB, id, interfaces.ReviewDecisionInput{
		Decision: "approve", ObjectiveID: objective, ChangeKind: "typographic",
	})
	if err != nil {
		t.Fatalf("approve %s: %v", id, err)
	}
	return item
}

// 验收1：同题重试、同题族换ID不增加独立覆盖。
// （重试窗口在阶段1已按题族去重；此处端到端复核服务端冻结链路。）
func TestStage2_RetryAndCloneDoNotAddCoverage(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := reviewedObjFixture(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	a := seedDraftItem(t, svc, repo, "q1", "obj-1")
	// A reworded clone with a new item id and reshuffled options — same
	// correct-answer text, same fingerprint → review must merge families.
	clone := &types.LearningQuizItem{
		ID: "q1-clone", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "Reworded " + a.Question, CorrectKey: "C",
		Options:     types.QuizOptions{"A": "delta", "B": "beta", "C": "alpha", "D": "gamma"},
		Explanation: "clone", ChunkRefs: types.RefList{"c1"},
		Status:       types.LearningQuizStatusDraft,
		EvidenceHash: a.EvidenceHash, ObjectiveID: "obj-1",
	}
	if err := repo.UpsertQuizItem(ctx, clone); err != nil {
		t.Fatal(err)
	}
	pa := approveItem(t, svc, ctx, "q1", "obj-1")
	pc := approveItem(t, svc, ctx, "q1-clone", "obj-1")
	if pa.FamilyID != pc.FamilyID {
		t.Fatalf("clone must inherit the family: %q vs %q (fingerprint %q/%q)", pa.FamilyID, pc.FamilyID, pa.FamilyFingerprint, pc.FamilyFingerprint)
	}

	// Answer both clones correctly: still ONE strict family covered.
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1", "A", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1-clone", "C", ""); err != nil {
		t.Fatal(err)
	}
	view, err := svc.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	e := view.Entries[0]
	if e.State != types.ObjectiveStatePartial || len(e.Evidence.FamiliesPassed) != 1 {
		t.Fatalf("clone with a new item id must not add coverage: state=%q families=%v", e.State, e.Evidence.FamiliesPassed)
	}

	// Same-item retry inside the window: zero weight practice (frozen
	// verdict), no additional coverage.
	attempts, _ := repo.ListAttempts(ctx, scopeOf("alice"), "")
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if !attempts[0].Eligible || attempts[0].GradeReason != "eligible_independent" {
		t.Fatalf("first trial must be strict-eligible: %+v", attempts[0])
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1", "A", ""); err != nil {
		t.Fatal(err)
	}
	attempts, _ = repo.ListAttempts(ctx, scopeOf("alice"), "")
	last := attempts[len(attempts)-1]
	if last.Eligible || last.GradeReason != "feedback_retry" {
		t.Fatalf("same-item retry must record practice with reason, got %+v", last)
	}
}

// 验收2：看答案后答对记练习，不算新的独立能力证明。
func TestStage2_FeedbackExposedRetryIsPractice(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := reviewedObjFixture(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	seedDraftItem(t, svc, repo, "q1", "obj-1")
	b := seedDraftItem(t, svc, repo, "q2", "obj-1")
	b.Question = "Different family"
	b.Options = types.QuizOptions{"A": "zed", "B": "beta", "C": "gamma", "D": "delta"}
	if err := repo.UpsertQuizItem(ctx, b); err != nil {
		t.Fatal(err)
	}
	approveItem(t, svc, ctx, "q1", "obj-1")
	approveItem(t, svc, ctx, "q2", "obj-1")

	// First a WRONG answer (feedback reveals the key), then a correct
	// retry inside the window: practice, zero weight, not evidence.
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1", "B", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1", "A", ""); err != nil {
		t.Fatal(err)
	}
	view, _ := svc.ObjectiveView(ctx, testKB)
	e := view.Entries[0]
	if e.State != types.ObjectiveStateUnverified || e.Evidence.EligiblePasses != 0 {
		t.Fatalf("feedback-exposed retry must not verify: %+v", e.Evidence)
	}
	attempts, _ := repo.ListAttempts(ctx, scopeOf("alice"), "")
	if attempts[1].Eligible || attempts[1].GradeReason != "feedback_retry" {
		t.Fatalf("retry verdict must be frozen as practice: %+v", attempts[1])
	}
}

// 验收3：自助闭卷、开卷应用和助手辅助结果不混算。
func TestStage2_AssistanceModesNeverMix(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := reviewedObjFixture(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	seedDraftItem(t, svc, repo, "q1", "obj-1")
	seedDraftItem(t, svc, repo, "q2", "obj-1")
	repo.mu.Lock()
	repo.quizItems[1].Question = "Second family"
	repo.quizItems[1].Options = types.QuizOptions{"A": "omega", "B": "beta", "C": "gamma", "D": "delta"}
	repo.mu.Unlock()
	approveItem(t, svc, ctx, "q1", "obj-1")
	approveItem(t, svc, ctx, "q2", "obj-1")

	// Both answered correctly but under assistant_helped: recorded as
	// practice with the mode frozen; the strict contract stays unmet.
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1", "A", "assistant_helped"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q2", "A", "assistant_helped"); err != nil {
		t.Fatal(err)
	}
	attempts, _ := repo.ListAttempts(ctx, scopeOf("alice"), "")
	for _, a := range attempts {
		if a.Eligible || a.AssistanceMode != types.AssistanceAssistantHelp || a.GradeReason != "assistance_not_strict" {
			t.Fatalf("assistant-helped trial must be practice: %+v", a)
		}
	}
	view, _ := svc.ObjectiveView(ctx, testKB)
	if view.Entries[0].State != types.ObjectiveStateUnverified {
		t.Fatalf("assistant-helped trials must not verify: %+v", view.Entries[0].Evidence)
	}
	// Weight zeroed: practice never farms the legacy fold either.
	for _, ev := range repo.snapshotEvents() {
		if ev.Type == types.LearningEventQuizCorrect && ev.Weight != 0 {
			t.Fatalf("assistant-helped trial folded weight %v", ev.Weight)
		}
	}
}

// 验收4：无合法题库时显示暂无验证方式，不生成假题兜底。
func TestStage2_NoBankMeansNoFabrication(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := reviewedObjFixture(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	// Only a DRAFT item exists (unreviewed LLM output).
	seedDraftItem(t, svc, repo, "q1", "obj-1")

	questions, err := svc.TakeQuiz(ctx, testKB, "concept/rag")
	if err != nil {
		t.Fatal(err)
	}
	if len(questions) != 1 || questions[0].Mode != "practice" {
		t.Fatalf("draft must serve as labelled practice only: %+v", questions)
	}
	view, _ := svc.ObjectiveView(ctx, testKB)
	e := view.Entries[0]
	if e.State != types.ObjectiveStateUnverified {
		t.Fatalf("no strict verification available: %+v", e)
	}
	if len(repo.quizItems) != 1 {
		t.Fatalf("no fake fallback items may be minted: %d", len(repo.quizItems))
	}
}

// 验收5：题目引用完整但答案有歧义的例子被内容审核拒绝。
func TestStage2_AmbiguousAnswerRejectedInReview(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := reviewedObjFixture(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	// Fully grounded but ambiguous: two options say the same thing, so
	// either could be "the" answer.
	amb := seedDraftItem(t, svc, repo, "q-amb", "obj-1")
	amb.Options = types.QuizOptions{"A": "alpha", "B": "alpha", "C": "gamma", "D": "delta"}
	if err := repo.UpsertQuizItem(ctx, amb); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviewQuizItem(ctx, testKB, "q-amb", interfaces.ReviewDecisionInput{
		Decision: "approve", ObjectiveID: "obj-1",
	}); err == nil {
		t.Fatal("duplicate-valued options must fail review validation")
	}

	// Reviewer-side ambiguity rejection: the human records the decision;
	// the item goes disabled with the audit trail.
	rejected, err := svc.ReviewQuizItem(ctx, testKB, "q-amb", interfaces.ReviewDecisionInput{
		Decision: "reject", Reason: "ambiguous_answer", Note: "both A and B defensible",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != types.LearningQuizStatusDisabled || rejected.Reviewer == "" {
		t.Fatalf("rejection must disable with the audit trail: %+v", rejected)
	}
	// Machine principal cannot review (LLM self-approval forbidden).
	machineCtx := types.WithPrincipal(ctx, machinePrincipalForTest())
	if _, err := svc.ReviewQuizItem(machineCtx, testKB, "q-amb", interfaces.ReviewDecisionInput{Decision: "approve"}); err == nil {
		t.Fatal("machine principal must not review")
	}
}

// 验收6：新旧来源版本正确隔离，不使用失效题晋级。
func TestStage2_VersionIsolation(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := reviewedObjFixture(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	seedDraftItem(t, svc, repo, "q1", "obj-1")
	published := approveItem(t, svc, ctx, "q1", "obj-1")
	if published.ContentVersion == "" {
		t.Fatalf("published item must carry a content version")
	}

	// Evidence drift: the material changes; the item stops serving and
	// scoring immediately (no promotion through a stale item).
	svc.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "revised material"
	if qs, err := svc.TakeQuiz(ctx, testKB, "concept/rag"); err != nil || len(qs) != 0 {
		t.Fatalf("drifted item must not serve: %v %v", qs, err)
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "q1", "A", ""); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("drifted item must not score: %v", err)
	}

	// Semantic re-review bumps the content version: history keeps its
	// frozen version and reads stale in the derivation.
	svc.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "RAG evidence" // restore hash
	item, err := svc.ReviewQuizItem(ctx, testKB, "q1", interfaces.ReviewDecisionInput{
		Decision: "approve", ChangeKind: "semantic", Note: "answer scope widened",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.ContentVersion == published.ContentVersion {
		t.Fatalf("semantic change must bump the content version")
	}
	// Typographic change keeps the version.
	item2, err := svc.ReviewQuizItem(ctx, testKB, "q1", interfaces.ReviewDecisionInput{
		Decision: "approve", ChangeKind: "typographic", Note: "spacing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item2.ContentVersion != item.ContentVersion {
		t.Fatalf("typographic change must keep the version")
	}
}

// 验收7（任务链路）：结构化应用任务确定性判分 + 待答不泄答案键。
func TestStage2_TaskChain(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	taskObj := objFixture("obj-task", "concept/rag")
	taskObj.ContractType = types.ObjectiveContractTaskChecks
	if err := repo.UpsertObjective(context.Background(), &taskObj); err != nil {
		t.Fatal(err)
	}
	task := &types.LearningTask{
		ID: "task-1", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		ObjectiveID: "obj-task", Title: "检索方案配置", Scenario: "客服知识库需要混合检索",
		Fields:         types.JSONColumn(`[{"id":"retriever","label":"检索方式","type":"select","options":["bm25","vector","hybrid"]},{"id":"rerank","label":"重排","type":"select","options":["on","off"]}]`),
		AnswerKey:      map[string]string{"retriever": "hybrid", "rerank": "on"},
		CriticalChecks: types.JSONColumn(`[{"id":"retriever","description":"选择混合检索"},{"id":"rerank","description":"开启重排"}]`),
		ContentVersion: "v1",
	}
	if err := repo.UpsertTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// Draft task is not takeable; review publishes it.
	if ts, err := svc.TakeTask(ctx, testKB, "obj-task"); err != nil || len(ts) != 0 {
		t.Fatalf("draft task must not be takeable: %v %v", ts, err)
	}
	if _, err := svc.ReviewTask(ctx, testKB, "task-1", interfaces.ReviewDecisionInput{Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	takes, err := svc.TakeTask(ctx, testKB, "obj-task")
	if err != nil || len(takes) != 1 {
		t.Fatalf("published task takeable: %v %v", takes, err)
	}
	// The take payload must not carry the answer key: no answer_key
	// field, and the checks list only ids/descriptions. (Option VALUES
	// legitimately appear in Fields — they are the choices.)
	blob := string(takes[0].Fields) + string(takes[0].CriticalChecks) + takes[0].Scenario + takes[0].Title
	if strings.Contains(blob, "answer_key") || strings.Contains(blob, "AnswerKey") {
		t.Fatal("take payload carries the answer key")
	}
	if string(takes[0].Fields) == "" {
		t.Fatal("take payload missing fields")
	}

	// Grading: one field wrong → failed; all correct → passed → verified.
	res, err := svc.SubmitTaskAnswer(ctx, testKB, "task-1", map[string]string{"retriever": "bm25", "rerank": "on"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatal("partial answers must fail")
	}
	res, err = svc.SubmitTaskAnswer(ctx, testKB, "task-1", map[string]string{"retriever": "hybrid", "rerank": "on"}, "")
	if err != nil || res.Eligible || !res.Passed {
		t.Fatalf("feedback retry is practice: %+v %v", res, err)
	}
	// Separate the earlier failed observation from the new independent scenario (Windows clocks can share a tick).
	for i := range repo.taskAttempts {
		repo.taskAttempts[i].SubmittedAt = repo.taskAttempts[i].SubmittedAt.Add(-time.Second)
	}
	fresh := *task
	fresh.ID = "task-new"
	fresh.FamilyID = "independent-scenario"
	fresh.Status = types.LearningQuizStatusDraft
	if err := repo.UpsertTask(ctx, &fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviewTask(ctx, testKB, fresh.ID, interfaces.ReviewDecisionInput{Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	res, err = svc.SubmitTaskAnswer(ctx, testKB, fresh.ID, map[string]string{"retriever": "hybrid", "rerank": "on"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || !res.Eligible {
		t.Fatalf("correct submission must pass strictly: %+v", res)
	}
	view, _ := svc.ObjectiveView(ctx, testKB)
	for _, e := range view.Entries {
		if e.ObjectiveID == "obj-task" && e.State != types.ObjectiveStateVerified {
			t.Fatalf("task contract must verify on full pass: %+v", e)
		}
	}
	// Client-influenced is_passed is impossible by construction: the
	// submit API takes answers only. An assistant_helped submission
	// grades but never counts.
	res, err = svc.SubmitTaskAnswer(ctx, testKB, "task-1", map[string]string{"retriever": "hybrid", "rerank": "on"}, "assistant_helped")
	if err != nil {
		t.Fatal(err)
	}
	if res.Eligible || res.GradeReason != "assistance_not_strict" {
		t.Fatalf("assistant-helped task submission must be practice: %+v", res)
	}
	// Task attempts are personal data: export carries them.
	payload, err := svc.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.TaskAttempts) != 4 {
		t.Fatalf("task attempts exported: %d", len(payload.TaskAttempts))
	}
}

func scopeOf(subject string) interfaces.LearningScope {
	return interfaces.LearningScope{TenantID: 1, SubjectID: "web_user:" + subject, KnowledgeBaseID: testKB}
}

func machinePrincipalForTest() types.Principal {
	return types.Principal{Type: "api_external_user", ID: "test-machine"}
}
