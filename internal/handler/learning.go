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
	case errors.Is(err, learning.ErrNoLearningScope):
		c.Error(apperrors.NewUnauthorizedError("no principal in request"))
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

// TakeQuiz godoc
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
}

type learningReadRequest struct {
	Slug string `json:"slug" binding:"required"`
}

type learningSelfAssessRequest struct {
	Slug      string `json:"slug" binding:"required"`
	Direction string `json:"direction" binding:"required,oneof=up down"`
	// Reason is required for "down" (the skills-matrix interview question):
	// all | doc_gap | doc_updated | quiz_easy.
	Reason string `json:"reason"`
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
	if err := h.learningService.RecordWikiRead(c.Request.Context(), c.Param("kb_id"), req.Slug); err != nil {
		h.fail(c, err, "Failed to record wiki read")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
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
	result, err := h.learningService.SubmitAnswer(c.Request.Context(), c.Param("kb_id"), c.Param("item_id"), req.ChosenKey)
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
