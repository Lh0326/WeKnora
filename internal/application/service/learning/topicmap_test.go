package learning

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestTopicCandidatesMatchesTitleAliasAndPrefix(t *testing.T) {
	pages := []*types.WikiPage{
		{Slug: "concept/rag", PageType: "concept", Title: "RAG"},
		{Slug: "concept/rag-pipeline", PageType: "concept", Title: "Retrieval Pipeline"},
		{Slug: "entity/weknora", PageType: "entity", Title: "WeKnora", Aliases: types.StringArray{"知识库框架"}},
		{Slug: "concept/decay", PageType: "concept", Title: "Lazy Decay"},
	}
	cases := []struct {
		name string
		stat types.MemoryTopicStat
		want []string
	}{
		{"exact title", types.MemoryTopicStat{Topic: "RAG"}, []string{"concept/rag", "concept/rag-pipeline"}},
		{"alias match", types.MemoryTopicStat{Topic: "框架", Aliases: types.MemoryTopicAliases{"知识库框架"}}, []string{"entity/weknora"}},
		{"no match", types.MemoryTopicStat{Topic: "cooking recipes"}, nil},
		{"cap applied", types.MemoryTopicStat{Topic: "ra"}, func() []string {
			// "ra" prefixes rag and rag-pipeline (title-normalized).
			return []string{"concept/rag", "concept/rag-pipeline"}
		}()},
	}
	for _, tc := range cases {
		got := topicCandidates(&tc.stat, pages, topicMaxCandidates)
		if len(got) != len(tc.want) {
			t.Errorf("%s: candidates = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: candidates[%d] = %q, want %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}

	// The cap must actually bound the list.
	many := make([]*types.WikiPage, 20)
	for i := range many {
		many[i] = &types.WikiPage{Slug: "concept/x", PageType: "concept", Title: "X"}
		many[i].Slug = "concept/x" + string(rune('a'+i))
		many[i].Title = "X" + string(rune('a'+i))
	}
	if got := topicCandidates(&types.MemoryTopicStat{Topic: "x"}, many, 3); len(got) != 3 {
		t.Fatalf("cap not enforced: %d", len(got))
	}
}

func TestAcceptedTopicMapsFilters(t *testing.T) {
	candidates := map[string][]string{
		"rag":   {"concept/rag", "concept/rag-pipeline"},
		"lunch": {"concept/cafeteria"},
	}
	resp := topicMapResponse{Maps: map[string]interface{}{
		"rag":   map[string]interface{}{"slug": "concept/rag", "confidence": 0.95},
		"low":   map[string]interface{}{"slug": "concept/rag", "confidence": 0.5},              // below floor
		"evil":  map[string]interface{}{"slug": "concept/not-a-candidate", "confidence": 0.99}, // hallucinated slug
		"lunch": "reject",                                                                      // explicit rejection
		"junk":  42,                                                                            // malformed value reads as reject
	}}
	// "low" and "evil" and "junk" have no candidate entries of their own except low/evil keyed topics;
	// give them candidate lists so only the intended filter reason applies.
	candidates["low"] = []string{"concept/rag"}
	candidates["evil"] = []string{"concept/rag"}
	candidates["junk"] = []string{"concept/rag"}

	accepted := acceptedTopicMaps(resp, candidates)
	if len(accepted) != 1 {
		t.Fatalf("accepted = %v, want only rag", accepted)
	}
	if v, ok := accepted["rag"]; !ok || v.Slug != "concept/rag" || v.Confidence != 0.95 {
		t.Fatalf("rag verdict = %+v", v)
	}
}

func TestTopicMapUserPromptNestsCandidates(t *testing.T) {
	stat := &types.MemoryTopicStat{NormalizedKey: "rag", Topic: "RAG", Aliases: types.MemoryTopicAliases{"检索增强"}}
	pages := map[string]*types.WikiPage{"concept/rag": {Slug: "concept/rag", PageType: "concept", Title: "RAG"}}
	out := topicMapUserPrompt([]*types.MemoryTopicStat{stat},
		map[string][]string{"rag": {"concept/rag"}}, pages)
	for _, want := range []string{`<topic key="rag">`, `<label>RAG</label>`, `<alias>检索增强</alias>`, `<page slug="concept/rag"`, `</candidates>`} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
