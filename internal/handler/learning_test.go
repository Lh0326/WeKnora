package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/learning"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeLearningService records calls; unimplemented methods stay nil-panic
// so tests only stray where they mean to.
type fakeLearningService struct {
	interfaces.LearningService
	progressCalls int
	healthCalls   int
	healthKB      string
	readCalls     int
	readKB        string
	readSlug      string
	lastCtx       context.Context
}

func (f *fakeLearningService) RecordWikiRead(_ context.Context, kbID, slug string) error {
	f.readCalls++
	f.readKB = kbID
	f.readSlug = slug
	return nil
}

func (f *fakeLearningService) KnowledgeHealth(_ context.Context, kbID string) (*interfaces.KnowledgeHealth, error) {
	f.healthCalls++
	f.healthKB = kbID
	return &interfaces.KnowledgeHealth{NodesTotal: 7, NodesCovered: 3, SubjectsActive: 2}, nil
}

func (f *fakeLearningService) GetProgress(ctx context.Context, kbID string) (*learning.LearningProgress, error) {
	f.progressCalls++
	f.lastCtx = ctx
	return &learning.LearningProgress{TotalNodes: 4, LitNodes: 1, Levels: map[string]int{"unseen": 3, "touched": 1}}, nil
}

func learningTestContext(t *testing.T, withPrincipal bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if withPrincipal {
		ctx := context.WithValue(req.Context(), types.TenantIDContextKey, uint64(1))
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
		req = req.WithContext(ctx)
	}
	c.Request = req
	c.Params = gin.Params{{Key: "kb_id", Value: "kb-1"}}
	return c, w
}

// TestLearningRoutesFeatureDisabledWhenGateClosed: with LEARNING_ENABLE
// unset, every route must answer feature-disabled and never reach the
// service — the "off is indistinguishable from absent" contract.
func TestLearningRoutesFeatureDisabledWhenGateClosed(t *testing.T) {
	fake := &fakeLearningService{}
	h := NewLearningHandler(fake)
	t.Setenv("LEARNING_ENABLE", "")

	c, _ := learningTestContext(t, true)
	h.GetProgress(c)
	require.Equal(t, 0, fake.progressCalls, "gate closed must not reach the service")
	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrBadRequest, appErr.Code)
}

// TestLearningProgressHappyPath is the happy-path proof: gate open,
// principal present, data flows through the standard envelope.
func TestLearningProgressHappyPath(t *testing.T) {
	fake := &fakeLearningService{}
	h := NewLearningHandler(fake)
	t.Setenv("LEARNING_ENABLE", "true")

	c, w := learningTestContext(t, true)
	h.GetProgress(c)
	require.Equal(t, 1, fake.progressCalls)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"total_nodes":4`)
	require.Contains(t, w.Body.String(), `"success":true`)
}

// TestLearningRecordReadHappyPath: the page-open touch signal reaches the
// service with the kb and slug intact and answers the standard envelope.
func TestLearningRecordReadHappyPath(t *testing.T) {
	fake := &fakeLearningService{}
	h := NewLearningHandler(fake)
	t.Setenv("LEARNING_ENABLE", "true")

	c, w := learningTestContext(t, true)
	var body bytes.Buffer
	body.WriteString(`{"slug":"concept/rag"}`)
	c.Request, _ = http.NewRequest(http.MethodPost, "/learning/kb/kb-1/read", &body)
	c.Request.Header.Set("Content-Type", "application/json")
	h.RecordRead(c)
	require.Equal(t, 1, fake.readCalls)
	require.Equal(t, "kb-1", fake.readKB)
	require.Equal(t, "concept/rag", fake.readSlug)
	require.Equal(t, http.StatusOK, w.Code)
}

// TestLearningNoPrincipalIsUnauthorized: a request without a principal
// maps the scope error to the unauthorized bucket.
func TestLearningNoPrincipalIsUnauthorized(t *testing.T) {
	h := NewLearningHandler(&scopeFailingService{})
	t.Setenv("LEARNING_ENABLE", "true")

	c, _ := learningTestContext(t, false)
	h.GetProgress(c)
	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrUnauthorized, appErr.Code)
}

type scopeFailingService struct {
	interfaces.LearningService
}

func (s *scopeFailingService) GetProgress(ctx context.Context, kbID string) (*learning.LearningProgress, error) {
	return nil, learning.ErrNoLearningScope
}

func (s *scopeFailingService) KnowledgeHealth(ctx context.Context, kbID string) (*interfaces.KnowledgeHealth, error) {
	return nil, learning.ErrNoLearningScope
}

// TestLearningKnowledgeHealthHappyPath: gate open, the aggregate flows
// through the standard envelope with the kb param intact. The owner/admin
// permission itself is carried by the route group (OwnedWikiKBOrAdmin +
// KBAccessRead, the activity-feed matrix) rather than the handler, so it
// is enforced by route construction — see routes_learning.go.
func TestLearningKnowledgeHealthHappyPath(t *testing.T) {
	fake := &fakeLearningService{}
	h := NewLearningHandler(fake)
	t.Setenv("LEARNING_ENABLE", "true")

	c, w := learningTestContext(t, true)
	h.KnowledgeHealth(c)
	require.Equal(t, 1, fake.healthCalls)
	require.Equal(t, "kb-1", fake.healthKB)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"nodes_total":7`)
	require.Contains(t, w.Body.String(), `"success":true`)
}

// TestLearningKnowledgeHealthGateClosed: with the kill switch off the
// health route answers feature-disabled and never reaches the service.
func TestLearningKnowledgeHealthGateClosed(t *testing.T) {
	fake := &fakeLearningService{}
	h := NewLearningHandler(fake)
	t.Setenv("LEARNING_ENABLE", "")

	c, _ := learningTestContext(t, true)
	h.KnowledgeHealth(c)
	require.Equal(t, 0, fake.healthCalls, "gate closed must not reach the service")
	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrBadRequest, appErr.Code)
}

// TestLearningKnowledgeHealthNoTenantIsUnauthorized: the org aggregate
// still needs the effective tenant the KB-access middleware sets; without
// it the scope error maps to the unauthorized bucket.
func TestLearningKnowledgeHealthNoTenantIsUnauthorized(t *testing.T) {
	h := NewLearningHandler(&scopeFailingService{})
	t.Setenv("LEARNING_ENABLE", "true")

	c, _ := learningTestContext(t, false)
	h.KnowledgeHealth(c)
	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrUnauthorized, appErr.Code)
}

// TestLearningSettingsValidation: the opt-out write requires the field.
func TestLearningSettingsValidation(t *testing.T) {
	h := NewLearningHandler(&fakeLearningService{})
	t.Setenv("LEARNING_ENABLE", "true")

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	h.UpdateSettings(c)
	require.Len(t, c.Errors, 1)
}
