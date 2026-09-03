package main

// optimize — the offline weight-calibration loop, deliberately modelled on
// how mature spaced-repetition projects fit their parameters:
//
//   - Anki/FSRS fits its 17-21 parameters against the user's own review
//     history by minimizing log-loss of predicted recall probability, with
//     parameter bounds and a "not enough reviews → keep defaults" rule.
//   - Duolingo's half-life regression fits feature weights by regression
//     over practice outcomes, with regularization toward sensible priors.
//
// Our analogue: every quiz attempt is a labelled observation — p_eff right
// before the answer is the model's predicted probability of success, and
// is_correct is the outcome. The objective is exactly FSRS's:
//
//	L(θ) = −Σ [ y·ln p_θ + (1−y)·ln(1−p_θ) ]  +  λ·Σ ((θ−θ₀)/θ₀)²
//
// where θ₀ are the shipped (theory-set) constants and the second term is a
// shrinkage prior: with little data the fit collapses back to the current
// values — the cold-start answer small-sample fitting needs. The optimizer
// is a bounded multiplicative coordinate descent (zero dependencies), and
// the search space is fenced by two kinds of rails:
//
//   1. hard bounds + the psychological ordering (direct evidence must stay
//      worth more than indirect evidence) — data may move magnitudes, never
//      the theory-imposed ranking;
//   2. a ±50% neighbourhood of the shipped values, mirroring Anki's
//      parameter clamping: calibration, not reinvention.
//
// The output is a report and a ready-to-paste `-w` override line for replay
// validation. Adopting a calibrated table remains a human decision that
// lands as a constants.go value change; nothing here touches the online
// path, and folding in this process uses refolded (recomputed) weights, so
// frozen history is never rewritten.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	learning "github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
)

// ---- parameter table ----

type optParam struct {
	Name  string
	Ptr   *float64
	Lo    float64 // hard lower bound (inclusive)
	Hi    float64 // hard upper bound (inclusive)
	Start float64 // value at optimizer start (the shrinkage anchor θ₀)
}

// optParamTable fences the twelve calibration-grade constants. Event
// weights carry the ordering rails; decay/stability parameters carry the
// FSRS-style curve shape. Thresholds, gates and ε are deliberately absent:
// they are product semantics, not fitting targets.
func optParamTable() []optParam {
	return []optParam{
		{Name: "WeightQuizCorrect", Ptr: &learning.WeightQuizCorrect, Lo: 0.5, Hi: 4.0},
		{Name: "WeightCrossRef", Ptr: &learning.WeightCrossRef, Lo: 0.2, Hi: 3.0},
		{Name: "WeightAnswerCite", Ptr: &learning.WeightAnswerCite, Lo: 0.2, Hi: 2.0},
		{Name: "WeightTopicSignal", Ptr: &learning.WeightTopicSignal, Lo: 0.1, Hi: 1.0},
		{Name: "WeightReAsk", Ptr: &learning.WeightReAsk, Lo: -2.0, Hi: -0.1},
		{Name: "WeightQuizWrong", Ptr: &learning.WeightQuizWrong, Lo: -4.0, Hi: -0.2},
		{Name: "QuizRepeatDecay", Ptr: &learning.QuizRepeatDecay, Lo: 0.1, Hi: 0.9},
		{Name: "BackfillDiscount", Ptr: &learning.BackfillDiscount, Lo: 0.1, Hi: 1.0},
		{Name: "StabilityBaseDays", Ptr: &learning.StabilityBaseDays, Lo: 2.0, Hi: 60.0},
		{Name: "StabilityGrowth", Ptr: &learning.StabilityGrowth, Lo: 0.05, Hi: 1.5},
		{Name: "StabilitySaturationExp", Ptr: &learning.StabilitySaturationExp, Lo: 0.1, Hi: 1.0},
		{Name: "StabilityLapseShrink", Ptr: &learning.StabilityLapseShrink, Lo: 0.5, Hi: 0.98},
	}
}

// optNeighbourhood is the ±relative envelope around the start value, the
// calibration analogue of FSRS's parameter clamping.
const optNeighbourhood = 0.5

