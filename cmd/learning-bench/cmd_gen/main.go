package main

// gen_replay_input builds a synthetic event stream large enough for
// replay's 80/20 split to have statistical meaning. It creates multiple
// slugs with varying touch patterns (frequent, occasional, quiz-heavy).
// Run with: go run ./cmd/learning-bench/cmd_gen > replay-input.json

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func main() {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var events []types.LearningEvent

	slugs := []struct {
		slug    string
		pattern string // frequent | occasional | quiz_heavy | sparse
	}{
		{"concept/rag", "frequent"},
		{"concept/decay", "frequent"},
		{"concept/vector-search", "occasional"},
		{"concept/hybrid-rerank", "occasional"},
		{"concept/embedding-model", "quiz_heavy"},
		{"concept/semantic-search", "quiz_heavy"},
		{"entity/weknora", "sparse"},
		{"concept/knowledge-graph", "sparse"},
		{"concept/chunking-strategy", "occasional"},
		{"concept/document-parsing", "sparse"},
		{"concept/bm25-scoring", "sparse"},
		{"concept/neo4j-integration", "sparse"},
		{"concept/api-design", "occasional"},
		{"concept/agent-workflow", "sparse"},
	}

	id := 0
	mkEvent := func(slug, eventType string, weight float64, day int) {
		events = append(events, types.LearningEvent{
			ID:              fmt.Sprintf("ev-%03d", id),
			TenantID:        1,
			SubjectID:       "web_user:sim",
			KnowledgeBaseID: "sim-kb",
			Slug:            slug,
			Type:            eventType,
			Weight:          weight,
			OccurredAt:      base.Add(time.Duration(day) * 24 * time.Hour),
		})
		id++
	}

	for _, s := range slugs {
		switch s.pattern {
		case "frequent":
			for d := 0; d < 50; d += 2 {
				mkEvent(s.slug, types.LearningEventAnswerCite, 1.0, d)
			}
			for d := 5; d < 45; d += 10 {
				mkEvent(s.slug, types.LearningEventQuizCorrect, 2.2, d)
			}
		case "occasional":
			for d := 3; d < 45; d += 7 {
				mkEvent(s.slug, types.LearningEventAnswerCite, 1.0, d)
			}
		case "quiz_heavy":
			for d := 1; d < 40; d += 3 {
				mkEvent(s.slug, types.LearningEventAnswerCite, 1.0, d)
			}
			for d := 2; d < 42; d += 5 {
				mkEvent(s.slug, types.LearningEventQuizCorrect, 2.2, d)
			}
			for d := 4; d < 44; d += 8 {
				mkEvent(s.slug, types.LearningEventQuizWrong, -1.5, d)
			}
		case "sparse":
			for d := 10; d < 50; d += 20 {
				mkEvent(s.slug, types.LearningEventAnswerCite, 1.0, d)
			}
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"events": events}); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}
