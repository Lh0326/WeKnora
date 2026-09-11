package learning

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestPathRelevanceRejectsForeignWeakAndExpiredSources(t *testing.T) {
	now := time.Now()
	scope := newReadScope(1, "web_user:alice")
	pages := []*types.WikiPage{{Slug: "a", SourceRefs: []string{"d1|A"}}, {Slug: "b", SourceRefs: []string{"old|Old", "future|Future"}}}
	base := types.MemoryWikiMap{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, Slug: "a", Confidence: .9, NormalizedTopicKey: "topic", TopicLabel: "关注主题"}
	maps := []types.MemoryWikiMap{base}
	for _, mutate := range []func(*types.MemoryWikiMap){
		func(m *types.MemoryWikiMap) { m.SubjectID = "web_user:bob" },
		func(m *types.MemoryWikiMap) { m.TenantID = 2 },
		func(m *types.MemoryWikiMap) { m.KnowledgeBaseID = "other" },
		func(m *types.MemoryWikiMap) { m.Confidence = .2 },
		func(m *types.MemoryWikiMap) { m.Confidence = math.NaN() },
		func(m *types.MemoryWikiMap) { m.Confidence = math.Inf(1) },
	} {
		m := base
		m.Slug = "b"
		mutate(&m)
		maps = append(maps, m)
	}
	affinities := []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, KnowledgeID: "old", Hits: 10, LastUsedAt: now.Add(-91 * 24 * time.Hour)},
		{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, KnowledgeID: "future", Hits: 10, LastUsedAt: now.Add(time.Hour)},
		{TenantID: 1, SubjectID: "web_user:bob", KnowledgeBaseID: testKB, KnowledgeID: "d1", Hits: 10, LastUsedAt: now},
	}
	got := derivePathRelevance(scope, pages, affinities, maps, now)
	if len(got) != 1 || got["a"].Priority != 1 || !strings.Contains(got["a"].Reason.Detail, "关注主题") {
		t.Fatalf("unsafe relevance: %+v", got)
	}
	if len(deriveLearningNodes(pages, nil, nil, nil)) != 2 {
		t.Fatal("lost nodes")
	}
	for _, n := range deriveLearningNodes(pages, nil, nil, nil) {
		if n.State != "unseen" {
			t.Fatal("relevance certified mastery")
		}
	}
}

func TestPathRelevanceDocumentExplanationIsDeterministic(t *testing.T) {
	now := time.Now()
	scope := newReadScope(1, "web_user:alice")
	pages := []*types.WikiPage{{Slug: "a", SourceRefs: []string{"d2|Two", "d1|One"}}}
	rows := []types.MemoryDocAffinity{
		{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, KnowledgeID: "d2", Title: "常用文档二", Hits: 4, LastUsedAt: now},
		{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, KnowledgeID: "d1", Title: "常用文档一", Hits: 4, LastUsedAt: now},
	}
	a := derivePathRelevance(scope, pages, rows, nil, now)
	rows[0], rows[1] = rows[1], rows[0]
	pages[0].SourceRefs[0], pages[0].SourceRefs[1] = pages[0].SourceRefs[1], pages[0].SourceRefs[0]
	b := derivePathRelevance(scope, pages, rows, nil, now)
	if !reflect.DeepEqual(a, b) || a["a"].Priority != 2 || !strings.Contains(a["a"].Reason.Detail, "常用文档一") {
		t.Fatalf("unstable explanation: %+v / %+v", a, b)
	}
}

