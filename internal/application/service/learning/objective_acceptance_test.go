package learning

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---- Stage-1 acceptance tests: evidence/profile separation ----
//
// The seven acceptance criteria of 阶段1, pinned as regressions. Pure
// derivation cases exercise DeriveObjectiveState directly; behavioural
// cases (reads, agent reads, self-assessment) run through the service so
// the FULL collection path is covered, not just the projection.

func objFixture(id, slug string) types.LearningObjective {
	publishedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return types.LearningObjective{
		ID: id, TenantID: 1, KnowledgeBaseID: testKB, Slug: slug,
		Title: "目标" + id, Behavior: "can do X",
		CapabilityType: "concept", ContractType: types.ObjectiveContractConceptTwoFamily,
		ContractVersion: types.ObjectiveContractVersion,
		EvidenceHash:    quizEvidenceHash(testWikiPage(slug, []string{"c1"}, []string{"d1|Doc"}), map[string]string{"c1": "RAG evidence"}), Reviewer: "web_user:test-reviewer",
		ContentVersion: "v1", Status: types.LearningObjectiveStatusPublished, PublishedAt: &publishedAt,
	}
}

func ptrAttempt(a types.LearningQuizAttempt) *types.LearningQuizAttempt { return &a }

func upsertObj(repo *stubLearningRepo, id, slug string) error {
	o := objFixture(id, slug)
	return repo.UpsertObjective(context.Background(), &o)
}

func attemptFixture(id, objective, family string, correct bool, at time.Time) types.LearningQuizAttempt {
	return types.LearningQuizAttempt{
		ID: id, TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		QuizItemID: "q-" + family, Slug: "concept/rag",
		ObjectiveID: objective, FamilyID: family, ContentVersion: "v1",
		AssistanceMode: types.AssistanceClosedBook, ItemStatus: types.LearningQuizStatusPublished,
		Eligible: true, GradeReason: "eligible_independent",
		ContractVersion: types.ObjectiveContractVersion, RubricVersion: "mcq-rubric-v1", ScorerVersion: types.MCQScorerVersion,
		ChosenKey: "A", IsCorrect: correct, AnsweredAt: at,
	}
}