// orderingOK enforces the theory-imposed ranking: data may tune magnitudes
// but never conclude that reading beats answering.
func orderingOK() bool {
	return learning.WeightQuizCorrect > learning.WeightCrossRef &&
		learning.WeightCrossRef > learning.WeightAnswerCite &&
		learning.WeightAnswerCite > learning.WeightTopicSignal &&
		learning.WeightTopicSignal > 0 &&
		learning.WeightReAsk < 0 &&
		learning.WeightQuizWrong < learning.WeightReAsk
}

func paramsFeasible(params []optParam) bool {
	if !orderingOK() {
		return false
	}
	for _, p := range params {
		v := *p.Ptr
		lo, hi := p.Lo, p.Hi
		// The ±50% neighbourhood applies inside the hard bounds.
		if p.Start > 0 {
			lo = math.Max(lo, p.Start*(1-optNeighbourhood))
			hi = math.Min(hi, p.Start*(1+optNeighbourhood))
		} else {
			lo = math.Max(lo, p.Start*(1+optNeighbourhood)) // negative: ×0.5 moves toward 0
			hi = math.Min(hi, p.Start*(1-optNeighbourhood)) // ×1.5 of |w|
		}
		if v < lo || v > hi {
			return false
		}
	}
	return true
}

// ---- input ----

type optInput struct {
	Events   []types.LearningEvent       `json:"events"`
	Attempts []types.LearningQuizAttempt `json:"quiz_attempts"`
}

func loadOptInput(path string) (optInput, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return optInput{}, err
	}
	var in optInput
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Events) == 0 {
		// Full ExportPayload shape (fields nested under .data).
		var payload struct {
			Data optInput `json:"data"`
		}
		if err2 := json.Unmarshal(raw, &payload); err2 == nil && len(payload.Data.Events) > 0 {
			in = payload.Data
		} else if err != nil {
			return optInput{}, fmt.Errorf("export parse: %w (also tried .data)", err)
		}
	}
	if len(in.Events) == 0 {
		return optInput{}, fmt.Errorf("export contains no events")
	}
	sort.Slice(in.Events, func(i, j int) bool { return in.Events[i].OccurredAt.Before(in.Events[j].OccurredAt) })
	sort.Slice(in.Attempts, func(i, j int) bool { return in.Attempts[i].AnsweredAt.Before(in.Attempts[j].AnsweredAt) })
	return in, nil
}

// ---- objective ----

type optGroupKey struct {
	Subject string
	Slug    string
}

const optProbClamp = 1e-4

// quizLogLoss folds the event stream per (subject, slug) with the currently
// set package weights (refolded, quiz repeat decay included) and scores the
// labelled quiz attempts: p_eff immediately before each answer is the
// predicted success probability, is_correct is the outcome. This is the
// FSRS review-fitting loss, one level up.
func quizLogLoss(in optInput) (float64, int) {
	eventsBy := map[optGroupKey][]types.LearningEvent{}
	for _, ev := range in.Events {
		eventsBy[optGroupKey{ev.SubjectID, ev.Slug}] = append(eventsBy[optGroupKey{ev.SubjectID, ev.Slug}], ev)
	}
	attemptsBy := map[optGroupKey][]types.LearningQuizAttempt{}
	for _, a := range in.Attempts {
		attemptsBy[optGroupKey{a.SubjectID, a.Slug}] = append(attemptsBy[optGroupKey{a.SubjectID, a.Slug}], a)
	}

	loss := 0.0
	n := 0
	for key, evs := range eventsBy {
		attempts := attemptsBy[key]
		if len(attempts) == 0 {
			continue
		}
		sort.Slice(evs, func(i, j int) bool { return evs[i].OccurredAt.Before(evs[j].OccurredAt) })
		state := learning.FoldState{}
		quizSeen := map[string]int{}
		ai := 0
		for _, ev := range evs {
			// Fold everything strictly before the next answer; the answer's
			// own quiz event happens at/after the attempt it belongs to.
			for ai < len(attempts) && ev.OccurredAt.After(attempts[ai].AnsweredAt) {
				p := clampProb(learning.EffectiveP(state, attempts[ai].AnsweredAt))
				y := 0.0
				if attempts[ai].IsCorrect {
					y = 1.0
				}
				loss += -(y*math.Log(p) + (1-y)*math.Log(1-p))
				n++
				ai++
			}
			w := refoldWeight(ev, quizSeen)
			state = learning.FoldEvent(state, learning.Event{Type: ev.Type, Weight: w, OccurredAt: ev.OccurredAt})
		}
		for ; ai < len(attempts); ai++ {
			p := clampProb(learning.EffectiveP(state, attempts[ai].AnsweredAt))
			y := 0.0
			if attempts[ai].IsCorrect {
				y = 1.0
			}
			loss += -(y*math.Log(p) + (1-y)*math.Log(1-p))
			n++
		}
	}
	// Attempts on slugs with no events at all still carry information via
	// the empty-state prior (p=0.5); they are constant w.r.t. θ, so they can
	// be skipped for optimization purposes.
	return loss, n
}

