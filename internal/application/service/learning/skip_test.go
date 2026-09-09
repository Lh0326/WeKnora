package learning

// Skip-list tests: the standing "已掌握，不再推荐" declaration. The load-
// bearing guarantees: the node leaves the recommendation queue on record,
// returns on revoke, carries the badge in the mastery view, joins the
// data-sovereignty export, and dies with the profile delete.

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestRecordSkipRoundTripQueueAndBadge(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"

	// Baseline: rag is a candidate.
	recs, err := svc.Recommend(ctx, testKB, 5)
	if err != nil {
		t.Fatal(err)
	}
	hasRag := false
	for _, r := range recs {
		if r.Slug == slug {
			hasRag = true
		}
	}
	if !hasRag {
		t.Fatal("baseline: rag must be recommendable before the skip")
	}

	// Skip it (works regardless of the collection opt-out: a preference
	// the user authored, not telemetry).
	if err := svc.RecordSkip(ctx, testKB, slug, true); err != nil {
		t.Fatal(err)
	}
	recs, err = svc.Recommend(ctx, testKB, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Slug == slug {
			t.Fatalf("skipped node still recommended (reason %s)", r.Reason)
		}
	}

	// The mastery view badges it.
	views, err := svc.ListMasteryView(ctx, testKB)
	if err != nil {
		t.Fatal(err)
	}
	badged := false
	for _, v := range views {
		if v.Slug == slug {
			if !v.Skipped || v.SkippedAt.IsZero() {
				t.Fatalf("view must carry the skip badge, got %+v", v)
			}
			badged = true
		}
	}
	if !badged {
		t.Fatal("view lost the node entirely")
	}

	// The forgetting digest also honours the skip: seed decayed evidence
	// on the skipped node and confirm it never surfaces.
	seedFold(t, svc, repo, ctx, slug,
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-150 * 24 * time.Hour)},
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-150 * 24 * time.Hour)},
		Event{Type: types.LearningEventAnswerCite, Weight: WeightAnswerCite, OccurredAt: time.Now().Add(-150 * 24 * time.Hour)},
	)
	changes, err := svc.PassiveChanges(ctx, testKB, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range changes.Items {
		if ch.Slug == slug {
			t.Fatal("skipped node surfaced in the passive forgetting digest")
		}
	}

	// Revoke: the node returns.
	if err := svc.RecordSkip(ctx, testKB, slug, false); err != nil {
		t.Fatal(err)
	}
	recs, err = svc.Recommend(ctx, testKB, 5)
	if err != nil {
		t.Fatal(err)
	}
	hasRag = false
	for _, r := range recs {
		if r.Slug == slug {
			hasRag = true
		}
	}
	if !hasRag {
		t.Fatal("revoked skip must restore the node to the queue")
	}
}

func TestRecordSkipRejectsNonNode(t *testing.T) {
	svc, _, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	if err := svc.RecordSkip(ctx, testKB, "summary/not-a-node", true); err != ErrWikiReadTarget {
		t.Fatalf("non-node skip must fail with ErrWikiReadTarget, got %v", err)
	}
}

func TestSkipExportsAndDiesWithProfile(t *testing.T) {
	svc, _, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	const slug = "concept/rag"

	if err := svc.RecordSkip(ctx, testKB, slug, true); err != nil {
		t.Fatal(err)
	}
	payload, err := svc.ExportProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range payload.Skips {
		if s.Slug == slug {
			found = true
		}
	}
	if !found {
		t.Fatal("export must include the skip declaration")
	}

	if err := svc.DeleteProfile(ctx, false); err != nil {
		t.Fatal(err)
	}
	skips, err := svc.repo.ListSkips(ctx, interfaces.LearningScope{
		TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("profile delete must remove skips, got %v", skips)
	}
}
