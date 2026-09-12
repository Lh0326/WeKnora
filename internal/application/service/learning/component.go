package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/service/learning/estimator"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const componentModelVersion = "kc-evidence-separated-v2"
const componentRecallCooldown = 10 * time.Minute

type componentFact struct {
	ComponentID  string  `json:"component_id"`
	Title        string  `json:"title"`
	Action       string  `json:"action"`
	OperationID  string  `json:"operation_id"`
	RequestHash  string  `json:"request_hash"`
	SessionID    string  `json:"session_id"`
	CheckID      string  `json:"check_id,omitempty"`
	Family       string  `json:"family,omitempty"`
	Correct      bool    `json:"correct"`
	Eligible     bool    `json:"eligible"`
	Rating       int     `json:"rating,omitempty"`
	Prediction   float64 `json:"prediction,omitempty"`
	ModelVersion string  `json:"model_version"`
}

func componentHash(s string) string      { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func componentNormalize(s string) string { return strings.Join(strings.Fields(s), " ") }
func componentIdentity(tenant uint64, kb, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("weknora:kc:%d:%s:%s", tenant, kb, key))).String()
}

// ValidateComponentPack checks provenance and structural boundaries, not semantic
// truth. Packs still require authored examples/checks and an explicit provenance.
// No LLM self-rating is treated as a quality guarantee.
func ValidateComponentPack(tenant uint64, kb string, defs []types.ComponentDefinition, pages []*types.WikiPage) ([]types.LearningComponent, error) {
	bad := func(reason string) ([]types.LearningComponent, error) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidLearningRequest, reason)
	}
	if tenant == 0 || kb == "" || len(defs) == 0 || len(defs) > 40 {
		return bad("a component pack needs 1–40 definitions and a KB scope")
	}
	sources := map[string]*types.WikiPage{}
	for _, p := range pages {
		if p != nil && !p.DeletedAt.Valid && p.TenantID == tenant && p.KnowledgeBaseID == kb && p.Status != types.WikiPageStatusArchived {
			sources[p.Slug] = p
		}
	}
	keys := map[string]bool{}
	boundaries := map[string]bool{}
	for _, d := range defs {
		if d.Key == "" || len(d.Key) > 100 || keys[d.Key] {
			return bad("component keys must be unique and stable")
		}
		keys[d.Key] = true
		boundary := componentNormalize(d.Condition) + "\n" + componentNormalize(d.Goal)
		if boundaries[boundary] {
			return bad("duplicate condition and goal; combine sources instead")
		}
		boundaries[boundary] = true
	}
	out := make([]types.LearningComponent, 0, len(defs))
	dependencies := map[string][]string{}
	for _, original := range defs {
		raw, _ := json.Marshal(original)
		var d types.ComponentDefinition
		_ = json.Unmarshal(raw, &d)
		// Generated candidates are deliberately not an importable learning pack.
		// Empty status retains existing explicitly authored pack compatibility.
		if d.MaterialStatus != "" && (d.MaterialStatus != "ready" || utf8.RuneCountInString(strings.TrimSpace(d.ReviewNote)) < 20) {
			return bad("candidate material needs a source review note before import: " + d.Key)
		}
		if d.Title == "" || utf8.RuneCountInString(d.Title) > 100 || d.Topic == "" || len(d.Topic) > 120 || utf8.RuneCountInString(d.Condition) < 4 || utf8.RuneCountInString(d.Goal) < 6 || utf8.RuneCountInString(d.Explanation) < 40 || utf8.RuneCountInString(d.Example) < 20 || d.Minutes < 1 || d.Minutes > 10 || d.Provenance == "" {
			return bad("incomplete condition, goal, explanation, example, time or provenance: " + d.Key)
		}
		if len(d.Sources) == 0 || len(d.Sources) > 8 || len(d.Checks) > 3 {
			return bad("each component requires sources and at most 3 authored checks: " + d.Key)
		}
		if d.Checks == nil {
			d.Checks = []types.ComponentCheck{}
		}
		for i, ref := range d.Sources {
			p := sources[ref.Slug]
			if p == nil || utf8.RuneCountInString(ref.Quote) < 12 || ref.Role == "" || !strings.Contains(componentNormalize(p.Content), componentNormalize(ref.Quote)) {
				return bad("source quote unavailable or outside this KB: " + d.Key)
			}
			hash := componentHash(componentNormalize(p.Content))
			if ref.Hash != "" && ref.Hash != hash {
				return bad("source version changed since material preparation: " + d.Key)
			}
			d.Sources[i].Hash = hash
		}
		ids, families := map[string]bool{}, map[string]bool{}
		for _, check := range d.Checks {
			if check.ID == "" || ids[check.ID] || check.Family == "" || families[check.Family] || len(check.Options) != 4 || check.Question == "" || check.Explanation == "" || check.Options[check.Answer] == "" {
				return bad("check must have four choices, an answer, explanation and independent family: " + d.Key)
			}
			for _, key := range []string{"A", "B", "C", "D"} {
				if check.Options[key] == "" {
					return bad("check options must use A–D")
				}
			}
			ids[check.ID] = true
			families[check.Family] = true
		}
		for _, key := range append(append([]string{}, d.Prerequisites...), d.Related...) {
			if key == d.Key || !keys[key] {
				return bad("unknown or self-referencing component relationship: " + d.Key)
			}
		}
		dependencies[d.Key] = d.Prerequisites
		encoded, _ := json.Marshal(d)
		out = append(out, types.LearningComponent{ID: componentIdentity(tenant, kb, d.Key), TenantID: tenant, KnowledgeBaseID: kb, Version: componentHash(string(encoded)), Definition: d})
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(key string) bool {
		if visiting[key] {
			return false
		}
		if done[key] {
			return true
		}
		visiting[key] = true
		for _, p := range dependencies[key] {
			if !visit(p) {
				return false
			}
		}
		visiting[key] = false
		done[key] = true
		return true
	}
	for key := range keys {
		if !visit(key) {
			return bad("prerequisite cycle")
		}
	}
	return out, nil
}
func (s *Service) componentRepo() (interfaces.LearningComponentRepository, error) {
	r, ok := s.repo.(interfaces.LearningComponentRepository)
	if !ok {
		return nil, fmt.Errorf("component repository unavailable")
	}
	return r, nil
}
func (s *Service) ImportComponents(ctx context.Context, kb string, defs []types.ComponentDefinition) (int, error) {
	scope, err := resolveReadScope(ctx, kb)
	if err != nil {
		return 0, err
	}
	r, err := s.componentRepo()
	if err != nil {
		return 0, err
	}
	pages, err := s.wikiRepo.ListAll(ctx, kb)
	if err != nil {
		return 0, err
	}
	rows, err := ValidateComponentPack(scope.TenantID, kb, defs, pages)
	if err != nil {
		return 0, err
	}
	err = r.SaveComponents(ctx, rows)
	return len(rows), err
}
func componentAvailable(c types.LearningComponent, pages []*types.WikiPage) bool {
	for _, ref := range c.Definition.Sources {
		found := false
		for _, p := range pages {
			if p != nil && !p.DeletedAt.Valid && p.TenantID == c.TenantID && p.KnowledgeBaseID == c.KnowledgeBaseID && p.Status != types.WikiPageStatusArchived && p.Slug == ref.Slug && componentHash(componentNormalize(p.Content)) == ref.Hash {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(c.Definition.Sources) > 0
}
func componentFacts(c types.LearningComponent, events []types.LearningEvent, now time.Time) []types.LearningEvent {
	out := []types.LearningEvent{}
	seen := map[string]bool{}
	for _, e := range events {
		if e.TenantID != c.TenantID || e.KnowledgeBaseID != c.KnowledgeBaseID || e.Type != types.LearningEventComponent || e.Slug != "kc:"+c.ID || e.ContentVersion != c.Version || e.OccurredAt.After(now) || seen[e.ID] {
			continue
		}
		var f componentFact
		if json.Unmarshal(e.ReviewData, &f) != nil || f.ComponentID != c.ID {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OccurredAt.Equal(out[j].OccurredAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].OccurredAt.Before(out[j].OccurredAt)
	})
	return out
}
func projectComponent(c types.LearningComponent, events []types.LearningEvent, now time.Time) interfaces.ComponentState {
	state := interfaces.ComponentState{Level: "unseen", ReadCheckIDs: []string{}, Basis: "尚无该组件的学习动作；旧页面接触仅供回查，不推断本目标已会。"}
	priors := []float64{.1, .2, .35}
	means := append([]float64{}, priors...)
	families, seenChecks, readDays := map[string]bool{}, map[string]bool{}, map[string]bool{}
	lastReport, lastCheck := time.Time{}, time.Time{}
	contacted := false
	lastFailed := false
	recalls := []estimator.RecallFact{}
	sourceSlugs := map[string]bool{}
	legacySeen := map[string]bool{}
	for _, src := range c.Definition.Sources {
		sourceSlugs[src.Slug] = true
	}
	for _, e := range events {
		if e.TenantID == c.TenantID && e.KnowledgeBaseID == c.KnowledgeBaseID && !e.OccurredAt.After(now) && e.Type == types.LearningEventComponent && e.Slug == "kc:"+c.ID && e.ContentVersion != c.Version && !legacySeen[e.ID] {
			var old componentFact
			if json.Unmarshal(e.ReviewData, &old) == nil && old.ComponentID == c.ID {
				state.LegacyComponentTouches++
				legacySeen[e.ID] = true
			}
		}
		if e.TenantID == c.TenantID && e.KnowledgeBaseID == c.KnowledgeBaseID && !e.OccurredAt.After(now) && sourceSlugs[e.Slug] && types.IsHumanLearningEvent(e.Type) {
			state.LegacyTouches++
		}
	}
	for _, e := range componentFacts(c, events, now) {
		var f componentFact
		_ = json.Unmarshal(e.ReviewData, &f)
		contacted = true
		switch f.Action {
		case "read":
			state.LastReadAt = e.OccurredAt
			state.LastStudyAt = e.OccurredAt
			day := e.OccurredAt.UTC().Format("2006-01-02")
			if readDays[day] {
				continue
			}
			readDays[day] = true
			state.Reads++
			state.LastStudyAt = e.OccurredAt
			// Exposure advances navigation, not performance probability. Without
			// local calibration, elapsed reading cannot identify a learning rate.
		case "known", "difficult":
			state.SelfReport = f.Action
			state.LastStudyAt = e.OccurredAt
			lastReport = e.OccurredAt
		case "check":
			if !seenChecks[f.CheckID] {
				state.ReadCheckIDs = append(state.ReadCheckIDs, f.CheckID)
				seenChecks[f.CheckID] = true
			}
			if !f.Eligible || families[f.Family] {
				state.Practice++
				continue
			}
			families[f.Family] = true
			state.Checks++
			lastCheck = e.OccurredAt
			state.LastCheckAt = e.OccurredAt
			state.LastStudyAt = e.OccurredAt
			lastFailed = !f.Correct
			if f.Correct {
				state.Passes++
			}
			for i, m := range means {
				p := estimator.KnowledgeParameters{Initial: priors[i], Learn: .2, Guess: .25, Slip: .1}
				updated, _ := p.Observe(m, f.Correct, false)
				means[i] = updated.AfterObservation
			}
		case "recall":
			recalls = append(recalls, estimator.RecallFact{ID: e.ID, ContentVersion: c.Version, At: e.OccurredAt, Rating: f.Rating})
			state.LastRecallAt = e.OccurredAt
			state.LastStudyAt = e.OccurredAt
		}
	}
	// Reports express user intent; they never alter response evidence or the
	// experimental predictor. A later independent check supersedes the report.
	currentReport := state.SelfReport != "" && !lastReport.Before(lastCheck)
	if !currentReport {
		state.SelfReport = ""
	}
	state.PerformanceObserved = state.Checks > 0
	state.Familiarity = means[1]
	state.Lower = means[0]
	state.Upper = means[2]
	if !contacted && state.LegacyComponentTouches > 0 {
		state.Level = "touched"
		state.Basis = "曾接触该目标的旧版材料；当前版本尚无学习记录，旧版结果不会转成新版本已会。"
	}
	if contacted {
		state.Level = "touched"
		state.Basis = "已记录本目标的接触；阅读会更新进度，尚无独立检查支持对理解程度的判断。"
	}
	if state.Reads > 0 {
		state.Level = "learning"
		state.Basis = fmt.Sprintf("已记录 %d 个阅读日；可继续下一目标，阅读次数不会自动提高表现估计。", state.Reads)
	}
	// This is an observable support label, not a calibrated mastery threshold.
	if state.Passes >= 2 && !lastFailed {
		state.Level = "familiar"
	}
	if currentReport && state.SelfReport == "known" {
		state.Level = "self_reported"
	}
	if state.Checks > 0 {
		state.Basis = fmt.Sprintf("本组件 %d 次独立原型检查，%d 次通过；材料阅读、自评和检查分别记录。", state.Checks, state.Passes)
	}
	if currentReport {
		label := "已经熟悉"
		if state.SelfReport == "difficult" {
			label = "仍有困难"
		}
		state.Basis += " 你当前反馈“" + label + "”；用于安排下一步，不改变表现估计或检查证据。"
	}
	if (lastFailed && !currentReport) || (currentReport && state.SelfReport == "difficult") {
		state.Level = "review"
		state.LastDifficultyAt = lastCheck
		if currentReport && state.SelfReport == "difficult" {
			state.LastDifficultyAt = lastReport
		}
	}
	if memory, err := estimator.ProjectRecall(recalls, c.Version, now, .9); err == nil && memory != nil {
		due := memory.DueAt
		state.DueAt = &due
		if !due.After(now) {
			state.Level = "review"
		}
	}
	return state
}
func (s *Service) ComponentView(ctx context.Context, kb string, budget int, goal string) (*interfaces.ComponentView, error) {
	return s.ComponentViewWithTopic(ctx, kb, budget, goal, "")
}

func (s *Service) ComponentViewWithTopic(ctx context.Context, kb string, budget int, goal, topic string) (*interfaces.ComponentView, error) {
	scope, err := resolveReadScope(ctx, kb)
	if err != nil {
		return nil, err
	}
	if budget < 1 || budget > 120 {
		return nil, fmt.Errorf("%w: invalid time budget", ErrInvalidLearningRequest)
	}
	r, err := s.componentRepo()
	if err != nil {
		return nil, err
	}
	rows, err := r.ListComponents(ctx, scope.TenantID, kb)
	if err != nil {
		return nil, err
	}
	if topic != "" {
		found := false
		for _, row := range rows {
			if row.Definition.Topic == topic {
				found = true
			}
			if row.ID == goal && row.Definition.Topic != topic {
				return nil, fmt.Errorf("%w: goal is outside the selected module", ErrInvalidLearningRequest)
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: module does not belong to this knowledge base", ErrInvalidLearningRequest)
		}
	}
	result := &interfaces.ComponentView{ModelVersion: componentModelVersion, Components: []interfaces.ComponentEntry{}, Steps: []interfaces.ComponentStep{}, Budget: budget}
	if len(rows) == 0 {
		return result, nil
	}
	if goal != "" {
		found := false
		for _, row := range rows {
			if row.ID == goal {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: goal does not belong to this knowledge base", ErrInvalidLearningRequest)
		}
	}
	pages, err := s.wikiRepo.ListAll(ctx, kb)
	if err != nil {
		return nil, err
	}
	events, err := listEventWindow(ctx, s.repo, scope, time.Time{})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, row := range rows {
		raw, _ := json.Marshal(row.Definition)
		var public types.ComponentDefinition
		_ = json.Unmarshal(raw, &public)
		for i := range public.Checks {
			public.Checks[i].Answer = ""
			public.Checks[i].Explanation = ""
		}
		available := componentAvailable(row, pages)
		entry := interfaces.ComponentEntry{ID: row.ID, Version: row.Version, Material: public, Available: available, State: projectComponent(row, events, now)}
		if !available {
			entry.SourceProblem = "来源已变更或不可用，材料与检查需要重新核对。"
		}
		result.Components = append(result.Components, entry)
	}
	relevance, status := s.componentRelevance(ctx, scope, result.Components, pages, now)
	result.RelevanceStatus = status
	for i := range result.Components {
		result.Components[i].Relevance = relevance[result.Components[i].ID]
	}
	result.Steps, result.UsedMinutes, result.Plan = planComponentsWithTopic(result.Components, budget, goal, topic, now)
	return result, nil
}

func componentReadSeconds(c types.LearningComponent) int {
	return int(math.Max(8, math.Min(120, math.Ceil(float64(utf8.RuneCountInString(c.Definition.Explanation+c.Definition.Example))/15))))
}
func (s *Service) RecordComponentAction(ctx context.Context, kb string, in interfaces.ComponentAction) (*interfaces.ComponentActionResult, error) {
	result := &interfaces.ComponentActionResult{}
	if !learningEnabled() {
		return result, nil
	}
	scope, err := resolveReadScope(ctx, kb)
	if err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(in.OperationID); err != nil {
		return nil, fmt.Errorf("%w: action identity required", ErrInvalidLearningRequest)
	}
	epoch, err := s.operationEpoch(ctx, scope.SubjectID)
	if err != nil {
		return nil, err
	}
	err = s.repo.WithSubject(ctx, scope.SubjectID, epoch, true, func(tx context.Context) error {
		r, err := s.componentRepo()
		if err != nil {
			return err
		}
		rows, err := r.ListComponents(tx, scope.TenantID, kb)
		if err != nil {
			return err
		}
		var component *types.LearningComponent
		for i := range rows {
			if rows[i].ID == in.ID {
				component = &rows[i]
				break
			}
		}
		if component == nil {
			return fmt.Errorf("%w: component unavailable", ErrInvalidLearningRequest)
		}
		c := *component
		if c.Version != in.Version {
			return interfaces.ErrLearningReviewConflict
		}
		pages, err := s.wikiRepo.ListAll(tx, kb)
		if err != nil {
			return err
		}
		if !componentAvailable(c, pages) {
			return interfaces.ErrLearningReviewConflict
		}
		events, err := listEventWindow(tx, s.repo, scope, time.Time{})
		if err != nil {
			return err
		}
		now := time.Now()
		current := componentFacts(c, events, now)
		raw, _ := json.Marshal(in)
		digest := componentHash(string(raw))
		result.ReadAfterSeconds = componentReadSeconds(c)
		fact := componentFact{ComponentID: c.ID, Title: c.Definition.Title, Action: in.Action, OperationID: in.OperationID, RequestHash: digest, SessionID: in.SessionID, ModelVersion: componentModelVersion}
		for _, e := range current {
			var old componentFact
			_ = json.Unmarshal(e.ReviewData, &old)
			if old.OperationID == in.OperationID {
				if old.RequestHash != digest {
					return fmt.Errorf("%w: action identity reused", ErrInvalidLearningRequest)
				}
				result.Recorded = true
				result.Duplicate = true
				if old.Action == "check" {
					correct := old.Correct
					result.Correct = &correct
					result.Eligible = old.Eligible
					for _, q := range c.Definition.Checks {
						if q.ID == old.CheckID {
							result.Explanation = q.Explanation
						}
					}
				}
				state := projectComponent(c, events, now)
				result.State = &state
				return nil
			}
		}
		state := projectComponent(c, events, now)
		switch in.Action {
		case "open":
			if in.SessionID == "" {
				return fmt.Errorf("%w: reading session required", ErrInvalidLearningRequest)
			}
		case "read":
			opened := time.Time{}
			for _, e := range current {
				var f componentFact
				_ = json.Unmarshal(e.ReviewData, &f)
				if f.SessionID == in.SessionID && in.SessionID != "" {
					if f.Action == "open" && (opened.IsZero() || e.OccurredAt.Before(opened)) {
						opened = e.OccurredAt
					}
					if f.Action == "read" {
						result.Recorded = true
						result.Duplicate = true
						result.State = &state
						return nil
					}
				}
			}
			if opened.IsZero() || now.Sub(opened) < time.Duration(result.ReadAfterSeconds)*time.Second {
				return fmt.Errorf("%w: reading opportunity not yet observed", ErrInvalidLearningRequest)
			}
		case "known", "difficult":
			latestReport := time.Time{}
			for _, e := range current {
				var old componentFact
				_ = json.Unmarshal(e.ReviewData, &old)
				if old.Action == "known" || old.Action == "difficult" {
					latestReport = e.OccurredAt
				}
			}
			if state.SelfReport == in.Action && !latestReport.Before(state.LastCheckAt) && !latestReport.Before(state.LastReadAt) {
				result.Recorded = true
				result.Duplicate = true
				result.State = &state
				return nil
			}
		case "check":
			var check *types.ComponentCheck
			for i := range c.Definition.Checks {
				if c.Definition.Checks[i].ID == in.CheckID {
					check = &c.Definition.Checks[i]
					break
				}
			}
			if check == nil || (in.Answer != "?" && check.Options[in.Answer] == "") {
				return fmt.Errorf("%w: invalid check answer", ErrInvalidLearningRequest)
			}
			fact.CheckID = check.ID
			fact.Family = check.Family
			fact.Correct = in.Answer == check.Answer
			fact.Eligible = !in.Helped && in.Answer != "?"
			for _, e := range current {
				var f componentFact
				_ = json.Unmarshal(e.ReviewData, &f)
				if f.Action == "check" && f.Family == check.Family {
					fact.Eligible = false
				}
			}
			fact.Prediction = state.Familiarity*.9 + (1-state.Familiarity)*.25
			result.Correct = &fact.Correct
			result.Eligible = fact.Eligible
			result.Explanation = check.Explanation
		case "recall":
			if in.Rating < 1 || in.Rating > 4 {
				return fmt.Errorf("%w: invalid recall rating", ErrInvalidLearningRequest)
			}
			for _, e := range current {
				var f componentFact
				_ = json.Unmarshal(e.ReviewData, &f)
				if f.Action == "recall" && now.Sub(e.OccurredAt) < componentRecallCooldown {
					return fmt.Errorf("%w: recall already recorded recently", ErrInvalidLearningRequest)
				}
			}
			fact.Rating = in.Rating
		default:
			return fmt.Errorf("%w: invalid component action", ErrInvalidLearningRequest)
		}
		data, _ := json.Marshal(fact)
		event := types.LearningEvent{ID: uuid.NewString(), TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: kb, Slug: "kc:" + c.ID, ContentVersion: c.Version, Type: types.LearningEventComponent, OccurredAt: now, ReviewData: data}
		if err := s.repo.AppendEvent(tx, &event); err != nil {
			return err
		}
		events = append(events, event)
		state = projectComponent(c, events, now)
		result.Recorded = true
		result.State = &state
		return nil
	})
	if errors.Is(err, interfaces.ErrLearningCollectionDisabled) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}