func TestRelevantPathStillHonorsExplicitReviewPrerequisitesAndBudget(t *testing.T) {
	in := planInput{IncludeExploration: true, GoalsResolved: true, Pages: map[string]string{"a": "前置", "b": "工作目标", "r": "待巩固", "c": "其他"}, PageMinutes: map[string]int{"a": 1, "b": 1, "r": 1, "c": 1}, NodeReview: map[string]bool{"r": true}, StrictEdges: map[string][]string{"a": {"b"}}, Relevance: map[string]pathRelevance{"b": {Priority: 2, Reason: PathReason{Code: "plan_reason_frequent_document", Detail: "常用资料"}}}, TimeBudgetMinutes: 3}
	p := planShortPath(in)
	if len(p.Steps) != 3 || p.Steps[0].Slug != "r" || p.Steps[1].Slug != "a" || p.Steps[2].Slug != "b" {
		t.Fatalf("priority bypassed obligations: %+v", p)
	}
	if len(p.Steps[2].Requires) == 0 || p.Steps[2].Reason.Code != "plan_reason_frequent_document" {
		t.Fatalf("missing dependency or explanation: %+v", p.Steps[2])
	}
	in.Skips = map[string]bool{"b": true}
	for _, step := range planShortPath(in).Steps {
		if step.Slug == "b" {
			t.Fatal("known node returned because of relevance")
		}
	}
}

func TestServiceShortPathUsesPersonalMemoryWithoutChangingNodeStates(t *testing.T) {
	svc, repo, _ := readFixture(t)
	repo.maps = map[string]*types.MemoryWikiMap{"alice": {TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "entity/weknora", NormalizedTopicKey: "weknora", TopicLabel: "企业知识管理", Confidence: .9}}
	ctx := collectorCtx(1, "alice")
	before, err := svc.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.ShortPath(ctx, testKB, interfaces.ColdStartRequestPayload{TimeBudgetMin: 5})
	if err != nil {
		t.Fatal(err)
	}
	if p.Personalization != "available" || len(p.Steps) == 0 || p.Steps[0].Slug != "entity/weknora" {
		t.Fatalf("memory absent from path: %+v", p)
	}
	after, err := svc.ObjectiveView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("recommendation modified mastery")
	}
	disabled := false
	p, err = svc.ShortPath(ctx, testKB, interfaces.ColdStartRequestPayload{TimeBudgetMin: 5, UseMemory: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if p.Personalization != "disabled" || p.Steps[0].Slug == "entity/weknora" {
		t.Fatalf("opt-out ignored: %+v", p)
	}
	p, err = svc.ShortPath(collectorCtx(1, "bob"), testKB, interfaces.ColdStartRequestPayload{TimeBudgetMin: 5})
	if err != nil {
		t.Fatal(err)
	}
	if p.Personalization != "no_signals" {
		t.Fatalf("another user's interests leaked: %+v", p)
	}
}

type unavailableMemoryRepo struct {
	*stubLearningRepo
	calls *int
}

func (r unavailableMemoryRepo) ListMapsBySubject(context.Context, uint64, string) ([]types.MemoryWikiMap, error) {
	*r.calls++
	return nil, errors.New("memory unavailable")
}
func TestPathMemoryFailureHasExplicitFallbackAndOptOutMakesNoMemoryRequest(t *testing.T) {
	_, repo, wiki := readFixture(t)
	calls := 0
	svc := NewService(unavailableMemoryRepo{repo, &calls}, wiki, nil, nil, nil, nil)
	p, err := svc.ShortPath(collectorCtx(1, "alice"), testKB, interfaces.ColdStartRequestPayload{TimeBudgetMin: 5})
	if err != nil {
		t.Fatal(err)
	}
	if p.Personalization != "unavailable" || len(p.Steps) == 0 {
		t.Fatalf("basic path unavailable: %+v", p)
	}
	if calls != 1 {
		t.Fatalf("expected one memory request, got %d", calls)
	}
	disabled := false
	p, err = svc.ShortPath(collectorCtx(1, "alice"), testKB, interfaces.ColdStartRequestPayload{TimeBudgetMin: 5, UseMemory: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if p.Personalization != "disabled" {
		t.Fatalf("read failed memory channel despite opt-out: %+v", p)
	}
	if calls != 1 {
		t.Fatalf("opt-out made another memory request: %d", calls)
	}
}