func clampProb(p float64) float64 {
	if p < optProbClamp {
		return optProbClamp
	}
	if p > 1-optProbClamp {
		return 1 - optProbClamp
	}
	return p
}

// shrinkage pulls every parameter toward its start value with λ=1: one
// parameter drifting the full ±50% costs 0.25, which the log-loss must
// actually out-earn. This is the Duolingo-HLR-style regularization and the
// small-sample cold-start guard in one term.
func shrinkage(params []optParam) float64 {
	s := 0.0
	for _, p := range params {
		d := (*p.Ptr - p.Start) / p.Start
		s += d * d
	}
	return s
}

func objective(in optInput, params []optParam) float64 {
	loss, _ := quizLogLoss(in)
	return loss + shrinkage(params)
}

// ---- optimizer: bounded multiplicative coordinate descent ----

// fitResult carries everything the printer/reporter needs out of a fit.
type fitResult struct {
	Params          []optParam
	After           []float64 // fitted values aligned with Params (live vars are restored on return)
	PlainBefore     float64   // quiz log-loss at shipped values, no shrinkage
	PlainAfter      float64   // quiz log-loss at fitted values, no shrinkage
	Objective       float64   // final objective incl. shrinkage
	ObjectiveBefore float64
	Evals           int
	Attempts        int
	Improvement     float64
	Recommendation  string
}

// runFit is the calibration core: snapshot the shipped values, search the
// fenced space, verify safety, compute the adopt/keep verdict, and restore
// the shipped values before returning (callers read the fitted values from
// the returned params).
func runFit(in optInput, verbose bool) (fitResult, error) {
	params := optParamTable()
	for i := range params {
		params[i].Start = *params[i].Ptr
	}
	snapshot := make([]float64, len(params))
	for i, p := range params {
		snapshot[i] = *p.Ptr
	}
	restore := func() {
		for i, p := range params {
			*p.Ptr = snapshot[i]
		}
	}
	defer restore()

	objectiveBefore := objective(in, params)
	obj, evals := coordinateDescent(in, params, verbose)

	if !paramsFeasible(params) {
		return fitResult{}, fmt.Errorf("optimizer produced infeasible parameters (ordering/bounds violated)")
	}
	if !gateSafetyOK() {
		return fitResult{}, fmt.Errorf("fitted parameters broke the direct-evidence gate personas")
	}

	fitted := make([]float64, len(params))
	for i, p := range params {
		fitted[i] = *p.Ptr
	}
	apply := func(vals []float64) {
		for i, p := range params {
			*p.Ptr = vals[i]
		}
	}
	snapshotCopy := append([]float64(nil), snapshot...)
	apply(snapshotCopy)
	plainBefore, attempts := quizLogLoss(in)
	apply(fitted)
	plainAfter, _ := quizLogLoss(in)

	improvement := 0.0
	if plainBefore > 0 {
		improvement = (plainBefore - plainAfter) / plainBefore
	}
	rec := "keep current values (improvement within noise)"
	if improvement >= 0.01 {
		rec = "candidate for adoption — human review required"
	}
	after := make([]float64, len(params))
	for i, p := range params {
		after[i] = *p.Ptr
	}
	return fitResult{
		Params: params, After: after, PlainBefore: plainBefore, PlainAfter: plainAfter,
		ObjectiveBefore: objectiveBefore, Objective: obj,
		Evals: evals, Attempts: attempts, Improvement: improvement,
		Recommendation: rec,
	}, nil
}

