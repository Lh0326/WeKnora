package handler

import (
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

func (h *LearningHandler) components(c *gin.Context) interfaces.LearningComponentService {
	if !h.requireLearningEnabled(c) {
		return nil
	}
	s, ok := h.learningService.(interfaces.LearningComponentService)
	if !ok {
		c.Error(apperrors.NewInternalServerError("component service unavailable"))
		return nil
	}
	return s
}
func (h *LearningHandler) ComponentView(c *gin.Context) {
	s := h.components(c)
	if s == nil {
		return
	}
	budget, err := strconv.Atoi(c.DefaultQuery("minutes", "15"))
	if err != nil {
		c.Error(apperrors.NewBadRequestError("invalid time budget"))
		return
	}
	var view *interfaces.ComponentView
	if topic := c.Query("topic"); topic != "" {
		scoped, ok := s.(interfaces.LearningComponentScopeService)
		if !ok {
			c.Error(apperrors.NewBadRequestError("module selection is unavailable"))
			return
		}
		view, err = scoped.ComponentViewWithTopic(c.Request.Context(), c.Param("kb_id"), budget, c.Query("goal"), topic)
	} else {
		view, err = s.ComponentView(c.Request.Context(), c.Param("kb_id"), budget, c.Query("goal"))
	}
	if err != nil {
		h.fail(c, err, "Failed to load learning components")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}
func (h *LearningHandler) ComponentAction(c *gin.Context) {
	s := h.components(c)
	if s == nil {
		return
	}
	var in interfaces.ComponentAction
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	if err := c.ShouldBindJSON(&in); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid component action"))
		return
	}
	result, err := s.RecordComponentAction(c.Request.Context(), c.Param("kb_id"), in)
	if err != nil {
		h.fail(c, err, "Failed to save component action")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
func (h *LearningHandler) ImportComponents(c *gin.Context) {
	s := h.components(c)
	if s == nil {
		return
	}
	var in struct {
		Components []types.ComponentDefinition `json:"components"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	if err := c.ShouldBindJSON(&in); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid component pack"))
		return
	}
	count, err := s.ImportComponents(c.Request.Context(), c.Param("kb_id"), in.Components)
	if err != nil {
		h.fail(c, err, "Failed to import component pack")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"count": count}})
}

func (h *LearningHandler) DraftComponents(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	s, ok := h.learningService.(interfaces.LearningComponentDraftService)
	if !ok {
		c.Error(apperrors.NewInternalServerError("component drafting unavailable"))
		return
	}
	var in interfaces.ComponentDraftRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	if err := c.ShouldBindJSON(&in); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid draft source selection"))
		return
	}
	result, err := s.DraftComponents(c.Request.Context(), c.Param("kb_id"), in)
	if err != nil {
		h.fail(c, err, "Failed to prepare learning material candidates")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
