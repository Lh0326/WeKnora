package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/application/service/learning"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// LearningHandler serves the learning tab's API. Every route derives the
// subject from the caller's principal — there is no subject parameter
// anywhere, so these endpoints can only ever touch the caller's own
// mastery (the memory routes' isolation argument, verbatim).
type LearningHandler struct {
	learningService interfaces.LearningService
}

// NewLearningHandler creates the handler; dig resolves the service.
func NewLearningHandler(learningService interfaces.LearningService) *LearningHandler {
	return &LearningHandler{learningService: learningService}
}

// requireLearningEnabled is the kill switch at the door: with
// LEARNING_ENABLE unset every route answers feature-disabled, shaped like
// the wiki KB-switch precedent (a plain bad-request error).
func (h *LearningHandler) requireLearningEnabled(c *gin.Context) bool {
	if learning.LearningEnabled() {
		return true
	}
	c.Error(apperrors.NewBadRequestError("learning feature is not enabled"))
	return false
}

func (h *LearningHandler) fail(c *gin.Context, err error, message string) {
	switch {
	case errors.Is(err, interfaces.ErrLearningReviewConflict):
		c.Error(apperrors.NewConflictError("recall schedule or source changed; reload before submitting"))
	case errors.Is(err, interfaces.ErrLearningPreferenceConflict):
		c.Error(apperrors.NewConflictError("saved learning scope changed in another page; reload it before saving"))
	case errors.Is(err, learning.ErrNoLearningScope):
		c.Error(apperrors.NewUnauthorizedError("no principal in request"))
	case errors.Is(err, learning.ErrInvalidLearningRequest), errors.Is(err, learning.ErrReviewRejected):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	case errors.Is(err, learning.ErrTaskNotFound), errors.Is(err, learning.ErrTaskNotTakeable):
		c.Error(apperrors.NewNotFoundError("task unavailable; refresh the learning path"))
	case errors.Is(err, interfaces.ErrLearningEpochAdvanced):
		c.Error(apperrors.NewBadRequestError("learning profile changed; refresh before submitting"))
	case errors.Is(err, learning.ErrQuizNotFound):
		c.Error(apperrors.NewNotFoundError("quiz item not found"))
	case errors.Is(err, learning.ErrWikiReadTarget):
		c.Error(apperrors.NewBadRequestError("not a knowledge node of this knowledge base"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(apperrors.NewInternalServerError(message).WithDetails(err.Error()))
	}
}

// GetProgress godoc
func (h *LearningHandler) GetProgress(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	progress, err := h.learningService.GetProgress(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "Failed to load learning progress")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": progress})
}

// ListMastery godoc
func (h *LearningHandler) ListMastery(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	views, err := h.learningService.ListMasteryView(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "Failed to load mastery")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": views})
}

// PassiveChanges godoc: the 遗忘动态 channel — decay-driven (non-action)
// state changes, derived entirely at read time. The timeline keeps only
// real actions, so passive drift lives in this separate endpoint and can
// never flood out the user's own activity.
func (h *LearningHandler) PassiveChanges(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 100 {
		limit = 100
	}
	summary, err := h.learningService.PassiveChanges(c.Request.Context(), c.Param("kb_id"), limit)
	if err != nil {
		h.fail(c, err, "Failed to load passive changes")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

// Recommend godoc
func (h *LearningHandler) Recommend(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	limit := 5
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			limit = n
		}
	}
	recs, err := h.learningService.Recommend(c.Request.Context(), c.Param("kb_id"), limit)
	if err != nil {
		h.fail(c, err, "Failed to build recommendations")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": recs})
}

// ZoneMap godoc — the module-partitioned recommendation surface: one
// wiki folder per knowledge zone with its own next step ("1") and
// follow-up ("2"), plus the full node set and prerequisite edges for the
// constellation's relation lines. Deterministic, no LLM on this path.
func (h *LearningHandler) ZoneMap(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	zm, err := h.learningService.ZoneMap(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "Failed to build zone map")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": zm})
}

// TakeQuiz godoc
// ReviewQuizItem godoc
// @Summary Human content review of a quiz item
// @Description Approve publishes (clone-safe family, version bump on semantic change); reject disables with the audit trail. Reviewer = authenticated human principal only.
// @Tags learning
// @Accept json
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Param item_id path string true "Quiz item id"
// @Param body body interfaces.ReviewDecisionInput true "Review decision"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/quiz/{item_id}/review [post]
func (h *LearningHandler) ReviewQuizItem(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req interfaces.ReviewDecisionInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, err, "invalid review body")
		return
	}
	item, err := h.learningService.ReviewQuizItem(c.Request.Context(), c.Param("kb_id"), c.Param("item_id"), req)
	if err != nil {
		h.fail(c, err, "review failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// ReviewObjective godoc
// @Summary Human review of an objective definition
// @Tags learning
// @Accept json
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Param objective_id path string true "Objective id"
// @Param body body interfaces.ReviewDecisionInput true "Review decision"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/objectives/{objective_id}/review [post]
func (h *LearningHandler) ReviewObjective(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req interfaces.ReviewDecisionInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, err, "invalid review body")
		return
	}
	obj, err := h.learningService.ReviewObjective(c.Request.Context(), c.Param("kb_id"), c.Param("objective_id"), req)
	if err != nil {
		h.fail(c, err, "review failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": obj})
}

// ReviewTask godoc
// @Summary Human review of a structured application task
// @Tags learning
// @Accept json
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Param task_id path string true "Task id"
// @Param body body interfaces.ReviewDecisionInput true "Review decision"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/tasks/{task_id}/review [post]
func (h *LearningHandler) ReviewTask(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req interfaces.ReviewDecisionInput
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, err, "invalid review body")
		return
	}
	task, err := h.learningService.ReviewTask(c.Request.Context(), c.Param("kb_id"), c.Param("task_id"), req)
	if err != nil {
		h.fail(c, err, "review failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}

// TakeTask godoc
// @Summary Takeable structured tasks (no answer keys)
// @Tags learning
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Param objective_id query string false "Filter by objective"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/tasks [get]
func (h *LearningHandler) TakeTask(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	tasks, err := h.learningService.TakeTask(c.Request.Context(), c.Param("kb_id"), c.Query("objective_id"))
	if err != nil {
		h.fail(c, err, "failed to load tasks")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tasks})
}

// SubmitTaskAnswer godoc
// @Summary Submit a task answer (server-side deterministic grading)
// @Tags learning
// @Accept json
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Param task_id path string true "Task id"
// @Param body body object true "{answers: {field_id: value}, assistance_mode?: string}"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/tasks/{task_id}/answer [post]
func (h *LearningHandler) SubmitTaskAnswer(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req struct {
		Answers        map[string]string `json:"answers"`
		AssistanceMode string            `json:"assistance_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, err, "invalid task answer body")
		return
	}
	result, err := h.learningService.SubmitTaskAnswer(c.Request.Context(), c.Param("kb_id"), c.Param("task_id"), req.Answers, req.AssistanceMode)
	if err != nil {
		h.fail(c, err, "task submit failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ObjectiveProgress godoc
// @Summary Separated progress numbers (stage 4)
// @Description Verified X/Y objectives, conflicts, stale, partial, untested, contact coverage — never a probability
// @Tags learning
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/objective-progress [get]
func (h *LearningHandler) ObjectiveProgress(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	summary, err := h.learningService.ObjectiveProgress(c.Request.Context(), c.Param("kb_id"), c.QueryArray("goal")...)
	if err != nil {
		h.fail(c, err, "failed to load objective progress")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

// ShortPath godoc
// @Summary Cold-start / short learning path (3-5 steps)
// @Description (node, objective, action) steps with structured reasons, time estimates, completion conditions and conditional successors
// @Tags learning
// @Accept json
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Param body body interfaces.ColdStartRequestPayload true "Cold start request (goals, depth, budget, fast-track)"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/path [post]
func (h *LearningHandler) ShortPath(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req interfaces.ColdStartRequestPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, err, "invalid path request body")
		return
	}
	plan, err := h.learningService.ShortPath(c.Request.Context(), c.Param("kb_id"), req)
	if err != nil {
		h.fail(c, err, "failed to plan path")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": plan})
}

// ObjectiveView godoc
// @Summary Separated evidence profile (objectives)
// @Description Per (node, objective) five dimensions: exposure, self_report, objective_evidence, recency, path_status
// @Tags learning
// @Produce json
// @Param kb_id path string true "Knowledge base id"
// @Success 200 {object} map[string]interface{}
// @Router /learning/kb/{kb_id}/objectives [get]
func (h *LearningHandler) ObjectiveView(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	view, err := h.learningService.ObjectiveView(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "failed to load objective view")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func (h *LearningHandler) TakeQuiz(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	slug := c.Query("slug")
	if slug == "" {
		c.Error(apperrors.NewValidationError("slug is required"))
		return
	}
	questions, err := h.learningService.TakeQuiz(c.Request.Context(), c.Param("kb_id"), slug)
	if err != nil {
		h.fail(c, err, "Failed to load quiz")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": questions})
}

type learningAnswerRequest struct {
	ChosenKey string `json:"chosen_key" binding:"required,oneof=A B C D E"`
	// AssistanceMode optionally declares the trial condition ("" = the
	// item's designed mode); assistant_helped records practice, never
	// strict evidence.
	AssistanceMode string `json:"assistance_mode"`
}

type learningReadRequest struct {
	Slug string `json:"slug" binding:"required"`
	// Tier refines the read signal: "" / "normal" = page opened;
	// "deep" = the reader stayed past the deep-dwell threshold.
	Tier string `json:"tier" binding:"omitempty,oneof=normal deep"`
}

type learningSelfAssessRequest struct {
	Slug      string `json:"slug" binding:"required"`
	Direction string `json:"direction" binding:"required,oneof=up down"`
	// Reason is required for "down" (the skills-matrix interview question):
	// all | doc_gap | doc_updated | quiz_easy.
	Reason string `json:"reason"`
}

type learningSkipRequest struct {
	Slug string `json:"slug" binding:"required"`
	// Skipped true marks "已掌握，不再推荐"; false revokes the mark.
	Skipped *bool `json:"skipped" binding:"required"`
}

// RecordRead godoc — the low-trust "opened this page to read it" touch
// signal from the wiki browser. Silent on success; failures are logged.
func (h *LearningHandler) RecordRead(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req learningReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if err := h.learningService.RecordWikiRead(c.Request.Context(), c.Param("kb_id"), req.Slug, req.Tier); err != nil {
		if errors.Is(err, interfaces.ErrLearningCollectionDisabled) {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"recorded": false, "reason": "collection_disabled"}})
			return
		}
		h.fail(c, err, "Failed to record wiki read")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"recorded": true}})
}

// SelfAssess godoc — the interactive self-assessment: lifts the score into
// the claimed band (tier still gated on quiz proof) or demotes with a
// reason taxonomy whose labels feed content maintenance.
func (h *LearningHandler) SelfAssess(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req learningSelfAssessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	reason := ""
	if req.Direction == "down" {
		switch req.Reason {
		case "all", "doc_gap", "doc_updated", "quiz_easy":
			reason = req.Reason
		default:
			c.Error(apperrors.NewValidationError("Invalid request data").WithDetails("reason is required for direction=down"))
			return
		}
	}
	if err := h.learningService.RecordSelfAssess(c.Request.Context(), c.Param("kb_id"), req.Slug, req.Direction == "up", reason); err != nil {
		h.fail(c, err, "Failed to record self-assessment")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RecordSkip godoc — the standing queue-suppression declaration ("已掌握，
// 不再推荐") or its revocation. Folds nothing, expires never: the node
// simply leaves every next-step surface until the user takes it back.
func (h *LearningHandler) RecordSkip(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req learningSkipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if err := h.learningService.RecordSkip(c.Request.Context(), c.Param("kb_id"), req.Slug, *req.Skipped); err != nil {
		h.fail(c, err, "Failed to record skip")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SubmitAnswer godoc — deterministic grading; no LLM anywhere on this path.
func (h *LearningHandler) SubmitAnswer(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req learningAnswerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	result, err := h.learningService.SubmitAnswer(c.Request.Context(), c.Param("kb_id"), c.Param("item_id"), req.ChosenKey, req.AssistanceMode)
	if err != nil {
		h.fail(c, err, "Failed to grade answer")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// KnowledgeHealth godoc — the owner/admin org aggregate: coverage counts,
// expert nodes, single-person and stale-doc risks, folder roll-ups and
// recent self-assessment maintenance marks. Deterministic read path; no
// LLM anywhere. Unlike the personal routes above, the subject dimension
// does not exist here at all — people are counted, never named.
func (h *LearningHandler) KnowledgeHealth(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	health, err := h.learningService.KnowledgeHealth(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "Failed to load knowledge health")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": health})
}

// Timeline godoc
func (h *LearningHandler) Timeline(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	page, pageSize, ok := parseListPagination(c)
	if !ok {
		return
	}
	items, total, err := h.learningService.Timeline(c.Request.Context(), c.Param("kb_id"), page, pageSize)
	if err != nil {
		h.fail(c, err, "Failed to load timeline")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items, "total": total, "page": page, "page_size": pageSize})
}

// ExportProfile godoc
func (h *LearningHandler) ExportProfile(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	payload, err := h.learningService.ExportProfile(c.Request.Context())
	if err != nil {
		h.fail(c, err, "Failed to export learning profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": payload})
}

// DeleteProfile godoc
func (h *LearningHandler) DeleteProfile(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	optOut := c.Query("opt_out") == "true"
	if err := h.learningService.DeleteProfile(c.Request.Context(), optOut); err != nil {
		h.fail(c, err, "Failed to delete learning profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type learningSettingsRequest struct {
	CollectDisabled *bool `json:"collect_disabled"`
}

// GetSettings godoc
func (h *LearningHandler) GetSettings(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	settings, err := h.learningService.GetSettings(c.Request.Context())
	if err != nil {
		h.fail(c, err, "Failed to load learning settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": settings})
}

// UpdateSettings godoc
func (h *LearningHandler) UpdateSettings(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req learningSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if req.CollectDisabled == nil {
		c.Error(apperrors.NewBadRequestError("collect_disabled is required"))
		return
	}
	if err := h.learningService.UpdateSettings(c.Request.Context(), *req.CollectDisabled); err != nil {
		h.fail(c, err, "Failed to update learning settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"collect_disabled": *req.CollectDisabled}})
}

// FreezeAssessment is a server-generated, label-free snapshot for external holdout evaluation.
func (h *LearningHandler) FreezeAssessment(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	snapshot, err := h.learningService.FreezeAssessment(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "failed to freeze assessment state")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": snapshot})
}

// SetNodeState records the caller's explicit learning preference, never a grade.
func (h *LearningHandler) SetNodeState(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	var req struct {
		Slug  string `json:"slug" binding:"required"`
		State string `json:"state" binding:"required,oneof=read known review"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid learning preference"))
		return
	}
	if err := h.learningService.SetNodeState(c.Request.Context(), c.Param("kb_id"), req.Slug, req.State); err != nil {
		h.fail(c, err, "Failed to save learning preference")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
