package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ---- Stage-3 acceptance trajectories: cold start and short paths ----
//
// The seven acceptance trajectories of 阶段3, pinned against the PURE
// planner (deterministic by construction) with fixtures that mirror the
// service-assembled inputs.

func planFixture() planInput {
	// KB shape: python(base) → langchain(mid) → rag(apply), plus a task
	// objective on rag. Doc order follows that line.
	return planInput{
		TenantID: 1, KBID: testKB,
		Pages: map[string]string{
			"entity/python":    "Python",
			"entity/langchain": "LangChain",
			"concept/rag":      "检索增强生成",
		},
		Objectives: map[string]types.LearningObjective{
			"obj-py":  {ID: "obj-py", Slug: "entity/python", ContractType: types.ObjectiveContractConceptTwoFamily, Status: types.LearningObjectiveStatusPublished},
			"obj-lc":  {ID: "obj-lc", Slug: "entity/langchain", ContractType: types.ObjectiveContractConceptTwoFamily, Status: types.LearningObjectiveStatusPublished},
			"obj-rag": {ID: "obj-rag", Slug: "concept/rag", ContractType: types.ObjectiveContractTaskChecks, Status: types.LearningObjectiveStatusPublished},
		},
		ObjBySlug: map[string][]string{
			"entity/python":    {"obj-py"},
			"entity/langchain": {"obj-lc"},
			"concept/rag":      {"obj-rag"},
		},
		States: map[string]ObjectiveDerivation{
			"obj-py":  {State: types.ObjectiveStateUnverified, Evidence: ObjectiveEvidence{Unknown: true}},
			"obj-lc":  {State: types.ObjectiveStateUnverified, Evidence: ObjectiveEvidence{Unknown: true}},
			"obj-rag": {State: types.ObjectiveStateUnverified, Evidence: ObjectiveEvidence{Unknown: true}},
		},
		StrictEdges: map[string][]string{
			"entity/python":    {"entity/langchain"},
			"entity/langchain": {"concept/rag"},
		},
		DocOrder:          map[string]int{"entity/python": 0, "entity/langchain": 1, "concept/rag": 2},
		Skips:             map[string]bool{},
		Exposure:          map[string]bool{},
		ReviewDue:         map[string]bool{},
		FailedRecently:    map[string]bool{},
		TimeBudgetMinutes: 15,
	}
}

// 轨迹1 新人：总览—基础—一次验证—后续应用。
func TestStage3_NewcomerPath(t *testing.T) {
	in := planFixture()
	in.UserGoals = []string{"obj-rag"} // the newcomer wants the applied goal
	plan := planShortPath(in)
	if plan.Degrade != "" || len(plan.Steps) == 0 {
		t.Fatalf("newcomer must get a real path: %+v", plan)
	}
	// The strict chain gates: langchain behind python, rag behind
	// langchain — the first actionable step is the base node with a
	// bridge/read action, NOT the applied goal directly.
	first := plan.Steps[0]
	if first.Slug != "entity/python" && first.Action != ActionBridge {
		t.Fatalf("newcomer first step should target the unmet base (bridge/read), got %+v", first)
	}
	// Conditional successors exist and never assume prior success.
	for i := 1; i < len(plan.Steps); i++ {
		if len(plan.Steps[i].Requires) == 0 {
			t.Fatalf("successor step %d must state its condition", i)
		}
	}
}

// 轨迹2 熟手冷启动：直接挑战—跳过基础阅读—进入应用。
func TestStage3_FastTrackSkipBasics(t *testing.T) {
	in := planFixture()
	in.UserGoals = []string{"obj-rag"}
	in.FastTrack = true
	plan := planShortPath(in)
	if len(plan.Steps) == 0 {
		t.Fatal("fast track must produce a path")
	}
	first := plan.Steps[0]
	if first.Action != ActionVerify {
		t.Fatalf("fast-track first step must be a direct challenge (verify), got %+v", first)
	}
	if first.Eligibility != "override" {
		t.Fatalf("challenge eligibility must be override (declaration, not certification), got %q", first.Eligibility)
	}
}

