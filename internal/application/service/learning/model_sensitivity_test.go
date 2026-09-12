package learning

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/learning/estimator"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// This experiment measures sensitivity to uncalibrated assumptions. It uses
// the running estimator implementation, but no generated correctness labels:
// its results cannot measure predictive accuracy or learning effectiveness.
func TestReadingAndFeedbackSensitivity(t *testing.T) {
	type row struct {
		Rho        float64 `json:"reading_discount"`
		Days       int     `json:"full_reading_days"`
		Correction float64 `json:"self_report_likelihood_ratio"`
		Mean       float64 `json:"mean_index"`
		Lower      float64 `json:"parameter_min"`
		Upper      float64 `json:"parameter_max"`
	}
	rows := []row{}
	for _, rho := range []float64{0.1, 0.25, 0.5} {
		for _, days := range []int{1, 5, 10} {
			for _, ratio := range []float64{1, 2, 4, 8} {
				r := row{Rho: rho, Days: days, Correction: ratio, Lower: 1}
				for _, prior := range []float64{0.15, 0.25, 0.4} {
					for _, learn := range []float64{0.15, 0.3, 0.45} {
						p := estimator.KnowledgeParameters{Initial: prior, Learn: learn, Guess: .25, Slip: .15}
						m := prior
						for day := 0; day < days; day++ {
							var err error
							m, err = p.ReadOpportunity(m, math.Log1p(1/float64(1+day))/math.Ln2, rho)
							if err != nil {
								t.Fatal(err)
							}
						}
						m, err := estimator.CorrectBelief(m, ratio)
						if err != nil {
							t.Fatal(err)
						}
						r.Mean += m / 9
						r.Lower = math.Min(r.Lower, m)
						r.Upper = math.Max(r.Upper, m)
					}
				}
				if r.Lower >= r.Upper || r.Mean <= r.Lower || r.Mean >= r.Upper {
					t.Fatalf("parameter uncertainty was collapsed: %+v", r)
				}
				rows = append(rows, r)
			}
		}
	}
	if path := os.Getenv("LEARNING_SENSITIVITY_REPORT"); path != "" {
		data, err := json.MarshalIndent(struct {
			Kind  string `json:"kind"`
			Model string `json:"model"`
			Rows  []row  `json:"rows"`
		}{"historical_reading_model_sensitivity_not_learning_outcomes", "bkt-reading-envelope-v2", rows}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelUtilityAblations(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	pages := modelPages()
	read := types.LearningEvent{ID: "read", Slug: "a", Type: types.LearningEventWikiDeepRead, ContentVersion: nodeContentVersion(pages[0]), OccurredAt: now.Add(-time.Minute)}
	known := types.LearningEvent{ID: "known", Slug: "a", Type: types.LearningEventNodeKnown, ContentVersion: nodeContentVersion(pages[0]), OccurredAt: now.Add(-time.Minute)}
	cold := deriveNodeEstimates(pages, nil, nil, nil, nil, now)
	warm := deriveNodeEstimates(pages, []types.LearningEvent{read}, nil, nil, nil, now)
	corrected := deriveNodeEstimates(pages, []types.LearningEvent{known}, nil, nil, nil, now)
	in := planInput{Pages: map[string]string{"a": "A", "b": "B"}, PageMinutes: map[string]int{"a": 1, "b": 1}, DocOrder: map[string]int{"a": 0, "b": 1}, GoalsResolved: true, IncludeExploration: true, TimeBudgetMinutes: 1}
	first := func(e map[string]*interfaces.LearningEstimate) string {
		in.Estimates = e
		p := planShortPath(in)
		if len(p.Steps) != 1 {
			t.Fatalf("expected a single budgeted step: %+v", p)
		}
		return p.Steps[0].Slug
	}
	if first(cold) != "a" || first(warm) != "b" || first(corrected) != "b" {
		t.Fatal("reading estimates and optional correction must each affect ordering without mandatory confirmation")
	}
	// With comparable learning value, relevance can change a tied order.
	in.Relevance = map[string]pathRelevance{"b": {Priority: 1000}}
	if first(cold) != "b" {
		t.Fatal("removing personalization did not change the tied case")
	}
	// Even arbitrarily large interest must not suppress a much better learning
	// opportunity: the relevance factor is bounded, rather than a raw counter.
	in.Relevance = map[string]pathRelevance{"a": {Priority: 1000}}
	if first(warm) != "b" {
		t.Fatal("interest overwhelmed the larger expected learning gain")
	}
}
