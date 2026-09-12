package handler

import (
	"context"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recallHandlerService struct {
	interfaces.LearningService
	calls int
	kb    string
	input interfaces.LearningReviewInput
	err   error
}

func (f *recallHandlerService) UpdateReviewSchedule(_ context.Context, kb string, input interfaces.LearningReviewInput) (*interfaces.LearningReviewStatus, error) {
	f.calls++
	f.kb = kb
	f.input = input
	return &interfaces.LearningReviewStatus{Revision: "r1"}, f.err
}
func TestLearningRecallHandlerScopeConflictSizeAndFeatureGate(t *testing.T) {
	f := &recallHandlerService{}
	h := NewLearningHandler(f)
	t.Setenv("LEARNING_ENABLE", "true")
	c, w := learningTestContext(t, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"slug":"concept/rag","action":"good","revision":"r0","subject_id":"other-user"}`)).WithContext(c.Request.Context())
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateReviewSchedule(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "kb-1", f.kb)
	require.Equal(t, "concept/rag", f.input.Slug)
	f.err = interfaces.ErrLearningReviewConflict
	c, _ = learningTestContext(t, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"slug":"a"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateReviewSchedule(c)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrConflict, appErr.Code)
	c, _ = learningTestContext(t, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"slug":"`+strings.Repeat("x", 5000)+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateReviewSchedule(c)
	require.Equal(t, 2, f.calls)
	require.NotEmpty(t, c.Errors)
	t.Setenv("LEARNING_ENABLE", "")
	c, _ = learningTestContext(t, true)
	h.UpdateReviewSchedule(c)
	require.Equal(t, 2, f.calls)
}