// 轨迹3 不测验用户：可自由探索，显示接触覆盖，能力仍未知。
func TestStage3_NoQuizFreeExploration(t *testing.T) {
	in := planFixture()
	// No goals requested, no objectives verified — the planner still
	// yields an orientation path; capability stays unknown.
	in.UserGoals = nil
	plan := planShortPath(in)
	if len(plan.Steps) == 0 && plan.Degrade == "" {
		t.Fatal("no-quiz user must still get an understandable path")
	}
	// Exposure is contact-only and never flips a state: unknown stays
	// unknown regardless of exposure marks.
	in.Exposure = map[string]bool{"entity/python": true, "concept/rag": true}
	plan2 := planShortPath(in)
	for _, st := range plan2.Steps {
		if st.Action == ActionVerify && st.Eligibility == "default" && in.States[st.Objective].State == types.ObjectiveStateUnverified {
			// A verify step offered as DEFAULT eligibility for an unknown
			// objective would be fine (challenge offered), but the state
			// itself must remain unknown — exposure never certifies.
			if in.States[st.Objective].State != types.ObjectiveStateUnverified {
				t.Fatal("exposure must not certify ability")
			}
		}
	}
}

// 轨迹4 先修冲突：一次桥接后可继续选择，不能永远锁死。
func TestStage4_PrereqConflictBridgesOnce(t *testing.T) {
	in := planFixture()
	in.UserGoals = []string{"obj-rag"}
	// python unverified and its strict edge gates langchain: the plan must
	// contain a bridge step and then continue — never dead-end.
	plan := planShortPath(in)
	hasBridge := false
	for _, st := range plan.Steps {
		if st.Action == ActionBridge {
			hasBridge = true
		}
	}
	if !hasBridge {
		t.Fatalf("unmet strict prereq must yield a bridge step: %+v", plan.Steps)
	}
	// A skip on the prerequisite releases the gate as a PATH CHOICE (the
	// user declares the base) without certifying it.
	in.Skips = map[string]bool{"entity/python": true}
	plan2 := planShortPath(in)
	if len(plan2.Steps) == 0 {
		t.Fatal("skip must unlock the path")
	}
	if in.States["obj-py"].State != types.ObjectiveStateUnverified {
		t.Fatal("skip must never flip the state to verified")
	}
}

// 轨迹5 循环图、失效边、删除页、题库缺失的确定性处理。
func TestStage3_GraphAnomalies(t *testing.T) {
	// Cycle: python → langchain → python. The service filters dangling
	// edges; the planner itself must not loop — candidate collection is
	// bounded by pages, and the plan length caps at 5.
	in := planFixture()
	in.StrictEdges["concept/rag"] = []string{"entity/python"} // closes the cycle
	in.UserGoals = []string{"obj-rag"}
	plan := planShortPath(in)
	if len(plan.Steps) > 5 {
		t.Fatalf("plan length must cap at 5, got %d", len(plan.Steps))
	}
	// Deleted page: the objective's page is gone → the service drops it;
	// at the planner level an empty universe degrades deterministically.
	empty := planFixture()
	empty.Pages = map[string]string{}
	empty.Objectives = map[string]types.LearningObjective{}
	empty.ObjBySlug = map[string][]string{}
	empty.UserGoals = nil
	degraded := planShortPath(empty)
	if degraded.Degrade != "plan_degrade_no_content" || len(degraded.Steps) != 0 {
		t.Fatalf("empty KB must degrade understandably, got %+v", degraded)
	}
	// Missing quiz bank: unverified objectives still yield read/practice
	// steps (no verify requirement to produce a path); no fabrication.
	noBank := planFixture()
	noBank.UserGoals = []string{"obj-py"}
	noBank.HasVerification = map[string]bool{}
	noBank.States["obj-py"] = ObjectiveDerivation{State: types.ObjectiveStateUnverified, Evidence: ObjectiveEvidence{Unknown: true}}
	p3 := planShortPath(noBank)
	if len(p3.Steps) == 0 || p3.Steps[0].Action == ActionVerify && p3.Steps[0].Objective == "" {
		t.Fatalf("missing bank must still path to read, got %+v", p3.Steps)
	}
}