// 验收1：100 次阅读与 100 次 Agent 读取不产生已验证目标（暴露与能力分离）。
// 验收2：“我已掌握”只产生自述，不填 verified。
func TestStage1_ReadsAndSelfReportNeverVerify(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	if err := upsertObj(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}

	// 100 human reads (spaced past the dedup window to all count) + 100
	// agent reads + a self-assessment "up" (更熟).
	base := time.Now().Add(-500 * time.Hour)
	for i := 0; i < 100; i++ {
		at := base.Add(time.Duration(i) * 3 * time.Hour)
		if err := repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: "concept/rag", Type: types.LearningEventWikiToolRead, Weight: 0.5, OccurredAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.AppendEvent(ctx, &types.LearningEvent{
			TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			Slug: "concept/rag", Type: types.LearningEventAgentRead, Weight: 0, OccurredAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.AppendEvent(ctx, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventSelfAssessUp, Weight: 1.5, OccurredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	view, err := svc.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(view.Entries))
	}
	e := view.Entries[0]
	if e.State != types.ObjectiveStateUnverified {
		t.Fatalf("100 reads + 100 agent reads + self-report must leave the objective unverified, got %q", e.State)
	}
	if !e.Evidence.Unknown {
		t.Fatalf("no attempt facts: evidence must read unknown, got %+v", e.Evidence)
	}
	if e.Exposure.Reads != 100 || e.Exposure.AgentReads != 100 {
		t.Fatalf("exposure counts = reads %d / agent %d, want 100/100 (exposure IS recorded, in its own dimension)", e.Exposure.Reads, e.Exposure.AgentReads)
	}
	if e.SelfReport.Direction != "up" {
		t.Fatalf("self-report must be visible as a claim, got %+v", e.SelfReport)
	}
	if e.PathStatus != "challenge_pending" {
		t.Fatalf("an up claim with no proof reads challenge_pending, got %q", e.PathStatus)
	}
}

// 验收3：没有阅读历史但挑战通过，可以获得对应证据。
func TestStage1_NoHistoryChallengeStillEarnsEvidence(t *testing.T) {
	svc, repo, _ := readFixture(t)
	ctx := collectorCtx(1, "alice")

	obj := objFixture("obj-1", "concept/rag")
	if err := repo.UpsertObjective(ctx, &obj); err != nil {
		t.Fatal(err)
	}
	// Two different families answered correctly — NO reads, NO cites, NO
	// self-assessment whatsoever.
	for i, fam := range []string{"fam-a", "fam-b"} {
		a := attemptFixture("a"+fam, "obj-1", fam, true, time.Now().Add(time.Duration(-i)*time.Hour))
		if err := repo.InsertAttempt(ctx, &a); err != nil {
			t.Fatal(err)
		}
	}

	view, err := svc.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	e := view.Entries[0]
	if e.State != types.ObjectiveStateVerified {
		t.Fatalf("challenge without prior exposure must verify, got %q", e.State)
	}
	if e.Exposure.Reads != 0 && e.Exposure.Cites != 0 {
		t.Fatalf("exposure should be zero in this scenario, got %+v", e.Exposure)
	}
	if e.Evidence.Unknown || len(e.Evidence.FamiliesPassed) != 2 {
		t.Fatalf("evidence must show two passing families, got %+v", e.Evidence)
	}
}

// 验收4：一个概念题族通过为 partial，两个合格题族满足契约才 verified。
// 同题族换 item_id 不增加独立覆盖（题族独立性）。
func TestStage1_FamilyContractAndCloneIndependence(t *testing.T) {
	obj := objFixture("obj-1", "concept/rag")
	base := time.Now().Add(-48 * time.Hour)

	one := DeriveObjectiveState(obj, []types.LearningQuizAttempt{
		attemptFixture("a1", "obj-1", "fam-a", true, base),
	}, nil, nil)
	if one.State != types.ObjectiveStatePartial {
		t.Fatalf("one family = partial, got %q", one.State)
	}

	// Same family, different item id and a LATER independent sitting:
	// still one family — clones never add independent coverage.
	two := DeriveObjectiveState(obj, []types.LearningQuizAttempt{
		attemptFixture("a1", "obj-1", "fam-a", true, base),
		attemptFixture("a2-clone", "obj-1", "fam-a", true, base.Add(72*time.Hour)),
	}, nil, nil)
	if two.State != types.ObjectiveStatePartial {
		t.Fatalf("clone with a new item id must stay partial, got %q", two.State)
	}
	if len(two.Evidence.FamiliesPassed) != 1 {
		t.Fatalf("families = %v, want exactly fam-a", two.Evidence.FamiliesPassed)
	}

	three := DeriveObjectiveState(obj, []types.LearningQuizAttempt{
		attemptFixture("a1", "obj-1", "fam-a", true, base),
		attemptFixture("a2", "obj-1", "fam-b", true, base.Add(time.Hour)),
	}, nil, nil)
	if three.State != types.ObjectiveStateVerified {
		t.Fatalf("two distinct families = verified, got %q", three.State)
	}
	if !three.Evidence.ContractMetAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("contract met at the second family's first pass, got %v", three.Evidence.ContractMetAt)
	}
}

// 验收5：新有效失败产生局部冲突，不清空无关目标。
func TestStage1_LaterFailureConflictsLocally(t *testing.T) {
	objA := objFixture("obj-1", "concept/rag")
	objB := objFixture("obj-2", "concept/decay")
	base := time.Now().Add(-96 * time.Hour)
	attempts := []types.LearningQuizAttempt{
		attemptFixture("a1", "obj-1", "fam-a", true, base),
		attemptFixture("a2", "obj-1", "fam-b", true, base.Add(time.Hour)),
		// New independent failure AFTER the contract was met.
		attemptFixture("a3", "obj-1", "fam-c", false, base.Add(72*time.Hour)),
		// An unrelated objective keeps its clean verification.
		attemptFixture("b1", "obj-2", "fam-x", true, base),
		attemptFixture("b2", "obj-2", "fam-y", true, base.Add(time.Hour)),
	}
	dA := DeriveObjectiveState(objA, attempts, nil, nil)
	if dA.State != types.ObjectiveStateConflicting {
		t.Fatalf("later valid failure after verification = conflicting, got %q", dA.State)
	}
	if len(dA.Evidence.FamiliesPassedHistoric) < 2 {
		t.Fatalf("conflicting must preserve passing history, got %+v", dA.Evidence)
	}
	dB := DeriveObjectiveState(objB, attempts, nil, nil)
	if dB.State != types.ObjectiveStateVerified {
		t.Fatalf("unrelated objective unaffected, got %q", dB.State)
	}

	// Recovery: a frozen re-verification round (two fresh families after
	// the failure) restores verified — old-question memory cannot, per the
	// family-independence window: the recovery families are new.
	recovered := DeriveObjectiveState(objA, append(attempts,
		attemptFixture("a4", "obj-1", "fam-d", true, base.Add(80*time.Hour)),
		attemptFixture("a5", "obj-1", "fam-e", true, base.Add(81*time.Hour)),
	), nil, nil)
	if recovered.State != types.ObjectiveStateVerified {
		t.Fatalf("fresh re-verification round must restore verified, got %q (evidence %+v)", recovered.State, recovered.Evidence)
	}
}

// 验收6：同一快照与时间下事件重放结果相同，重复投递幂等。
func TestStage1_DeterministicAndIdempotent(t *testing.T) {
	obj := objFixture("obj-1", "concept/rag")
	base := time.Now().Add(-48 * time.Hour)
	attempts := []types.LearningQuizAttempt{
		attemptFixture("a1", "obj-1", "fam-a", true, base),
		attemptFixture("a2", "obj-1", "fam-b", true, base.Add(time.Hour)),
		attemptFixture("a3", "obj-1", "fam-a", false, base.Add(2*time.Hour)),
	}
	first := DeriveObjectiveState(obj, attempts, nil, nil)
	// Shuffle the input order: derivation output must be identical.
	shuffled := []types.LearningQuizAttempt{attempts[2], attempts[0], attempts[1]}
	second := DeriveObjectiveState(obj, shuffled, nil, nil)
	if first.State != second.State || first.Evidence.EligiblePasses != second.Evidence.EligiblePasses ||
		len(first.Evidence.FamiliesPassed) != len(second.Evidence.FamiliesPassed) {
		t.Fatalf("input order changed the derivation: %+v vs %+v", first, second)
	}
	// Duplicate delivery: the same attempt row re-delivered (same id/time)
	// must not add evidence — idempotent projection.
	dup := append(append([]types.LearningQuizAttempt{}, attempts...), attempts[0], attempts[1])
	third := DeriveObjectiveState(obj, dup, nil, nil)
	if third.Evidence.EligiblePasses != first.Evidence.EligiblePasses ||
		third.State != first.State {
		t.Fatalf("duplicate delivery changed the projection: %+v vs %+v", third, first)
	}
}

// 验收7：旧数据缺少元信息时可见、可导出，但不伪造严格验证状态。
// 服务器端核实导出包含 objective 段且 legacy 保持未验证。
func TestStage1_LegacyVisibleNeverVerified(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	if err := upsertObj(repo, "obj-1", "concept/rag"); err != nil {
		t.Fatal(err)
	}
	// The legacy item currently maps to the objective, so per-objective
	// legacy counts stay visible (display-only association).
	legacyItem := &types.LearningQuizItem{
		ID: "old-q", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "old?", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		Explanation: "x", ChunkRefs: types.RefList{"c1"},
		Status: types.LearningQuizStatusActive, ObjectiveID: "obj-1",
	}
	if err := repo.UpsertQuizItem(ctx, legacyItem); err != nil {
		t.Fatal(err)
	}
	// Legacy attempts: rows WITHOUT objective/family metadata (pre-stage).
	base := time.Now().Add(-72 * time.Hour)
	for i := 0; i < 5; i++ {
		if err := repo.InsertAttempt(ctx, ptrAttempt(types.LearningQuizAttempt{
			ID: "legacy-" + string(rune('a'+i)), TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
			QuizItemID: "old-q", Slug: "concept/rag", ChosenKey: "A", IsCorrect: true, AnsweredAt: base.Add(time.Duration(i) * time.Hour),
		})); err != nil {
			t.Fatal(err)
		}
	}

	view, err := svc.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	e := view.Entries[0]
	if e.State != types.ObjectiveStateUnverified {
		t.Fatalf("legacy history must never verify, got %q", e.State)
	}
	if e.Evidence.Source != types.ObjectiveSourceLegacy {
		t.Fatalf("source must read legacy_unverified, got %q", e.Evidence.Source)
	}
	if e.Evidence.LegacyAttempts != 5 {
		t.Fatalf("legacy attempts visible in their own counter, got %d", e.Evidence.LegacyAttempts)
	}
	if view.LegacyByNode["concept/rag"] != 5 {
		t.Fatalf("per-node legacy visibility, got %+v", view.LegacyByNode)
	}

	// Export: derived objective section present, legacy stays unverified,
	// raw attempts exported unchanged.
	payload, err := svc.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Objectives) != 1 || payload.Objectives[0].State != types.ObjectiveStateUnverified {
		t.Fatalf("export objective section = %+v", payload.Objectives)
	}
	if len(payload.Attempts) != 5 {
		t.Fatalf("raw legacy attempts exported, got %d", len(payload.Attempts))
	}
}

