package handler

import (
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *LearningHandler) UpdateReviewSchedule(c *gin.Context) {
	if !h.requireLearningEnabled(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input interfaces.LearningReviewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid recall feedback"))
		return
	}
	result, err := h.learningService.UpdateReviewSchedule(c.Request.Context(), c.Param("kb_id"), input)
	if err != nil {
		h.fail(c, err, "Failed to save recall feedback")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
