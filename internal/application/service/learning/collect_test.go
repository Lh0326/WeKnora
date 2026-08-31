package learning

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSourceRefDocIDSplitsPrefix(t *testing.T) {
	cases := map[string]string{
		"doc-1|My Doc Title": "doc-1",
		"doc-2|":             "doc-2",
		"no-separator":       "",
		"|leading":           "", // empty id is no identity
	}
	for in, want := range cases {
		if got := sourceRefDocID(in); got != want {
			t.Errorf("sourceRefDocID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCitationsByKBGroupsAndDrops(t *testing.T) {
	refs := types.References{
		{ID: "c1", KnowledgeID: "d1", KnowledgeBaseID: "kb1"},
		{ID: "c2", KnowledgeID: "d1", KnowledgeBaseID: "kb1"}, // same doc, second chunk
		{ID: "c3", KnowledgeID: "d2", KnowledgeBaseID: "kb2"},
		nil,                           // tolerated
		{ID: "c4", KnowledgeID: "d3"}, // no KB: dropped
		{KnowledgeID: "d4", KnowledgeBaseID: "kb1"}, // no chunk id: doc-only citation
	}
	byKB := citationsByKB(refs)
	if len(byKB) != 2 {
		t.Fatalf("kb groups = %d, want 2", len(byKB))
	}
	kb1 := byKB["kb1"]
	// c4 has no knowledge base and is dropped; the d4 citation carries no
	// chunk id, so kb1 sees exactly c1 and c2.
	if len(kb1.chunkIDs) != 2 || !kb1.chunkIDs["c1"] || !kb1.chunkIDs["c2"] {
		t.Fatalf("kb1 chunks = %v", kb1.chunkIDs)
	}
	if len(kb1.docIDs) != 2 || !kb1.docIDs["d1"] || !kb1.docIDs["d4"] {
		t.Fatalf("kb1 docs = %v", kb1.docIDs)
	}
}

func testWikiPage(slug string, chunkRefs, sourceRefs []string) *types.WikiPage {
	pageType := "concept"
	for i := 0; i < len(slug); i++ {
		if slug[i] == '/' {
			pageType = slug[:i]
			break
		}
	}
	return &types.WikiPage{Slug: slug, PageType: pageType, ChunkRefs: chunkRefs, SourceRefs: sourceRefs}
}

func TestTouchedSlugsIntersectsBothHalves(t *testing.T) {
	index := buildPageRefIndex([]*types.WikiPage{
		testWikiPage("concept/rag", []string{"c1", "c2"}, []string{"d9|Old Draft"}),
		testWikiPage("concept/decay", []string{"c3"}, []string{"d1|Decay Doc"}),
		testWikiPage("entity/weknora", nil, []string{"d1|Weknora Doc", "d2|Other"}),
	})

	cases := []struct {
		name  string
		cites kbCitations
		want  []string
	}{
		{"chunk hit", kbCitations{chunkIDs: setOf("c1")}, []string{"concept/rag"}},
		{"doc hit via prefix", kbCitations{docIDs: setOf("d1")}, []string{"concept/decay", "entity/weknora"}},
		{"both halves same page once", kbCitations{chunkIDs: setOf("c3"), docIDs: setOf("d1")}, []string{"concept/decay", "entity/weknora"}},
		{"miss", kbCitations{chunkIDs: setOf("zz"), docIDs: setOf("zz")}, nil},
		{"sorted output", kbCitations{docIDs: setOf("d9", "d2", "d1")}, []string{"concept/decay", "concept/rag", "entity/weknora"}},
	}
	for _, tc := range cases {
		got := index.touchedSlugs(tc.cites)
		if len(got) != len(tc.want) {
			t.Errorf("%s: touched = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: touched[%d] = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

func setOf(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestClassifyTouchWindows(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	prior := func(slug string, at time.Time, eventType string) types.LearningEvent {
		return types.LearningEvent{Slug: slug, Type: eventType, OccurredAt: at}
	}

	cases := []struct {
		name  string
		prior []types.LearningEvent
		want  string
	}{
		{"first touch", nil, types.LearningEventAnswerCite},
		{"other slug ignored", []types.LearningEvent{prior("concept/other", now.Add(-time.Hour), types.LearningEventAnswerCite)}, types.LearningEventAnswerCite},
		{"quiz events ignored", []types.LearningEvent{prior("concept/rag", now.Add(-time.Minute), types.LearningEventQuizCorrect)}, types.LearningEventAnswerCite},
		{"inside window", []types.LearningEvent{prior("concept/rag", now.Add(-47*time.Hour), types.LearningEventAnswerCite)}, types.LearningEventReAsk},
		{"cross_ref counts as prior", []types.LearningEvent{prior("concept/rag", now.Add(-2*time.Hour), types.LearningEventCrossRef)}, types.LearningEventReAsk},
		{"outside window", []types.LearningEvent{prior("concept/rag", now.Add(-49*time.Hour), types.LearningEventAnswerCite)}, types.LearningEventCrossRef},
		{"boundary exactly 48h is re_ask", []types.LearningEvent{prior("concept/rag", now.Add(-48*time.Hour), types.LearningEventAnswerCite)}, types.LearningEventReAsk},
		// One re_ask per window: the "did not land" signal is fully expressed
		// by its first occurrence; later same-window touches are facet
		// exploration and are skipped (empty verdict), not re-punished.
		{"window already voiced re_ask", []types.LearningEvent{prior("concept/rag", now.Add(-46*time.Hour), types.LearningEventReAsk)}, ""},
		{"capped despite newer cite in window", []types.LearningEvent{
			prior("concept/rag", now.Add(-47*time.Hour), types.LearningEventAnswerCite),
			prior("concept/rag", now.Add(-46*time.Hour), types.LearningEventReAsk),
			prior("concept/rag", now.Add(-1*time.Hour), types.LearningEventAnswerCite),
		}, ""},
		{"stale re_ask beyond window does not cap", []types.LearningEvent{
			prior("concept/rag", now.Add(-72*time.Hour), types.LearningEventReAsk),
			prior("concept/rag", now.Add(-2*time.Hour), types.LearningEventAnswerCite),
		}, types.LearningEventReAsk},
	}
	for _, tc := range cases {
		for i := range tc.prior {
			if tc.prior[i].Type == "" {
				tc.prior[i].Type = types.LearningEventAnswerCite
			}
		}
		if got := classifyTouch(now, "concept/rag", tc.prior); got != tc.want {
			t.Errorf("%s: classifyTouch = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWeightForTypeCoversAllSignalTypes(t *testing.T) {
	cases := map[string]float64{
		types.LearningEventAnswerCite:  WeightAnswerCite,
		types.LearningEventCrossRef:    WeightCrossRef,
		types.LearningEventReAsk:       WeightReAsk,
		types.LearningEventTopicSignal: WeightTopicSignal,
		// Opportunistic signal rides the topic weight (spec §3.3.6).
		types.LearningEventWikiToolRead: WeightTopicSignal,
		types.LearningEventQuizCorrect:  WeightQuizCorrect,
		types.LearningEventQuizWrong:    WeightQuizWrong,
	}
	for eventType, want := range cases {
		if got := weightForType(eventType); got != want {
			t.Errorf("weightForType(%q) = %v, want %v", eventType, got, want)
		}
	}
	if got := weightForType("unknown"); got != 0 {
		t.Errorf("unknown type must weigh zero, got %v", got)
	}
}
