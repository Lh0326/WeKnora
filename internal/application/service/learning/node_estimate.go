package learning

import (
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/learning/estimator"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const NodeEstimateVersion = "page-evidence-separated-v3"

type estimateObservation struct {
	id, kind          string
	objective, family string
	at                time.Time
	dose              float64
	correct           bool
}

// deriveNodeEstimates uses a small explicit prior ensemble. Public math-task
// parameters are deliberately not represented as fitted enterprise-reading
// parameters. Reading and self-report change navigation only. Independent
// answers update each objective separately; the page summary averages only
// observed objectives, never certifying the whole page from one objective.
// All input belongs to the already-authorized user/tenant/KB service snapshot.
func deriveNodeEstimates(pages []*types.WikiPage, events []types.LearningEvent, attempts []types.LearningQuizAttempt, tasks []types.LearningTaskAttempt, objectives []types.LearningObjective, now time.Time) map[string]*interfaces.LearningEstimate {
	bySlug := map[string][]types.LearningEvent{}
	for _, e := range events {
		if !e.OccurredAt.IsZero() && !e.OccurredAt.After(now) {
			bySlug[e.Slug] = append(bySlug[e.Slug], e)
		}
	}
	definitions := map[string]types.LearningObjective{}
	objectiveTotal, objectiveVerified := map[string]int{}, map[string]int{}
	currentAttempts := []types.LearningQuizAttempt{}
	currentTasks := []types.LearningTaskAttempt{}
	for _, a := range attempts {
		if !a.AnsweredAt.After(now) {
			currentAttempts = append(currentAttempts, a)
		}
	}
	for _, a := range tasks {
		if !a.SubmittedAt.After(now) {
			currentTasks = append(currentTasks, a)
		}
	}
	for _, o := range objectives {
		if o.Status == types.LearningObjectiveStatusPublished {
			definitions[o.ID] = o
			objectiveTotal[o.Slug]++
			if DeriveObjectiveState(o, currentAttempts, nil, currentTasks).State == types.ObjectiveStateVerified {
				objectiveVerified[o.Slug]++
			}
		}
	}
	answerBySlug := map[string][]estimateObservation{}
	seenAnswers := map[string]bool{}
	for _, a := range familyIndependentAttempts(attempts) {
		o, ok := definitions[a.ObjectiveID]
		if !ok || !a.Eligible || a.ContentVersion != o.ContentVersion || a.AnsweredAt.After(now) || a.AnsweredAt.IsZero() || a.ID == "" || seenAnswers[a.ID] || a.ChosenKey == "" {
			continue
		}
		seenAnswers[a.ID] = true
		facts := DeriveObjectiveState(o, []types.LearningQuizAttempt{a}, nil, nil).Evidence
		if facts.EligiblePasses+facts.EligibleFailures == 0 {
			continue
		}
		answerBySlug[o.Slug] = append(answerBySlug[o.Slug], estimateObservation{id: a.ID, kind: "answer", objective: o.ID, family: a.FamilyID, at: a.AnsweredAt, correct: a.IsCorrect})
	}
	orderedTasks := append([]types.LearningTaskAttempt(nil), tasks...)
	sort.Slice(orderedTasks, func(i, j int) bool {
		if !orderedTasks[i].SubmittedAt.Equal(orderedTasks[j].SubmittedAt) {
			return orderedTasks[i].SubmittedAt.Before(orderedTasks[j].SubmittedAt)
		}
		return orderedTasks[i].ID < orderedTasks[j].ID
	})
	lastTask := map[string]time.Time{}
	for _, a := range orderedTasks {
		o, ok := definitions[a.ObjectiveID]
		if !ok || a.ObjectiveVersion != o.ContentVersion || a.SubmittedAt.IsZero() || a.SubmittedAt.After(now) || a.ID == "" || seenAnswers["task:"+a.ID] {
			continue
		}
		seenAnswers["task:"+a.ID] = true
		key := a.ObjectiveID + ":" + a.FamilyID
		previous := lastTask[key]
		lastTask[key] = a.SubmittedAt
		if !previous.IsZero() && a.SubmittedAt.Sub(previous) < ReAskWindowHours*time.Hour {
			continue
		}
		facts := DeriveObjectiveState(o, nil, nil, []types.LearningTaskAttempt{a}).Evidence
		if facts.EligiblePasses+facts.EligibleFailures == 0 {
			continue
		}
		answerBySlug[o.Slug] = append(answerBySlug[o.Slug], estimateObservation{id: "task:" + a.ID, kind: "answer", objective: o.ID, family: a.FamilyID, at: a.SubmittedAt, correct: a.IsPassed})
	}
	out := map[string]*interfaces.LearningEstimate{}
	for _, page := range pages {
		if page == nil || page.Slug == "" {
			continue
		}
		version := nodeContentVersion(page)
		observations := append([]estimateObservation(nil), answerBySlug[page.Slug]...)
		seen := map[string]bool{}
		recalls := []estimator.RecallFact{}
		contact := false
		for _, e := range bySlug[page.Slug] {
			if e.ID != "" && seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			if e.ContentVersion != "" && e.ContentVersion != version {
				continue
			}
			switch e.Type {
			case types.LearningEventWikiToolRead, types.LearningEventWikiDeepRead, types.LearningEventNodeRead:
				contact = true
				dose := 0.25
				if e.Type == types.LearningEventWikiDeepRead || e.Type == types.LearningEventNodeRead {
					dose = 1
				}
				observations = append(observations, estimateObservation{id: e.ID, kind: "read", at: e.OccurredAt, dose: dose})
			case types.LearningEventAnswerCite, types.LearningEventCrossRef, types.LearningEventReAsk:
				contact = true // citation and repeated questioning are NOT answer labels
			case types.LearningEventNodeKnown, types.LearningEventNodeReview:
				if e.ContentVersion == version {
					kind := "known"
					if e.Type == types.LearningEventNodeReview {
						kind = "difficult"
					}
					observations = append(observations, estimateObservation{id: e.ID, kind: kind, at: e.OccurredAt})
				}
			}
			if rating := map[string]int{"again": 1, "hard": 2, "good": 3, "easy": 4}[reviewAction(e.Type)]; rating > 0 && e.ContentVersion == version {
				var record reviewRecord
				if json.Unmarshal(e.ReviewData, &record) == nil && supportedReviewPolicy(record.Policy) && record.Sequence > 0 {
					recalls = append(recalls, estimator.RecallFact{ID: e.ID, ContentVersion: version, At: e.OccurredAt, Rating: rating})
				}
			}
		}
		sort.Slice(observations, func(i, j int) bool {
			if !observations[i].at.Equal(observations[j].at) {
				return observations[i].at.Before(observations[j].at)
			}
			if observations[i].kind != observations[j].kind {
				return observations[i].kind < observations[j].kind
			}
			if observations[i].correct != observations[j].correct {
				return !observations[i].correct
			}
			return observations[i].id < observations[j].id
		})
		params, priors := []estimator.KnowledgeParameters{}, []float64{}
		for _, prior := range []float64{0.15, 0.25, 0.4} {
			params = append(params, estimator.KnowledgeParameters{Initial: prior, Learn: 0.1, Guess: 0.25, Slip: 0.15}) // Observe(false) disables learning transitions.
			priors = append(priors, prior)
		}
		beliefByObjective := map[string][]float64{}
		seenFamilies := map[string]bool{}
		e := &interfaces.LearningEstimate{ContentVersion: version, ModelVersion: NodeEstimateVersion, Level: "unseen", Basis: "prior", Lower: 1}
		lastCorrection := ""
		needsSupport := false
		dailyDose := map[string]float64{}
		for _, ob := range observations {
			coverage := ob.dose
			if ob.kind == "read" {
				// Absorb repeated tiers at their original timestamps. Replacing an
				// earlier normal read with a later deep read would rewrite the
				// prediction made for an answer between those two observations.
				day := ob.at.UTC().Format("2006-01-02")
				previous := dailyDose[day]
				dailyDose[day] = math.Max(previous, coverage)
				ob.dose = math.Max(0, coverage-previous)
				if ob.dose == 0 && !(needsSupport && coverage >= 1) {
					continue
				}
			}
			if ob.kind == "known" || ob.kind == "difficult" {
				if ob.kind != lastCorrection {
					e.Corrections++
				}
				lastCorrection = ob.kind
				needsSupport = ob.kind == "difficult"
				e.SelfReport = ob.kind
			}
			if ob.kind == "answer" {
				key := ob.objective + "\x00" + ob.family
				if seenFamilies[key] {
					continue
				}
				seenFamilies[key] = true
				e.LastAnswerPrediction = 0
				beliefs := beliefByObjective[ob.objective]
				if beliefs == nil {
					beliefs = append([]float64(nil), priors...)
				}
				for i, p := range params {
					update, _ := p.Observe(beliefs[i], ob.correct, false)
					e.LastAnswerPrediction += update.PredictedCorrect / float64(len(params))
					beliefs[i] = update.AfterLearning
				}
				beliefByObjective[ob.objective] = beliefs
			}
			if ob.kind == "read" {
				e.Opportunities += ob.dose
				e.Coverage = math.Max(e.Coverage, coverage)
				e.LastStudyAt = ob.at
				if coverage >= 1 {
					needsSupport = false // revisited, not proof that difficulty was resolved
				}
			}
			if ob.kind == "answer" {
				e.Answers++
				e.LastStudyAt = ob.at
				e.LastAnswerAt = ob.at
				needsSupport = !ob.correct
				lastCorrection = "" // later independent evidence permits fresh correction
				e.SelfReport = ""
			}
		}
		e.PerformanceObserved = e.Answers > 0
		beliefs := []float64{}
		ids := make([]string, 0, len(beliefByObjective))
		for id := range beliefByObjective {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			beliefs = append(beliefs, beliefByObjective[id]...)
		}
		if len(beliefs) == 0 {
			beliefs = priors
		}
		for _, m := range beliefs {
			e.Familiarity += m / float64(len(beliefs))
			e.Lower = math.Min(e.Lower, m)
			e.Upper = math.Max(e.Upper, m)
		}
		if contact || len(observations) > 0 {
			e.Level, e.Basis = "introduced", "reading"
		}
		if e.Coverage >= 1 {
			e.Level = "developing"
		}
		if e.Answers > 0 {
			e.Basis = "answers"
		}
		if objectiveTotal[page.Slug] > 0 && objectiveVerified[page.Slug] == objectiveTotal[page.Slug] {
			e.Level = "familiar"
		}
		if e.SelfReport == "known" && e.Level != "familiar" {
			e.Level = "self_reported"
		}
		if needsSupport {
			e.Level = "review"
		}
		// Explicit navigation priorities, not an estimate of learning gain.
		e.ReadPriority = 1
		if e.Coverage > 0 {
			e.ReadPriority = .75
		}
		if e.Coverage >= 1 || e.SelfReport == "known" || e.Level == "familiar" {
			e.ReadPriority = 0
		}
		if needsSupport {
			e.ReadPriority = 1.2
		}
		if memory, err := estimator.ProjectRecall(recalls, version, now, 0.9); err == nil && memory != nil {
			if e.Level == "unseen" {
				e.Level = "introduced"
			}
			if e.Answers == 0 && e.Opportunities == 0 {
				e.Basis = "recall"
			}
			e.Memory = &interfaces.LearningMemoryEstimate{ModelVersion: memory.ModelVersion, Stability: memory.Stability, Difficulty: memory.Difficulty, Retrievability: memory.Retrievability, DueAt: memory.DueAt, Observations: memory.Observations}
			active := projectRecall(bySlug[page.Slug])
			if active != nil && active.Active && (memory.Retrievability < 0.9 || recallStatus(active, version, now).Due) {
				e.Level = "review"
			}
		}
		out[page.Slug] = e
	}
	return out
}

func attachNodeEstimates(nodes []interfaces.LearningNodeView, estimates map[string]*interfaces.LearningEstimate) {
	for i := range nodes {
		nodes[i].Estimate = estimates[nodes[i].Slug]
	}
}
