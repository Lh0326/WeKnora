package learning

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// quizEvidenceChunkCap bounds how many cited chunks are fed to the quiz
// prompt per page; the first N by page order, which for wiki pages is
// already the curation order.
const quizEvidenceChunkCap = 20

// quizDraft is the raw LLM output shape for one question.
type quizDraft struct {
	Question    string            `json:"question"`
	Options     map[string]string `json:"options"`
	CorrectKey  string            `json:"correct_key"`
	Explanation string            `json:"explanation"`
	ChunkRefs   []string          `json:"chunk_refs"`
}

// quizResponse is the LLM response envelope.
type quizResponse struct {
	Questions []quizDraft `json:"questions"`
}

func normalizedQuizQuestion(question string) string {
	return strings.ToLower(strings.Join(strings.Fields(question), " "))
}

// validateQuizDraft enforces the grounding contract deterministically:
// exactly four non-empty options keyed A–D, a correct key among them, a
// non-empty stem and explanation, and chunk refs that are a non-empty
// subset of the page's own ChunkRefs. Any violation rejects the whole
// question — partial rescue would launder ungrounded content.
func validateQuizDraft(d quizDraft, page *types.WikiPage) *types.LearningQuizItem {
	if page == nil {
		return nil
	}
	if strings.TrimSpace(d.Question) == "" || strings.TrimSpace(d.Explanation) == "" {
		return nil
	}
	if len(d.Options) != 4 {
		return nil
	}
	seenOptions := map[string]bool{}
	for _, key := range []string{"A", "B", "C", "D"} {
		normalized := strings.ToLower(strings.Join(strings.Fields(d.Options[key]), " "))
		if normalized == "" || seenOptions[normalized] {
			return nil
		}
		seenOptions[normalized] = true
	}
	if d.CorrectKey != "A" && d.CorrectKey != "B" && d.CorrectKey != "C" && d.CorrectKey != "D" {
		return nil
	}
	if len(d.ChunkRefs) == 0 {
		return nil
	}
	pageChunks := map[string]bool{}
	for _, c := range page.ChunkRefs {
		pageChunks[c] = true
	}
	for _, c := range d.ChunkRefs {
		if !pageChunks[c] {
			return nil // out-of-page citation: the whole question is ungrounded
		}
	}
	options := types.QuizOptions{}
	for k, v := range d.Options {
		options[k] = v
	}
	return &types.LearningQuizItem{
		TenantID:        0, // filled by the caller (kb scope)
		KnowledgeBaseID: page.KnowledgeBaseID,
		Slug:            page.Slug,
		Question:        d.Question,
		Options:         options,
		CorrectKey:      d.CorrectKey,
		Explanation:     d.Explanation,
		ChunkRefs:       append(types.RefList{}, d.ChunkRefs...),
		Status:          types.LearningQuizStatusActive,
	}
}

// quizDeficit returns how many more active items a page needs.
func quizDeficit(page *types.WikiPage, activeItems int) int {
	if page.PageType != "entity" && page.PageType != "concept" {
		return 0
	}
	d := QuizItemsPerSlug - activeItems
	if d < 0 {
		return 0
	}
	return d
}

// quizUserPrompt renders one page's grounding input.
func quizUserPrompt(page *types.WikiPage, need int, chunkExcerpts map[string]string) string {
	var b strings.Builder
	b.WriteString("<page slug=\"" + page.Slug + "\" type=\"" + page.PageType + "\" title=\"" + page.Title + "\">\n")
	b.WriteString(page.Content)
	b.WriteString("\n</page>\n\n")
	b.WriteString("Additional questions needed: ")
	b.WriteString(strconv.Itoa(need))
	b.WriteString("\n\n<evidence>\n")
	ids := make([]string, 0, len(chunkExcerpts))
	for id := range chunkExcerpts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b.WriteString("  <chunk id=\"" + id + "\">\n")
		b.WriteString(chunkExcerpts[id])
		b.WriteString("\n  </chunk>\n")
	}
	b.WriteString("</evidence>\n")
	return b.String()
}

// quizSchema is the response format for the quiz generation call.
var quizSchema = jsonRaw(`{
  "type": "object",
  "properties": {
    "questions": {"type": "array", "items": {"type": "object"}}
  },
  "required": ["questions"]
}`)
