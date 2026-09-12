package handler

import (
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *LearningHandler) GetPlanPreferences(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	result, err := h.learningService.GetPlanPreferences(c.Request.Context(), c.Param("kb_id"))
	if err != nil {
		h.fail(c, err, "Failed to load learning scope")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
func (h *LearningHandler) UpdatePlanPreferences(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	var input interfaces.LearningPlanSettings
	if err := c.ShouldBindJSON(&input); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid learning scope"))
		return
	}
	result, err := h.learningService.UpdatePlanPreferences(c.Request.Context(), c.Param("kb_id"), input)
	if err != nil {
		h.fail(c, err, "Failed to save learning scope")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
