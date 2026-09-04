package learning

import (
	"reflect"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// healthFixture wires the real Service over the stub set with three node
// pages: rag (doc d1), decay (no sources) and legacy (doc d2).
func healthFixture(t *testing.T) (*Service, *stubLearningRepo, *stubWikiRepo) {
	t.Helper()
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	for _, p := range []*types.WikiPage{
		testWikiPage("concept/rag", nil, []string{"d1|RAG 手册"}),
		testWikiPage("concept/decay", nil, nil),
		testWikiPage("entity/legacy", nil, []string{"d2|旧版手册"}),
	} {
		p.Title = map[string]string{
			"concept/rag": "RAG", "concept/decay": "Decay", "entity/legacy": "Legacy",
		}[p.Slug]
		wiki.addPage(testKB, p)
	}
	svc := NewService(repo, wiki, nil, nil, nil, nil)
	return svc, repo, wiki
}

// healthMastery writes one folded row directly — read-path tests own the
// fold's output, not its history.
func healthMastery(tenant uint64, subject, kb, slug string, logit float64, evidence int, last time.Time) *types.MasteryState {
	return &types.MasteryState{
		TenantID: tenant, SubjectID: subject, KnowledgeBaseID: kb, Slug: slug,
		Logit: logit, EvidenceCount: evidence, LastEvidenceAt: last,
		Stability: StabilityBaseDays,
	}
}

// TestKnowledgeHealthCountsExpertsAndSinglePoint is the org math proof:
// two subjects, different bands per node — the summary counts, the expert
// ranking, the single-person risk and the folder roll-up must all land.
func TestKnowledgeHealthCountsExpertsAndSinglePoint(t *testing.T) {
	svc, repo, wiki := healthFixture(t)
	now := time.Now()

	// rag lives in a named folder; decay and legacy stay in the root.
	wiki.folders = map[string][]*types.WikiFolder{
		testKB: {{ID: "f1", Name: "RAG 基础"}},
	}
	for _, pages := range wiki.pages {
		for _, p := range pages {
			if p.Slug == "concept/rag" {
				p.FolderID = "f1"
			}
		}
	}

	// alice: rag familiar (fresh logit 1.5 → p≈0.82) and decay familiar
	// (fresh logit 1.2 → p≈0.77); bob: rag familiar plus decay covered only
	// (fresh logit -0.5 → p≈0.38). Legacy untouched — so decay is covered
	// by two people yet familiar to exactly one: the single-point shape.
	repo.mastery["1|web_user:alice|"+testKB+"|concept/rag"] = healthMastery(1, "web_user:alice", testKB, "concept/rag", 1.5, 3, now)
	repo.mastery["1|web_user:alice|"+testKB+"|concept/decay"] = healthMastery(1, "web_user:alice", testKB, "concept/decay", 1.2, 2, now)
	repo.mastery["1|web_user:bob|"+testKB+"|concept/rag"] = healthMastery(1, "web_user:bob", testKB, "concept/rag", 2.0, 4, now)
	repo.mastery["1|web_user:bob|"+testKB+"|concept/decay"] = healthMastery(1, "web_user:bob", testKB, "concept/decay", -0.5, 2, now)

	health, err := svc.KnowledgeHealth(collectorCtx(1, "alice"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	if health.NodesTotal != 3 || health.NodesCovered != 2 || health.SubjectsActive != 2 {
		t.Fatalf("summary = %d/%d/%d, want 3/2/2", health.NodesTotal, health.NodesCovered, health.SubjectsActive)
	}

	// Experts: rag (2 familiar) before decay (1 familiar), count desc.
	if len(health.Experts) != 2 {
		t.Fatalf("experts = %+v, want rag+decay", health.Experts)
	}
	if health.Experts[0].Slug != "concept/rag" || health.Experts[0].FamiliarCount != 2 || health.Experts[0].Title != "RAG" {
		t.Fatalf("top expert = %+v, want rag with 2", health.Experts[0])
	}
	if health.Experts[1].Slug != "concept/decay" || health.Experts[1].FamiliarCount != 1 {
		t.Fatalf("second expert = %+v, want decay with 1", health.Experts[1])
	}

	// Exactly one risk: decay is familiar to alice only (single_point).
	if len(health.Risks) != 1 {
		t.Fatalf("risks = %+v, want single single_point", health.Risks)
	}
	r := health.Risks[0]
	if r.Kind != "single_point" || r.Key != "concept/decay" || r.Title != "Decay" || r.FamiliarCount != 1 {
		t.Fatalf("risk = %+v, want single_point on decay", r)
	}
	if r.Note == "" || r.BestPEff < LevelTouchedUp {
		t.Fatalf("risk note/p_eff = %q/%f, want a note and a covered best", r.Note, r.BestPEff)
	}

	// Folders: root (2 nodes) before f1 (1 node) by node count desc; the
	// root keeps the empty name the client labels itself.
	if len(health.Folders) != 2 {
		t.Fatalf("folders = %+v, want root + f1", health.Folders)
	}
	if root := health.Folders[0]; root.FolderID != "" || root.FolderName != "" || root.TotalNodes != 2 || root.CoveredNodes != 1 || root.FamiliarUsers != 1 {
		t.Fatalf("root folder = %+v, want 2 nodes / 1 covered / 1 familiar", root)
	}
	if f1 := health.Folders[1]; f1.FolderID != "f1" || f1.FolderName != "RAG 基础" || f1.TotalNodes != 1 || f1.CoveredNodes != 1 || f1.FamiliarUsers != 2 {
		t.Fatalf("f1 folder = %+v, want 1 node with a bench of 2", f1)
	}
}

// TestKnowledgeHealthStaleDoc: a document someone clearly learned from
// (folded evidence) whose every covering pair has decayed below the
// covered band must surface as a stale_doc risk — learned before, decayed
// now — while a doc someone still covers must not.
func TestKnowledgeHealthStaleDoc(t *testing.T) {
	svc, repo, _ := healthFixture(t)
	now := time.Now()

	// alice learned legacy hard (logit cap) but two years ago: p_eff sinks
	// well below LevelTouchedUp at default stability.
	stale := healthMastery(1, "web_user:alice", testKB, "entity/legacy", LogitCap, 5, now.Add(-730*24*time.Hour))
	repo.mastery["1|web_user:alice|"+testKB+"|entity/legacy"] = stale
	// bob keeps rag alive, so d1 is not stale.
	repo.mastery["1|web_user:bob|"+testKB+"|concept/rag"] = healthMastery(1, "web_user:bob", testKB, "concept/rag", 1.5, 2, now)

	health, err := svc.KnowledgeHealth(collectorCtx(1, "alice"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	var staleRisk *HealthRisk
	for i := range health.Risks {
		if health.Risks[i].Kind == "stale_doc" {
			staleRisk = &health.Risks[i]
		}
	}
	if staleRisk == nil {
		t.Fatalf("risks = %+v, want a stale_doc on d2", health.Risks)
	}
	if staleRisk.Key != "d2" || staleRisk.Title != "旧版手册" {
		t.Fatalf("stale doc = %+v, want d2 旧版手册", staleRisk)
	}
	if staleRisk.FamiliarCount != 1 || staleRisk.BestPEff >= LevelTouchedUp {
		t.Fatalf("stale doc counts = %+v, want 1 learned pair below covered", staleRisk)
	}

	// A doc with a never-learned covering node (no evidence at all) is a
	// coverage gap, not staleness — legacy without alice's row must not
	// appear purely because its best is 0.
	delete(repo.mastery, "1|web_user:alice|"+testKB+"|entity/legacy")
	health2, err := svc.KnowledgeHealth(collectorCtx(1, "alice"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range health2.Risks {
		if r.Kind == "stale_doc" {
			t.Fatalf("never-learned doc surfaced as stale: %+v", r)
		}
	}
}

// TestKnowledgeHealthMaintenanceMarks: self-assessment events group by
// (kind, slug) across subjects with count and latest time; events outside
// the 30-day window and non-self-assess events never enter the queue.
func TestKnowledgeHealthMaintenanceMarks(t *testing.T) {
	svc, repo, _ := healthFixture(t)
	now := time.Now()
	append := func(subject, eventType string, slug string, at time.Time) {
		if err := repo.AppendEvent(nil, &types.LearningEvent{
			TenantID: 1, SubjectID: subject, KnowledgeBaseID: testKB,
			Slug: slug, Type: eventType, OccurredAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	append("web_user:alice", types.LearningEventSelfAssessDownDocGap, "concept/rag", now.Add(-24*time.Hour))
	append("web_user:bob", types.LearningEventSelfAssessDownDocGap, "concept/rag", now.Add(-48*time.Hour))
	append("web_user:alice", types.LearningEventSelfAssessUp, "concept/rag", now.Add(-72*time.Hour))
	append("web_user:alice", types.LearningEventSelfAssessDownDocGap, "concept/rag", now.Add(-40*24*time.Hour)) // out of window
	append("web_user:alice", types.LearningEventQuizCorrect, "concept/rag", now.Add(-time.Hour))                // not a mark

	health, err := svc.KnowledgeHealth(collectorCtx(1, "alice"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	if len(health.Maintenance) != 2 {
		t.Fatalf("maintenance = %+v, want one doc_gap group + one up group", health.Maintenance)
	}
	// count desc: the twice-flagged doc gap leads with the newer timestamp.
	first := health.Maintenance[0]
	if first.Kind != types.LearningEventSelfAssessDownDocGap || first.Slug != "concept/rag" || first.Count != 2 {
		t.Fatalf("first mark = %+v, want doc_gap/rag x2", first)
	}
	if first.Title != "RAG" {
		t.Fatalf("mark title = %q, want the page title", first.Title)
	}
	wantLatest := now.Add(-24 * time.Hour)
	if !first.LatestAt.Equal(wantLatest) {
		t.Fatalf("mark latest = %v, want %v", first.LatestAt, wantLatest)
	}
	if second := health.Maintenance[1]; second.Kind != types.LearningEventSelfAssessUp || second.Count != 1 {
		t.Fatalf("second mark = %+v, want self_assess_up x1", second)
	}
}

// TestKnowledgeHealthDeterministic: two runs over the same input must be
// deep-equal. Evidence is timestamped just ahead of the read clock, which
// EffectiveP clamps to "no decay yet" (p_eff = sigmoid exactly), so the
// microseconds between the two calls cannot leak into the comparison —
// what this really pins down is that map iteration never reaches the
// output order.
func TestKnowledgeHealthDeterministic(t *testing.T) {
	svc, repo, wiki := healthFixture(t)
	now := time.Now()
	fresh := now.Add(time.Hour)
	wiki.folders = map[string][]*types.WikiFolder{testKB: {{ID: "f1", Name: "RAG 基础"}}}
	for _, pages := range wiki.pages {
		for _, p := range pages {
			if p.Slug == "concept/rag" {
				p.FolderID = "f1"
			}
		}
	}
	repo.mastery["1|web_user:alice|"+testKB+"|concept/rag"] = healthMastery(1, "web_user:alice", testKB, "concept/rag", 1.5, 3, fresh)
	repo.mastery["1|web_user:alice|"+testKB+"|concept/decay"] = healthMastery(1, "web_user:alice", testKB, "concept/decay", 2.2, 3, fresh)
	repo.mastery["1|web_user:bob|"+testKB+"|concept/rag"] = healthMastery(1, "web_user:bob", testKB, "concept/rag", 1.8, 2, fresh)
	_ = repo.AppendEvent(nil, &types.LearningEvent{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
		Slug: "concept/decay", Type: types.LearningEventSelfAssessDownDocUpdated, OccurredAt: now.Add(-time.Hour),
	})

	ctx := collectorCtx(1, "alice")
	first, err := svc.KnowledgeHealth(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.KnowledgeHealth(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("health not deterministic:\nfirst  = %+v\nsecond = %+v", first, second)
	}
}

// TestKnowledgeHealthEmptyKB: a KB with no node pages answers zeros with
// empty (never nil) slices — the frontend always renders arrays. A mastery
// row for a slug that no longer resolves still counts its subject active:
// the person worked in this KB even if the page is gone.
func TestKnowledgeHealthEmptyKB(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	svc := NewService(repo, wiki, nil, nil, nil, nil)
	repo.mastery["1|web_user:alice|"+testKB+"|concept/gone"] = healthMastery(1, "web_user:alice", testKB, "concept/gone", 1.5, 3, time.Now())

	health, err := svc.KnowledgeHealth(collectorCtx(1, "alice"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	if health.NodesTotal != 0 || health.NodesCovered != 0 || health.SubjectsActive != 1 {
		t.Fatalf("summary = %d/%d/%d, want 0/0/1", health.NodesTotal, health.NodesCovered, health.SubjectsActive)
	}
	if health.Folders == nil || health.Experts == nil || health.Risks == nil || health.Maintenance == nil {
		t.Fatalf("slices must be empty, not nil: %+v", health)
	}
	if len(health.Folders)+len(health.Experts)+len(health.Risks)+len(health.Maintenance) != 0 {
		t.Fatalf("empty KB must carry no members: %+v", health)
	}
}

// TestKnowledgeHealthTenantAndKBIsolation: the aggregation must see only
// rows of THIS tenant and THIS kb — the stub filters strictly by the call
// arguments, so foreign rows sitting in the same store must not move a
// single count.
func TestKnowledgeHealthTenantAndKBIsolation(t *testing.T) {
	svc, repo, _ := healthFixture(t)
	now := time.Now()
	repo.mastery["1|web_user:alice|"+testKB+"|concept/rag"] = healthMastery(1, "web_user:alice", testKB, "concept/rag", 1.5, 3, now)

	// Foreign rows: another tenant (same kb) and another kb (same tenant),
	// both strongly familiar — they must not inflate anything.
	repo.mastery["2|web_user:eve|"+testKB+"|concept/rag"] = healthMastery(2, "web_user:eve", testKB, "concept/rag", 3.0, 5, now)
	repo.mastery["1|web_user:alice|kb-other|concept/rag"] = healthMastery(1, "web_user:alice", "kb-other", "concept/rag", 3.0, 5, now)
	_ = repo.AppendEvent(nil, &types.LearningEvent{
		TenantID: 2, SubjectID: "web_user:eve", KnowledgeBaseID: testKB,
		Slug: "concept/rag", Type: types.LearningEventSelfAssessDownDocGap, OccurredAt: now.Add(-time.Hour),
	})

	health, err := svc.KnowledgeHealth(collectorCtx(1, "alice"), testKB)
	if err != nil {
		t.Fatal(err)
	}
	if health.SubjectsActive != 1 || health.NodesCovered != 1 {
		t.Fatalf("summary = %d/%d, want 1/1 — foreign rows leaked in", health.SubjectsActive, health.NodesCovered)
	}
	if len(health.Experts) != 1 || health.Experts[0].FamiliarCount != 1 {
		t.Fatalf("experts = %+v, want exactly alice on rag", health.Experts)
	}
	if len(health.Maintenance) != 0 {
		t.Fatalf("maintenance = %+v, foreign-tenant marks leaked in", health.Maintenance)
	}
}

// TestKnowledgeHealthExcludesMachinePrincipals: API-key subjects (subject
// ids "api_*:...") legitimately accumulate mastery rows, but they are not
// people — the org aggregate must not count them as 熟悉人数/活跃主体.
func TestKnowledgeHealthExcludesMachinePrincipals(t *testing.T) {
	now := time.Now()
	pages := []*types.WikiPage{
		{Slug: "concept/alpha", PageType: "concept", Title: "甲"},
		{Slug: "concept/beta", PageType: "concept", Title: "乙"},
	}
	human := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now.Add(-24 * time.Hour)},
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now},
	}) // logit 4.4 -> clamped 4 -> p ≈ 98%: familiar
	machine := FoldAll(FoldState{}, []Event{
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now},
		{Type: types.LearningEventQuizCorrect, Weight: WeightQuizCorrect, OccurredAt: now},
	})
	rows := []types.MasteryState{
		*healthMastery(1, "web_user:u1", testKB, "concept/alpha", human.Logit, human.EvidenceCount, now),
		*healthMastery(1, "api_tenant:10000", testKB, "concept/beta", machine.Logit, machine.EvidenceCount, now),
	}
	h := deriveKnowledgeHealth(rows, pages, map[string]string{}, nil, now)
	if h.SubjectsActive != 1 {
		t.Fatalf("SubjectsActive = %d, want 1 (api key must not count as a person)", h.SubjectsActive)
	}
	for _, e := range h.Experts {
		if e.Slug == "concept/beta" {
			t.Fatalf("api-key-only node must not appear in experts, got %+v", e)
		}
	}
}
