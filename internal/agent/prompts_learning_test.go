package agent

import (
	"testing"
	"text/template"
)

// TestLearningPromptsParse mirrors the prompts_wiki_test.go contract: a
// prompt constant must be a valid text/template. The learning prompts take
// no template fields (their inputs are appended as literal blocks by the
// caller), so plain parsing is the whole contract.
func TestLearningPromptsParse(t *testing.T) {
	for name, prompt := range map[string]string{
		"LearningTopicMapPrompt":   LearningTopicMapPrompt,
		"LearningPrereqEdgePrompt": LearningPrereqEdgePrompt,
		"LearningQuizPrompt":       LearningQuizPrompt,
	} {
		if _, err := template.New(name).Parse(prompt); err != nil {
			t.Errorf("%s does not parse: %v", name, err)
		}
	}
}

// TestLearningPromptsCarryRejectionAndGroundingContracts pins the two
// safety-critical wordings: the adjudicators must promise an explicit
// reject/none escape, and the quiz prompt must carry the verbatim-token
// chunk-id rule plus the thin-evidence escape.
func TestLearningPromptsCarryRejectionAndGroundingContracts(t *testing.T) {
	if !contains(LearningTopicMapPrompt, `"reject"`) || !contains(LearningTopicMapPrompt, "NEVER invent a slug") {
		t.Error("topic map prompt lost its rejection/invention guards")
	}
	if !contains(LearningPrereqEdgePrompt, "related ≠ prerequisite") || !contains(LearningPrereqEdgePrompt, `"prerequisite"`) {
		t.Error("prereq prompt lost its key principle or vocabulary")
	}
	if !contains(LearningQuizPrompt, "VERBATIM") || !contains(LearningQuizPrompt, `{"questions": []}`) {
		t.Error("quiz prompt lost its verbatim-token or empty-escape contract")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
