package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Stage 2: structured application tasks (contract B, 01 §5.3). A task is
// a scenario plus a form of SELECT fields; grading is a server-side EXACT
// field match against the frozen answer key for every critical check.
// No free-text grading, no keyword matching, no vector similarity — open
// answers are not part of this chain by construction. The answer key
// never appears in any take payload.

// TaskTake/TaskSubmitResult alias the canonical interfaces payload types.
type (
	TaskTake         = interfaces.TaskTakePayload
	TaskSubmitResult = interfaces.TaskSubmitPayload
	TaskCheckResult  = interfaces.TaskCheckResultPayload
)

// ErrTaskNotFound / ErrTaskNotTakeable mirror the quiz path's semantics.
var (
	ErrTaskNotFound    = errors.New("learning: task not found")
	ErrTaskNotTakeable = errors.New("learning: task is not available for verification")
)

// validateTaskShape enforces the deterministic structure at write/review
// time: non-empty scenario, ≥1 select field with ≥2 options, every
// critical check references a field, and the answer key covers every
// critical check with a value among the field's options.
func validateTaskShape(t *types.LearningTask) error {
	if strings.TrimSpace(t.Scenario) == "" || strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("task title/scenario required")
	}
	var fields []map[string]interface{}
	if err := json.Unmarshal(t.Fields, &fields); err != nil || len(fields) == 0 {
		return fmt.Errorf("task fields must be a non-empty array")
	}
	optionsByField := map[string][]string{}
	for _, f := range fields {
		id, _ := f["id"].(string)
		ftype, _ := f["type"].(string)
		if id == "" || ftype != "select" || optionsByField[id] != nil {
			return fmt.Errorf("task field %q must be a select", id)
		}
		raw, _ := f["options"].([]interface{})
		if len(raw) < 2 {
			return fmt.Errorf("task field %q needs ≥2 options", id)
		}
		seen := map[string]bool{}
		for _, o := range raw {
			v, ok := o.(string)
			if !ok || strings.TrimSpace(v) == "" || seen[v] {
				return fmt.Errorf("invalid or duplicate option for %s", id)
			}
			seen[v] = true
			optionsByField[id] = append(optionsByField[id], v)
		}
	}
	var checks []map[string]interface{}
	if err := json.Unmarshal(t.CriticalChecks, &checks); err != nil || len(checks) == 0 {
		return fmt.Errorf("task needs ≥1 critical check")
	}
	seenChecks := map[string]bool{}
	for _, c := range checks {
		id, _ := c["id"].(string)
		if seenChecks[id] {
			return fmt.Errorf("duplicate check %s", id)
		}
		seenChecks[id] = true
		if id == "" {
			return fmt.Errorf("critical check needs an id")
		}
		if _, ok := optionsByField[id]; !ok {
			return fmt.Errorf("critical check %q references no field", id)
		}
		answer, ok := t.AnswerKey[id]
		if !ok || answer == "" {
			return fmt.Errorf("answer key missing for check %q", id)
		}
		found := false
		for _, o := range optionsByField[id] {
			if o == answer {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("answer %q for check %q is not among the field options", answer, id)
		}
	}
	return nil
}