// stale_content：契约仅由旧内容版本的通过满足 → 保留历史、当前待复核；
// draft 目标无论证据如何都不产生 verified（未审核内容不能认证能力）。
func TestStage1_StaleContentAndDraftNeverVerify(t *testing.T) {
	base := time.Now().Add(-96 * time.Hour)
	old := attemptFixture("a1", "obj-1", "fam-a", true, base)
	old.ContentVersion = "v0"
	old2 := attemptFixture("a2", "obj-1", "fam-b", true, base.Add(time.Hour))
	old2.ContentVersion = "v0"

	obj := objFixture("obj-1", "concept/rag")
	d := DeriveObjectiveState(obj, []types.LearningQuizAttempt{old, old2}, nil, nil)
	if d.State != types.ObjectiveStateStale {
		t.Fatalf("contract met only under old versions = stale_content, got %q", d.State)
	}
	if len(d.Evidence.FamiliesPassedHistoric) != 2 || d.Evidence.StalePasses != 2 {
		t.Fatalf("history preserved: %+v", d.Evidence)
	}

	draft := objFixture("obj-1", "concept/rag")
	draft.Status = types.LearningObjectiveStatusDraft
	fresh := []types.LearningQuizAttempt{
		attemptFixture("a1", "obj-1", "fam-a", true, base),
		attemptFixture("a2", "obj-1", "fam-b", true, base.Add(time.Hour)),
	}
	dd := DeriveObjectiveState(draft, fresh, nil, nil)
	if dd.State != types.ObjectiveStateUnverified {
		t.Fatalf("draft must never verify in core, got %q", dd.State)
	}
	if good := DeriveObjectiveState(obj, fresh, nil, nil); good.State != types.ObjectiveStateVerified {
		t.Fatalf("published positive control: %s", good.State)
	}
	// Both the pure projection and assembled view enforce publication.
}