func runOptimize(exportPath, reportPath string, verbose bool) error {
	in, err := loadOptInput(exportPath)
	if err != nil {
		return err
	}
	res, err := runFit(in, verbose)
	if err != nil {
		return err
	}

	fmt.Println("== learning-bench optimize (offline weight calibration, FSRS-style log-loss) ==")
	fmt.Printf("  labelled quiz attempts: %d   events: %d\n", res.Attempts, len(in.Events))
	fmt.Printf("  quiz log-loss: %.4f -> %.4f (%.1f%% better)   objective incl. shrinkage: %.4f -> %.4f\n",
		res.PlainBefore, res.PlainAfter, res.Improvement*100, res.ObjectiveBefore, res.Objective)
	fmt.Printf("  evaluations: %d\n", res.Evals)
	fmt.Println("  parameters (before -> after):")
	for i, p := range res.Params {
		fmt.Printf("    %-24s %6.3f -> %6.3f\n", p.Name, p.Start, res.After[i])
	}
	fmt.Printf("  verdict: %s\n", res.Recommendation)
	fmt.Println("  replay validation line:")
	fmt.Println("    " + overrideLine(res.Params, res.After))

	if reportPath != "" {
		rep := map[string]interface{}{
			"attempts":         res.Attempts,
			"events":           len(in.Events),
			"logloss_before":   res.PlainBefore,
			"logloss_after":    res.PlainAfter,
			"improvement":      res.Improvement,
			"objective_before": res.ObjectiveBefore,
			"objective_after":  res.Objective,
			"evaluations":      res.Evals,
			"recommendation":   res.Recommendation,
			"params":           paramReport(res.Params, res.After),
		}
		blob, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(reportPath, blob, 0o644); err != nil {
			return err
		}
		fmt.Printf("  report written: %s\n", reportPath)
	}
	return nil
}

func coordinateDescent(in optInput, params []optParam, verbose bool) (float64, int) {
	best := objective(in, params)
	evals := 1
	step := 0.25
	for pass := 0; pass < 4 && step >= 0.03; pass++ {
		improved := false
		for i := range params {
			bestVal := *params[i].Ptr
			for _, f := range []float64{1 + step, 1 + step/2, 1 - step/2, 1 - step} {
				cand := candidateValue(params[i], f)
				*params[i].Ptr = cand
				if paramsFeasible(params) {
					obj := objective(in, params)
					evals++
					if obj < best-1e-9 {
						best = obj
						bestVal = cand
						improved = true
					}
				}
			}
			*params[i].Ptr = bestVal
		}
		if verbose {
			fmt.Printf("    pass %d step %.3f objective %.4f\n", pass+1, step, best)
		}
		if !improved {
			step /= 2
		}
	}
	return best, evals
}

// candidateValue scales a parameter multiplicatively; for negative weights
// scaling the magnitude means dividing by the factor.
func candidateValue(p optParam, factor float64) float64 {
	if p.Start >= 0 {
		return p.Start * factor
	}
	return p.Start / factor
}

func gateSafetyOK() bool {
	now := time.Now()
	oneFact := []learning.DirectQuizFact{{ItemID: "q1", FirstCorrectAt: now.Add(-24 * time.Hour)}}
	twoFacts := []learning.DirectQuizFact{
		{ItemID: "q1", FirstCorrectAt: now.Add(-5 * 24 * time.Hour)},
		{ItemID: "q2", FirstCorrectAt: now.Add(-1 * 24 * time.Hour)},
	}
	// The gate only ever caps (never promotes), so the mastered-unlock check
	// asks "would a mastered verdict survive the cap with two cross-gap facts".
	return learning.ApplyDirectGate(learning.LevelMastered, nil, now) == learning.LevelTouched &&
		learning.ApplyDirectGate(learning.LevelMastered, oneFact, now) == learning.LevelFamiliar &&
		learning.ApplyDirectGate(learning.LevelMastered, twoFacts, now) == learning.LevelMastered
}

func overrideLine(params []optParam, after []float64) string {
	line := "-w "
	for i, p := range params {
		if i > 0 {
			line += ","
		}
		line += fmt.Sprintf("%s=%.4f", p.Name, after[i])
	}
	return line
}

type paramDelta struct {
	Name   string  `json:"name"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

func paramReport(params []optParam, after []float64) []paramDelta {
	out := make([]paramDelta, 0, len(params))
	for i, p := range params {
		out = append(out, paramDelta{Name: p.Name, Before: p.Start, After: after[i]})
	}
	return out
}
