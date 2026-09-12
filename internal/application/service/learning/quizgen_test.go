package learning

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func quizPage() *types.WikiPage {
	return &types.WikiPage{
		Slug: "concept/rag", PageType: "concept", Title: "RAG",
		KnowledgeBaseID: testKB, Content: "RAG combines retrieval and generation.",
		ChunkRefs: types.StringArray{"c1", "c2"},
	}
}

func validDraft() quizDraft {
	return quizDraft{
		Question:    "What does RAG combine?",
		Options:     map[string]string{"A": "retrieval and generation", "B": "encryption", "C": "compilation", "D": "compression"},
		CorrectKey:  "A",
		Explanation: "The page states RAG combines retrieval and generation.",
		ChunkRefs:   []string{"c1"},
	}
}

func TestValidateQuizDraftAcceptsGrounded(t *testing.T) {
	item := validateQuizDraft(validDraft(), quizPage())
	if item == nil {
		t.Fatal("grounded draft rejected")
	}
	if item.Question == "" || item.CorrectKey != "A" || len(item.ChunkRefs) != 1 || item.ChunkRefs[0] != "c1" {
		t.Fatalf("item mangled: %+v", item)
	}
	if item.Status != types.LearningQuizStatusActive {
		t.Fatal("new items must be active")
	}
}

func TestValidateQuizDraftRejectsUngrounded(t *testing.T) {
	page := quizPage()
	cases := map[string]func(d *quizDraft){
		"out-of-page chunk ref": func(d *quizDraft) { d.ChunkRefs = []string{"c1", "c-secret"} },
		"empty chunk refs":      func(d *quizDraft) { d.ChunkRefs = nil },
		"three options":         func(d *quizDraft) { delete(d.Options, "D") },
		"empty option text":     func(d *quizDraft) { d.Options["B"] = "  " },
		"bad correct key":       func(d *quizDraft) { d.CorrectKey = "E" },
		"missing correct key":   func(d *quizDraft) { d.CorrectKey = "" },
		"empty question":        func(d *quizDraft) { d.Question = " " },
		"empty explanation":     func(d *quizDraft) { d.Explanation = "" },
	}
	for name, mutate := range cases {
		draft := validDraft()
		mutate(&draft)
		if item := validateQuizDraft(draft, page); item != nil {
			t.Errorf("%s: draft must be rejected whole", name)
		}
	}
	if validateQuizDraft(validDraft(), nil) != nil {
		t.Error("nil page must reject")
	}
}

func TestQuizDeficitOnlyForNodeTypes(t *testing.T) {
	if got := quizDeficit(&types.WikiPage{PageType: "summary"}, 0); got != 0 {
		t.Error("summary pages never quiz")
	}
	if got := quizDeficit(&types.WikiPage{PageType: "concept"}, 0); got != QuizItemsPerSlug {
		t.Errorf("fresh concept deficit = %d", got)
	}
	if got := quizDeficit(&types.WikiPage{PageType: "entity"}, QuizItemsPerSlug); got != 0 {
		t.Error("full page has no deficit")
	}
	if got := quizDeficit(&types.WikiPage{PageType: "entity"}, QuizItemsPerSlug+5); got != 0 {
		t.Error("overfull page clamps to zero")
	}
}

func TestQuizUserPromptCarriesEvidence(t *testing.T) {
	out := quizUserPrompt(quizPage(), 2, map[string]string{"c1": "chunk one text", "c2": "chunk two text"})
	for _, want := range []string{`<page slug="concept/rag"`, "Additional questions needed: 2", `<chunk id="c1">`, "chunk two text", "</evidence>"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