// ReviewTask applies the human review to a task; approve requires
// validateTaskShape plus a bound objective.
func (s *Service) ReviewTask(ctx context.Context, kbID, taskID string, input interfaces.ReviewDecisionInput) (*types.LearningTask, error) {
	dec := reviewDecisionOf(input)
	if err := dec.normalize(); err != nil {
		return nil, err
	}
	reviewer, err := reviewerPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	task, err := s.repo.GetTaskByID(ctx, scope.TenantID, taskID)
	if err != nil || task == nil || task.KnowledgeBaseID != scope.KnowledgeBaseID {
		return nil, ErrTaskNotFound
	}
	if dec.Decision == "reject" {
		task.Status = types.LearningQuizStatusDisabled
		task.Reviewer = reviewer
		task.ReviewNote = strings.TrimSpace(dec.Reason + " " + dec.Note)
		task.ChangeKind = conservativeChangeKind(dec.ChangeKind)
		if err := s.repo.UpsertTask(ctx, task); err != nil {
			return nil, err
		}
		return task, nil
	}
	if err := validateTaskShape(task); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReviewRejected, err)
	}
	if task.ObjectiveID == "" {
		return nil, fmt.Errorf("%w: strict tasks must bind to an objective", ErrReviewRejected)
	}
	objective, err := s.verificationObjective(ctx, scope.TenantID, kbID, task.ObjectiveID, task.Slug, types.ObjectiveContractTaskChecks)
	if err != nil {
		return nil, err
	}
	task.ObjectiveVersion = objective.ContentVersion
	task.EvidenceHash = objective.EvidenceHash
	task.SourceRefs, _ = json.Marshal(objective.SourceRefs)
	task.ContentVersion = defaultIfEmpty(task.ContentVersion, "v1")
	if task.AssistanceMode == types.AssistanceAssistantHelp {
		return nil, ErrReviewRejected
	}
	now := time.Now()
	task.ChangeKind = conservativeChangeKind(dec.ChangeKind)
	if task.Status == types.LearningQuizStatusPublished && task.ChangeKind == "semantic" {
		task.ContentVersion = bumpVersion(task.ContentVersion)
		objective.ContentVersion = bumpVersion(objective.ContentVersion)
		objective.ReviewNote = "Semantic task correction requires renewed evidence: " + task.ID
		objective.Reviewer = reviewer
		objective.PublishedAt = &now
		if err := s.repo.UpsertObjective(ctx, objective); err != nil {
			return nil, err
		}
		task.ObjectiveVersion = objective.ContentVersion
	}
	task.Status = types.LearningQuizStatusPublished
	task.Reviewer = reviewer
	task.PublishedAt = &now
	task.ReviewNote = dec.Note
	task.AssistanceMode = defaultIfEmpty(task.AssistanceMode, types.AssistanceOpenBook)
	task.ScorerVersion = types.TaskScorerVersion
	task.RubricVersion = defaultIfEmpty(task.RubricVersion, "task-rubric-v1")
	if task.FamilyID == "" {
		if len(task.ID) >= 8 {
			task.FamilyID = "task-" + task.ID[:8]
		} else {
			task.FamilyID = "task-" + task.ID
		}
	}
	if err := s.repo.UpsertTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// TakeTask serves the takeable tasks of one objective (or one slug when