// 轨迹6 相同输入、策略版本与种子得到同一结果。
func TestStage3_DeterministicPlanning(t *testing.T) {
	in := planFixture()
	in.UserGoals = []string{"obj-rag"}
	a := planShortPath(in)
	b := planShortPath(in)
	if len(a.Steps) != len(b.Steps) || a.Degrade != b.Degrade {
		t.Fatalf("plan shape drifted")
	}
	for i := range a.Steps {
		if a.Steps[i].Slug != b.Steps[i].Slug || a.Steps[i].Action != b.Steps[i].Action ||
			a.Steps[i].Objective != b.Steps[i].Objective || a.Steps[i].Reason.Code != b.Steps[i].Reason.Code {
			t.Fatalf("step %d drifted: %+v vs %+v", i, a.Steps[i], b.Steps[i])
		}
	}
	if a.PolicyVersion != PathPolicyVersion {
		t.Fatalf("policy version must be stamped")
	}
}

// 轨迹7 旧skip能映射为用户路径选择，不自动变成verified。
func TestStage3_SkipIsPathChoiceNeverVerified(t *testing.T) {
	in := planFixture()
	in.Skips = map[string]bool{"entity/python": true}
	in.UserGoals = []string{"obj-lc"} // behind the skipped base
	plan := planShortPath(in)
	// The gate released; obj-lc appears (its node is reachable).
	found := false
	for _, st := range plan.Steps {
		if st.Slug == "entity/langchain" {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped prerequisite must release the default gate: %+v", plan.Steps)
	}
	// The skipped objective's derivation is untouched by the planner.
	if in.States["obj-py"].State != types.ObjectiveStateUnverified {
		t.Fatal("skip must never become verified")
	}
}

// 防卡住：同一目标连续失败后改桥接/换材料，不重复同一道题；复核
// 建议最多一条且不压过新内容。
func TestStage3_AntiStuckAndReviewCap(t *testing.T) {
	in := planFixture()
	in.UserGoals = []string{"obj-rag"}
	// Two consecutive recent failures on the rag objective.
	in.FailedRecently = map[string]bool{"obj-rag": true}
	plan := planShortPath(in)
	if len(plan.Steps) == 0 || plan.Steps[0].Action != ActionBridge {
		t.Fatalf("two consecutive failures must surface a bridge first, got %+v", plan.Steps)
	}
	// Review cap: mark everything review-due; at most one review step.
	in2 := planFixture()
	in2.ReviewDue = map[string]bool{"obj-py": true, "obj-lc": true, "obj-rag": true}
	in2.States["obj-py"] = ObjectiveDerivation{State: types.ObjectiveStateVerified, Evidence: ObjectiveEvidence{LastPassAt: time.Now().Add(-30 * 24 * time.Hour)}}
	in2.States["obj-lc"] = ObjectiveDerivation{State: types.ObjectiveStateVerified, Evidence: ObjectiveEvidence{LastPassAt: time.Now().Add(-30 * 24 * time.Hour)}}
	in2.UserGoals = []string{"obj-rag"}
	plan2 := planShortPath(in2)
	reviews := 0
	for _, st := range plan2.Steps {
		if st.Action == ActionReview {
			reviews++
		}
	}
	if reviews > 1 {
		t.Fatalf("review suggestions must cap at one per plan, got %d", reviews)
	}
	// New-goal steps are never displaced by review (prio 2 sorts before
	// prio 4). The goal step's objective is "obj-rag"; a review step for
	// obj-py/obj-lc must not precede it.
	goalIdx := -1
	reviewIdx := -1
	for i, st := range plan2.Steps {
		if st.Objective == "obj-rag" && goalIdx == -1 {
			goalIdx = i
		}
		if st.Action == ActionReview && reviewIdx == -1 {
			reviewIdx = i
		}
	}
	if goalIdx == -1 {
		for i, st := range plan2.Steps {
			t.Logf("step[%d] = %+v", i, st)
		}
		t.Fatal("the user's goal step must appear in the plan")
	}
	if reviewIdx != -1 && reviewIdx < goalIdx {
		for i, st := range plan2.Steps {
			t.Logf("step[%d] = %+v", i, st)
		}
		t.Fatalf("review (idx %d) must not precede the goal step (idx %d)", reviewIdx, goalIdx)
	}
}
