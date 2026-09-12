package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TestZoneMapSkipCountsAsLit checks that a familiarity claim counts toward
// coverage. Zone totals and progress totals must use the same rule, including
// when the claimed node already has activity evidence.
func TestZoneMapSkipCountsAsLit(t *testing.T) {
	svc, repo, wiki := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")

	// fa 模块：一个从未接触的节点（跳过后 0/1 → 1/1）。
	// fb 模块：一个已点亮节点（跳过后保持 1/1，绝不退回 0/1）。
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/a1", PageType: "concept", Title: "甲一", FolderID: "fa"})
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/b1", PageType: "concept", Title: "乙一", FolderID: "fb"})
	wiki.folders = map[string][]*types.WikiFolder{
		testKB: {{ID: "fa", Name: "模块甲"}, {ID: "fb", Name: "模块乙"}},
	}
	seedFold(t, svc, repo, ctx, "concept/b1",
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-time.Hour)})

	// 跳过前基线：fa 0/1，fb 1/1。
	zm, err := svc.ZoneMap(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]interfaces.ZoneSummary{}
	for _, z := range zm.Zones {
		byID[z.FolderID] = z
	}
	if byID["fa"].Lit != 0 || byID["fa"].Total != 1 {
		t.Fatalf("fa before skip = %d/%d, want 0/1", byID["fa"].Lit, byID["fa"].Total)
	}
	if byID["fb"].Lit != 1 || byID["fb"].Total != 1 {
		t.Fatalf("fb before skip = %d/%d, want 1/1", byID["fb"].Lit, byID["fb"].Total)
	}

	// 两个节点都声明"已掌握，移除推荐"。
	if err := repo.AddSkip(ctx, interfaces.LearningScope{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
	}, "concept/a1", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddSkip(ctx, interfaces.LearningScope{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
	}, "concept/b1", time.Time{}); err != nil {
		t.Fatal(err)
	}

	zm, err = svc.ZoneMap(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	byID = map[string]interfaces.ZoneSummary{}
	for _, z := range zm.Zones {
		byID[z.FolderID] = z
	}
	if byID["fa"].Lit != 1 || byID["fa"].Total != 1 {
		t.Fatalf("fa after skip = %d/%d, want 1/1 (declaration covers the node)", byID["fa"].Lit, byID["fa"].Total)
	}
	if byID["fb"].Lit != 1 || byID["fb"].Total != 1 {
		t.Fatalf("fb after skip = %d/%d, want 1/1 (lit must not be erased)", byID["fb"].Lit, byID["fb"].Total)
	}

	// 头部进度同口径：LitNodes 把两个跳过节点都计为已点亮。
	pg, err := svc.GetProgress(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	// 根区另有 readFixture 的页面（无 skip、可能未接触），只断言模块单元。
	unitLit := map[string]int{}
	for _, u := range pg.Units {
		unitLit[u.FolderID] = u.Lit
	}
	if unitLit["fa"] != 1 || unitLit["fb"] != 1 {
		t.Fatalf("unit lit after skip = %+v, want fa=1 fb=1", unitLit)
	}
	if pg.LitNodes < 2 {
		t.Fatalf("LitNodes = %d, want ≥2 (both skips count as covered)", pg.LitNodes)
	}
}
