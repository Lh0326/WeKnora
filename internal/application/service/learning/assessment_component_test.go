package learning

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assessmentComponentFixture(t *testing.T) (*Service, []types.LearningComponent, []*types.WikiPage, *gorm.DB) {
	t.Helper()
	s, db := auditDatabaseFixture(t)
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}))
	rows, pages := componentFixture(t)
	defs := []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	second := defs[0].Checks[0]
	second.ID, second.Family = "q2", "f2"
	defs[0].Checks = append(defs[0].Checks, second)
	rows, err := ValidateComponentPack(1, testKB, defs, pages)
	require.NoError(t, err)
	s.wikiRepo = &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}
	require.NoError(t, db.Create(&pages).Error)
	r, err := s.componentRepo()
	require.NoError(t, err)
	require.NoError(t, r.SaveComponents(collectorCtx(1, "alice"), rows))
	return s, rows, pages, db
}

func assessmentCheck(c types.LearningComponent, id, check, family string, eligible bool, at time.Time) types.LearningEvent {
	event := componentEvent(c, id, "check", at)
	var fact componentFact
	_ = json.Unmarshal(event.ReviewData, &fact)
	fact.CheckID, fact.Family, fact.Eligible = check, family, eligible
	event.ReviewData, _ = json.Marshal(fact)
	return event
}

func TestAssessmentFreezeComponentsPreservesStateAndExposure(t *testing.T) {
	s, rows, _, _ := assessmentComponentFixture(t)
	ctx := collectorCtx(1, "alice")
	now := time.Now().Add(-time.Hour)
	events := []types.LearningEvent{
		assessmentCheck(rows[0], "first", "q1", "f1", true, now),
		assessmentCheck(rows[0], "second", "q2", "f2", true, now.Add(time.Minute)),
		componentEvent(rows[1], "known", "known", now),
		assessmentCheck(rows[0], "old", "q-old", "f-old", false, now.Add(-time.Hour)),
		assessmentCheck(rows[0], "bob", "q-bob", "f-bob", false, now),
		assessmentCheck(rows[0], "tenant", "q-tenant", "f-tenant", true, now),
		assessmentCheck(rows[0], "kb", "q-kb", "f-kb", true, now),
		assessmentCheck(rows[0], "future", "q-future", "f-future", true, time.Now().Add(time.Hour)),
	}
	events[3].ContentVersion = "previous-material-version"
	events[4].SubjectID = "web_user:bob"
	events[5].TenantID = 2
	events[6].KnowledgeBaseID = "another-kb"
	for i := range events {
		require.NoError(t, s.repo.AppendEvent(ctx, &events[i]))
	}
	before, err := s.repo.ListEventsBySubject(ctx, "web_user:alice")
	require.NoError(t, err)
	started := time.Now().UTC()
	frozen, err := s.FreezeAssessment(ctx, testKB)
	require.NoError(t, err)
	require.False(t, frozen.StateAsOf.Before(started))
	require.False(t, frozen.StateAsOf.After(time.Now().UTC()))
	require.Equal(t, componentModelVersion, frozen.ComponentModelVersion)
	require.Equal(t, map[string]string{rows[0].ID: "familiar", rows[1].ID: "self_reported"}, frozen.ComponentStatesBefore)
	require.Empty(t, frozen.GoalStatesBefore, "KC support must not become legacy objective verification")
	require.Equal(t, rows[0].Version, frozen.ComponentVersions[rows[0].ID])
	require.True(t, frozen.ComponentAvailable[rows[0].ID])
	require.Equal(t, []string{"f-old", "f1", "f2"}, frozen.ComponentEvidenceFamiliesBefore)
	require.Equal(t, []string{"q-old", "q1", "q2"}, frozen.ComponentCheckIDsBefore[rows[0].ID])
	require.False(t, frozen.ComponentExposureComplete)
	require.Contains(t, frozen.ComponentExposureNote, "Displayed-but-unanswered")
	encoded, err := json.Marshal(frozen)
	require.NoError(t, err)
	for _, private := range []string{"answer_key", "review_data", "subject_id", "q-bob", "q-tenant", "q-kb", "q-future", rows[0].Definition.Checks[0].Question, rows[0].Definition.Checks[0].Explanation} {
		require.NotContains(t, string(encoded), private)
	}
	after, err := s.repo.ListEventsBySubject(ctx, "web_user:alice")
	require.NoError(t, err)
	require.Equal(t, before, after, "freezing must not append or rewrite personal evidence")

	bob, err := s.FreezeAssessment(collectorCtx(1, "bob"), testKB)
	require.NoError(t, err)
	require.Equal(t, "touched", bob.ComponentStatesBefore[rows[0].ID])
	require.Equal(t, "unseen", bob.ComponentStatesBefore[rows[1].ID])
	require.Equal(t, []string{"f-bob"}, bob.ComponentEvidenceFamiliesBefore)
	require.Equal(t, []string{"q-bob"}, bob.ComponentCheckIDsBefore[rows[0].ID])
}