// 试次冻结链路：submitAnswer 把 item 的 objective/family 与目标内容版本
// 写进试次行（服务器侧），后续 item 改族不改历史。
func TestStage1_SubmitFreezesObjectiveLinkage(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	obj := objFixture("obj-1", "concept/rag")
	if err := repo.UpsertObjective(ctx, &obj); err != nil {
		t.Fatal(err)
	}
	item := &types.LearningQuizItem{
		ID: "quiz-linked", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag",
		Question: "linked?", CorrectKey: "A",
		Options:     types.QuizOptions{"A": "a", "B": "b", "C": "c", "D": "d"},
		Explanation: "x", ChunkRefs: types.RefList{"c1"},
		Status:       types.LearningQuizStatusActive,
		EvidenceHash: quizEvidenceHash(testWikiPage("concept/rag", []string{"c1"}, []string{"d1|Doc"}), map[string]string{"c1": "RAG evidence"}),
		ObjectiveID:  "obj-1", FamilyID: "fam-a",
	}
	if err := repo.UpsertQuizItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviewQuizItem(ctx, testKB, item.ID, interfaces.ReviewDecisionInput{Decision: "approve", ObjectiveID: obj.ID, FamilyID: "fam-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitAnswer(ctx, testKB, "quiz-linked", "A", ""); err != nil {
		t.Fatal(err)
	}
	attempts, _ := repo.ListAttempts(ctx, interfaces.LearningScope{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
	}, "")
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	a := attempts[0]
	if a.ObjectiveID != "obj-1" || a.FamilyID != "fam-a" || a.ContentVersion != "v1" {
		t.Fatalf("attempt must freeze objective/family/content version server-side, got %+v", a)
	}

	// Later re-familying the item does not rewrite the frozen attempt.
	item.FamilyID = "fam-zz"
	if err := repo.UpsertQuizItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	attempts, _ = repo.ListAttempts(ctx, interfaces.LearningScope{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
	}, "")
	if attempts[0].FamilyID != "fam-a" {
		t.Fatalf("history rewritten by later item edit: %+v", attempts[0])
	}
}