// objectiveID is empty) WITHOUT the answer key. Only published tasks are
// takeable — drafts are not silently servable.
func (s *Service) TakeTask(ctx context.Context, kbID, objectiveID string) ([]interfaces.TaskTakePayload, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	tasks, err := s.repo.ListTasks(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	prior, err := s.repo.ListTaskAttempts(ctx, scope)
	if err != nil {
		return nil, err
	}
	var out, practice []interfaces.TaskTakePayload
	for i := range tasks {
		t := &tasks[i]
		if !s.taskReady(ctx, t) {
			continue
		}
		if objectiveID != "" && t.ObjectiveID != objectiveID {
			continue
		}
		used := false
		for _, a := range prior {
			if a.TaskID == t.ID || (a.ObjectiveID == t.ObjectiveID && a.FamilyID == t.FamilyID) {
				used = true
			}
		}
		mode := "verification"
		if used {
			mode = "practice"
		}
		dto := interfaces.TaskTakePayload{FamilyID: t.FamilyID, Mode: mode,
			ID: t.ID, Title: t.Title, Scenario: t.Scenario,
			Fields:         json.RawMessage(t.Fields),
			AssistanceMode: t.AssistanceMode, ContentVersion: t.ContentVersion,
			RubricVersion: t.RubricVersion, ObjectiveID: t.ObjectiveID,
		}
		if used {
			practice = append(practice, dto)
		} else {
			out = append(out, dto)
		}
	}
	return append(out, practice...), nil
}

// SubmitTaskAnswer grades one task submission server-side: every critical
// check is an exact match against the frozen answer key. The client
// submits field VALUES only; it cannot influence the verdict. Declared
// assistance that differs from the task's designed mode is recorded but
// never eligible (modes do not mix).
func (s *Service) SubmitTaskAnswer(ctx context.Context, kbID, taskID string, answers map[string]string, declaredAssistance string) (*interfaces.TaskSubmitPayload, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	epoch, err := s.operationEpoch(ctx, scope.SubjectID)
	if err != nil {
		return nil, err
	}
	var out *interfaces.TaskSubmitPayload
	err = s.repo.WithSubject(ctx, scope.SubjectID, epoch, true, func(tx context.Context) error {
		var e error
		out, e = s.submitTaskAnswer(tx, kbID, taskID, answers, declaredAssistance, true)
		return e
	})
	if errors.Is(err, interfaces.ErrLearningCollectionDisabled) {
		return s.submitTaskAnswer(ctx, kbID, taskID, answers, declaredAssistance, false)
	}
	return out, err
}

func (s *Service) taskReady(ctx context.Context, t *types.LearningTask) bool {
	if t.Status != types.LearningQuizStatusPublished || t.Reviewer == "" || t.PublishedAt == nil || t.FamilyID == "" || t.ScorerVersion != types.TaskScorerVersion || validateTaskShape(t) != nil {
		return false
	}
	o, err := s.verificationObjective(ctx, t.TenantID, t.KnowledgeBaseID, t.ObjectiveID, t.Slug, types.ObjectiveContractTaskChecks)
	return err == nil && o.ContentVersion == t.ObjectiveVersion && t.EvidenceHash != "" && o.EvidenceHash == t.EvidenceHash
}

func (s *Service) submitTaskAnswer(ctx context.Context, kbID, taskID string, answers map[string]string, declaredAssistance string, persist bool) (*interfaces.TaskSubmitPayload, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	task, err := s.repo.GetTaskByID(ctx, scope.TenantID, taskID)
	if err != nil || task == nil || task.KnowledgeBaseID != scope.KnowledgeBaseID {
		return nil, ErrTaskNotFound
	}
	if !s.taskReady(ctx, task) {
		return nil, ErrTaskNotTakeable
	}
	var fields []struct {
		ID      string   `json:"id"`
		Options []string `json:"options"`
	}
	if err := json.Unmarshal(task.Fields, &fields); err != nil {
		return nil, ErrTaskNotTakeable
	}
	if len(answers) != len(fields) {
		return nil, fmt.Errorf("%w: answer all presented fields", ErrInvalidLearningRequest)
	}
	for _, f := range fields {
		valid := false
		for _, option := range f.Options {
			if value, ok := answers[f.ID]; ok && value == option {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("%w: invalid value for field %s", ErrInvalidLearningRequest, f.ID)
		}
	}
	var checks []map[string]interface{}
	if err := json.Unmarshal(task.CriticalChecks, &checks); err != nil {
		return nil, err
	}
	result := &interfaces.TaskSubmitPayload{Assistance: defaultIfEmpty(declaredAssistance, task.AssistanceMode)}
	frozen := make([]interfaces.TaskCheckResultPayload, 0, len(checks))
	allPassed := true
	for _, c := range checks {
		id, _ := c["id"].(string)
		correct := answers[id] == task.AnswerKey[id]
		frozen = append(frozen, interfaces.TaskCheckResultPayload{ID: id, Passed: correct})
		if !correct {
			allPassed = false
		}
	}
	result.Passed = allPassed
	result.Checks = frozen

	// Eligibility: published + reviewed task, assistance matching the
	// designed condition. assistant_helped (or any mismatch) records but
	// never counts.
	eligible := persist && result.Assistance != types.AssistanceAssistantHelp && result.Assistance == task.AssistanceMode
	if eligible {
		result.GradeReason = "eligible_independent"
	} else {
		result.GradeReason = "assistance_not_strict"
	}
	if !persist {
		result.GradeReason = "collection_disabled"
	}
	if eligible {
		prior, err := s.repo.ListTaskAttempts(ctx, scope)
		if err != nil {
			return nil, err
		}
		for _, a := range prior {
			if a.TaskID == task.ID || (a.ObjectiveID == task.ObjectiveID && a.FamilyID == task.FamilyID) {
				eligible = false
				result.GradeReason = "feedback_retry"
				break
			}
		}
	}
	result.Eligible = eligible

	attempt := &types.LearningTaskAttempt{
		TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID,
		TaskID: task.ID, ObjectiveID: task.ObjectiveID, FamilyID: task.FamilyID, Slug: task.Slug,
		Answers: answers, IsPassed: allPassed,
		ObjectiveVersion: task.ObjectiveVersion, ContractVersion: types.ObjectiveContractVersion, RubricVersion: task.RubricVersion, ScorerVersion: task.ScorerVersion,
		AssistanceMode: result.Assistance, TaskStatus: task.Status, ContentVersion: task.ContentVersion,
		Eligible: eligible, GradeReason: result.GradeReason, SubmittedAt: time.Now(),
	}
	if raw, err := json.Marshal(frozen); err == nil {
		attempt.Checks = raw
	}
	if !persist {
		return result, nil
	}
	if !s.taskReady(ctx, task) {
		return nil, ErrTaskNotTakeable
	}
	if err := s.repo.InsertTaskAttempt(ctx, attempt); err != nil {
		return nil, err
	}
	return result, nil
}