func TestAssessmentFreezeComponentsVersionsAndUnsubmittedExposure(t *testing.T) {
	s, rows, pages, db := assessmentComponentFixture(t)
	ctx := collectorCtx(1, "alice")
	now := time.Now().Add(-time.Hour)
	for _, fact := range []types.LearningEvent{
		assessmentCheck(rows[0], "first", "q1", "f1", true, now),
		assessmentCheck(rows[0], "second", "q2", "f2", true, now.Add(time.Minute)),
		componentEvent(rows[1], "opened", "open", now),
	} {
		require.NoError(t, s.repo.AppendEvent(ctx, &fact))
	}
	// A revised target retains exposure to old questions, but no old results
	// are silently promoted into the new version's personal state.
	defs := []types.ComponentDefinition{rows[0].Definition, rows[1].Definition}
	defs[0].Example += " 此修订改变了材料例子。"
	revised, err := ValidateComponentPack(1, testKB, defs, pages)
	require.NoError(t, err)
	r, err := s.componentRepo()
	require.NoError(t, err)
	require.NoError(t, r.SaveComponents(ctx, revised))
	frozen, err := s.FreezeAssessment(ctx, testKB)
	require.NoError(t, err)
	require.Equal(t, "touched", frozen.ComponentStatesBefore[rows[0].ID])
	require.Equal(t, revised[0].Version, frozen.ComponentVersions[rows[0].ID])
	require.Equal(t, []string{"f1", "f2"}, frozen.ComponentEvidenceFamiliesBefore)
	require.Equal(t, []string{"q1", "q2"}, frozen.ComponentCheckIDsBefore[rows[0].ID])
	require.NotContains(t, frozen.ComponentCheckIDsBefore, rows[1].ID, "opening a component does not identify which check was displayed")
	require.False(t, frozen.ComponentExposureComplete)

	pages[0].Content += " 来源已修订。"
	require.NoError(t, db.Model(&types.WikiPage{}).Where("slug = ?", pages[0].Slug).Update("content", pages[0].Content).Error)
	stale, err := s.FreezeAssessment(ctx, testKB)
	require.NoError(t, err)
	require.False(t, stale.ComponentAvailable[rows[0].ID])
	require.False(t, stale.ComponentAvailable[rows[1].ID])
	require.Equal(t, revised[0].Version, stale.ComponentVersions[rows[0].ID])
}

type assessmentTransactionKey struct{}

// Assert that the component catalogue and paginated personal history consume
// the exact context supplied by the one subject transaction, even with >500
// events. This detects accidentally calling ComponentView outside the fence.
type assessmentTransactionRepo struct {
	*componentStubRepo
	t              *testing.T
	transactions   int
	componentReads int
	eventReads     int
	sourcePages    []*types.WikiPage
}

func (r *assessmentTransactionRepo) WithSubject(ctx context.Context, subject string, epoch int64, collect bool, fn func(context.Context) error) error {
	r.transactions++
	require.False(r.t, collect, "freeze is not learning telemetry")
	return r.stubLearningRepo.WithSubject(ctx, subject, epoch, collect, func(tx context.Context) error {
		return fn(context.WithValue(tx, assessmentTransactionKey{}, r))
	})
}

func (r *assessmentTransactionRepo) ListComponents(ctx context.Context, tenant uint64, kb string) ([]types.LearningComponent, error) {
	require.Same(r.t, r, ctx.Value(assessmentTransactionKey{}))
	r.componentReads++
	return r.componentStubRepo.ListComponents(ctx, tenant, kb)
}

func (r *assessmentTransactionRepo) ListComponentAssessmentSources(ctx context.Context, tenant uint64, kb string) ([]*types.WikiPage, error) {
	require.Same(r.t, r, ctx.Value(assessmentTransactionKey{}))
	require.Equal(r.t, uint64(1), tenant)
	require.Equal(r.t, testKB, kb)
	return r.sourcePages, nil
}

func (r *assessmentTransactionRepo) ListEventsPaged(ctx context.Context, scope interfaces.LearningScope, at time.Time, id string, limit int) ([]types.LearningEvent, error) {
	require.Same(r.t, r, ctx.Value(assessmentTransactionKey{}))
	r.eventReads++
	return r.stubLearningRepo.ListEventsPaged(ctx, scope, at, id, limit)
}

func TestAssessmentFreezeComponentsUsesSingleSubjectTransactionAndFullHistory(t *testing.T) {
	rows, pages := componentFixture(t)
	r := &assessmentTransactionRepo{componentStubRepo: &componentStubRepo{stubLearningRepo: newStubRepo(), components: rows}, t: t, sourcePages: pages}
	ctx := collectorCtx(1, "alice")
	now := time.Now().Add(-time.Hour)
	old := assessmentCheck(rows[0], "old-submission", "q-old", "f-old", false, now.Add(-time.Hour))
	require.NoError(t, r.AppendEvent(ctx, &old))
	for i := 0; i < 510; i++ {
		event := componentEvent(rows[0], "", "open", now.Add(time.Duration(i)*time.Second))
		require.NoError(t, r.AppendEvent(ctx, &event))
	}
	s := NewService(r, &stubWikiRepo{pages: map[string][]*types.WikiPage{testKB + "|concept": pages}}, nil, nil, nil, nil)
	frozen, err := s.FreezeAssessment(ctx, testKB)
	require.NoError(t, err)
	require.Equal(t, 1, r.transactions)
	require.Equal(t, 1, r.componentReads)
	require.GreaterOrEqual(t, r.eventReads, 4, "both projections must see the complete paginated history")
	require.Equal(t, []string{"f-old"}, frozen.ComponentEvidenceFamiliesBefore)
	require.Equal(t, []string{"q-old"}, frozen.ComponentCheckIDsBefore[rows[0].ID])
	require.Len(t, r.events, 511)
}
