package handler

import (
	"bytes"
	"context"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type preferenceHandlerService struct {
	interfaces.LearningService
	calls int
	kb    string
	input interfaces.LearningPlanSettings
	err   error
}

func (f *preferenceHandlerService) UpdatePlanPreferences(_ context.Context, kb string, input interfaces.LearningPlanSettings) (*interfaces.LearningPlanSettings, error) {
	f.calls++
	f.kb = kb
	f.input = input
	return &input, f.err
}
func (f *preferenceHandlerService) GetPlanPreferences(_ context.Context, kb string) (*interfaces.LearningPlanSettings, error) {
	f.calls++
	f.kb = kb
	return &interfaces.LearningPlanSettings{Depth: "aware", TimeBudgetMinutes: 15}, f.err
}

func TestLearningPlanPreferencesHandlersEnvelopeAndConflict(t *testing.T) {
	t.Setenv("LEARNING_ENABLE", "true")
	f := &preferenceHandlerService{}
	h := NewLearningHandler(f)
	c, w := learningTestContext(t, true)
	h.GetPlanPreferences(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "kb-1", f.kb)
	c, w = learningTestContext(t, true)
	c.Request = httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"revision":"r1","limit_to_folder":true,"folder_id":"","depth":"operate","time_budget_minutes":5,"use_memory":false,"goal_objectives":[],"subject_id":"someone-else"}`)).WithContext(c.Request.Context())
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdatePlanPreferences(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, f.input.LimitToFolder)
	require.False(t, f.input.UseMemory)
	require.Equal(t, 5, f.input.TimeBudgetMinutes)
	f.err = interfaces.ErrLearningPreferenceConflict
	c, _ = learningTestContext(t, true)
	h.GetPlanPreferences(c)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrConflict, appErr.Code)
}

func TestLearningPlanPreferencesRejectOversizeAndFeatureDisabled(t *testing.T) {
	f := &preferenceHandlerService{}
	h := NewLearningHandler(f)
	t.Setenv("LEARNING_ENABLE", "")
	c, _ := learningTestContext(t, true)
	h.GetPlanPreferences(c)
	require.Zero(t, f.calls)
	t.Setenv("LEARNING_ENABLE", "true")
	c, _ = learningTestContext(t, true)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"folder_id":"`+strings.Repeat("x", 33*1024)+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdatePlanPreferences(c)
	require.Zero(t, f.calls)
	require.NotEmpty(t, c.Errors)
}
