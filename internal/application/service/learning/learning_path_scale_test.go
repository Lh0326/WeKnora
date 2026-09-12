package learning

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRequestedReviewCannotBeStarvedByPublishedGoals(t *testing.T) {
	in := planInput{IncludeExploration: true, Pages: map[string]string{"review": "Needs another look"}, Objectives: map[string]types.LearningObjective{}, NodeReview: map[string]bool{"review": true}, PageMinutes: map[string]int{"review": 1}, TimeBudgetMinutes: 15}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("goal-%02d", i)
		in.Pages[id] = id
		in.Objectives[id] = objFixture(id, id)
	}
	p := planShortPath(in)
	if len(p.Steps) == 0 || p.Steps[0].Slug != "review" || p.Steps[0].Reason.Code != "plan_reason_user_review" {
		t.Fatalf("explicit review starved: %+v", p)
	}
	spent := 0
	for _, step := range p.Steps {
		spent += step.Minutes
	}
	if len(p.Steps) > 5 || spent > 15 {
		t.Fatalf("unbounded path: %+v", p)
	}
}

func BenchmarkShortPathTenThousandNodes(b *testing.B) {
	in := planInput{IncludeExploration: true, GoalsResolved: true, Pages: map[string]string{}, DocOrder: map[string]int{}, PageMinutes: map[string]int{}, TimeBudgetMinutes: 15}
	for i := 0; i < 10000; i++ {
		slug := fmt.Sprintf("concept/%05d", i)
		in.Pages[slug] = slug
		in.DocOrder[slug] = i
		in.PageMinutes[slug] = 2
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := planShortPath(in)
		if len(p.Steps) != 5 {
			b.Fatalf("wrong plan size: %d", len(p.Steps))
		}
	}
}

func BenchmarkRecentAnchorsTenThousandNodes(b *testing.B) {
	events := make([]types.LearningEvent, 10000)
	for i := range events {
		events[i] = types.LearningEvent{Slug: fmt.Sprintf("concept/%05d", i), Type: types.LearningEventNodeRead, OccurredAt: time.Unix(int64(i), 0)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := recentAnchorsOf(events); len(got) != 5 || got[0].Slug != "concept/09999" {
			b.Fatal("incorrect bounded recent anchors")
		}
	}
}
