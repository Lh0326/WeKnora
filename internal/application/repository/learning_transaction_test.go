package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLearningSubjectTransactionRollsBackAttemptEventAndFold(t *testing.T) {
	db := setupLearningTestDB(t)
	r := NewLearningRepository(db)
	scope := learningTestScope()
	require.NoError(t, db.Exec("CREATE TRIGGER reject_fold BEFORE INSERT ON mastery_states BEGIN SELECT RAISE(ABORT, 'injected fold failure'); END").Error)
	err := r.WithSubject(t.Context(), scope.SubjectID, 0, true, func(ctx context.Context) error {
		if err := r.InsertAttempt(ctx, &types.LearningQuizAttempt{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, QuizItemID: "q", Slug: "a", ChosenKey: "A", AnsweredAt: time.Now()}); err != nil {
			return err
		}
		if err := r.AppendEvent(ctx, &types.LearningEvent{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", Type: types.LearningEventQuizCorrect, OccurredAt: time.Now()}); err != nil {
			return err
		}
		return r.UpsertMastery(ctx, &types.MasteryState{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", EvidenceCount: 1})
	})
	require.Error(t, err)
	for _, table := range []string{"learning_quiz_attempts", "learning_events", "mastery_states"} {
		require.Zero(t, countRows(t, db, table, scope.SubjectID), table)
	}
}

func TestLearningAgentTraceDoesNotCountAsHumanActivity(t *testing.T) {
	db := setupLearningTestDB(t)
	r := NewLearningRepository(db)
	scope := learningTestScope()
	now := time.Now()
	require.NoError(t, r.AppendEvent(t.Context(), &types.LearningEvent{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", Type: types.LearningEventAgentRead, OccurredAt: now}))
	last, err := r.ListLastActivity(t.Context(), scope)
	require.NoError(t, err)
	require.Empty(t, last)
	days, err := r.ListActiveDays(t.Context(), scope, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, days)
	require.NoError(t, r.AppendEvent(t.Context(), &types.LearningEvent{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", Type: types.LearningEventWikiToolRead, OccurredAt: now}))
	last, err = r.ListLastActivity(t.Context(), scope)
	require.NoError(t, err)
	require.Len(t, last, 1)
}

// Each test gets a fresh, randomly named schema in the explicitly supplied TEST
// database. No application database configuration is read or migrated.
func postgresLearningTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LEARNING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LEARNING_TEST_POSTGRES_DSN is required for real PostgreSQL concurrency verification")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := "learning_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, base.Exec("CREATE SCHEMA "+schema).Error)
	scoped := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, e := url.Parse(dsn)
		require.NoError(t, e)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		scoped = u.String()
	}
	db, err := gorm.Open(postgres.Open(scoped), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		require.NoError(t, base.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		pool, e := base.DB()
		if e == nil {
			_ = pool.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&types.LearningEvent{}, &types.MasteryState{}, &types.MemoryWikiMap{}, &types.LearningQuizAttempt{}, &types.LearningTaskAttempt{}, &types.LearningPlanPreference{}, &types.LearningSubjectPrefs{}, &types.LearningSubjectEpoch{}, &types.LearningSkip{}))
	return db
}

func awaitLearningError(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("database operation did not finish")
		return nil
	}
}

type learningDeleteBarrierKey struct{}

func TestPostgresDeletionFencesOldWritesAcrossConnections(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_control=%v", existing), func(t *testing.T) {
			db := postgresLearningTestDB(t)
			r := NewLearningRepository(db)
			scope := learningTestScope()
			if existing {
				require.NoError(t, r.WithSubject(t.Context(), scope.SubjectID, 0, false, func(context.Context) error { return nil }))
			}
			paused, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			// Pause AFTER the last personal DELETE. The old lock order allowed an
			// old-epoch writer to commit in exactly this interval.
			require.NoError(t, db.Callback().Delete().After("gorm:delete").Register("test:delete_barrier", func(tx *gorm.DB) {
				if tx.Statement.Table == "learning_plan_preferences" && tx.Statement.Context.Value(learningDeleteBarrierKey{}) == true {
					once.Do(func() { close(paused); <-release })
				}
			}))
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			deleted := make(chan error, 1)
			go func() {
				deleted <- r.DeleteProfileData(context.WithValue(ctx, learningDeleteBarrierKey{}, true), scope.TenantID, scope.SubjectID, false)
			}()
			select {
			case <-paused:
			case <-ctx.Done():
				t.Fatal("delete did not reach barrier")
			}
			write := make(chan error, 1)
			go func() {
				write <- r.WithSubject(ctx, scope.SubjectID, 0, true, func(ctx context.Context) error {
					return r.AppendEvent(ctx, &types.LearningEvent{ID: "old-work", TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", Type: types.LearningEventAgentRead, OccurredAt: time.Now()})
				})
			}()
			select {
			case err := <-write:
				close(release)
				t.Fatalf("writer bypassed delete lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			require.NoError(t, awaitLearningError(t, deleted))
			require.ErrorIs(t, awaitLearningError(t, write), interfaces.ErrLearningEpochAdvanced)
			require.Zero(t, countRows(t, db, "learning_events", scope.SubjectID))
			epoch, err := r.GetSubjectEpoch(ctx, scope.SubjectID)
			require.NoError(t, err)
			// A genuinely new operation remains legal after optOut=false.
			require.NoError(t, r.WithSubject(ctx, scope.SubjectID, epoch, true, func(ctx context.Context) error {
				return r.AppendEvent(ctx, &types.LearningEvent{ID: "new-work", TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", Type: types.LearningEventWikiToolRead, OccurredAt: time.Now()})
			}))
			require.EqualValues(t, 1, countRows(t, db, "learning_events", scope.SubjectID))
		})
	}
}

func TestPostgresDeleteWaitsForWriterThenSweepsItsCommit(t *testing.T) {
	db := postgresLearningTestDB(t)
	r := NewLearningRepository(db)
	scope := learningTestScope()
	locked, release := make(chan struct{}), make(chan struct{})
	write := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	go func() {
		write <- r.WithSubject(ctx, scope.SubjectID, 0, true, func(ctx context.Context) error {
			close(locked)
			<-release
			return r.UpsertMastery(ctx, &types.MasteryState{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", EvidenceCount: 1})
		})
	}()
	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal("writer did not lock")
	}
	deleted := make(chan error, 1)
	go func() { deleted <- r.DeleteProfileData(ctx, scope.TenantID, scope.SubjectID, false) }()
	select {
	case err := <-deleted:
		close(release)
		t.Fatalf("delete bypassed writer: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, awaitLearningError(t, write))
	require.NoError(t, awaitLearningError(t, deleted))
	require.Zero(t, countRows(t, db, "mastery_states", scope.SubjectID))
}

func TestPostgresConsentAndConcurrentFoldAreSerialized(t *testing.T) {
	db := postgresLearningTestDB(t)
	r := NewLearningRepository(db)
	scope := learningTestScope()
	ctx := t.Context()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.WithSubject(ctx, scope.SubjectID, 0, true, func(ctx context.Context) error {
				row, err := r.GetMastery(ctx, scope, "a")
				if err != nil {
					return err
				}
				if row == nil {
					row = &types.MasteryState{TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a"}
				}
				row.EvidenceCount++
				return r.UpsertMastery(ctx, row)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	row, err := r.GetMastery(ctx, scope, "a")
	require.NoError(t, err)
	require.Equal(t, 12, row.EvidenceCount)
	require.NoError(t, r.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{TenantID: scope.TenantID, SubjectID: scope.SubjectID, CollectDisabled: true}))
	called := false
	err = r.WithSubject(ctx, scope.SubjectID, 0, true, func(context.Context) error { called = true; return nil })
	require.ErrorIs(t, err, interfaces.ErrLearningEpochAdvanced)
	require.False(t, called)
	epoch, err := r.GetSubjectEpoch(ctx, scope.SubjectID)
	require.NoError(t, err)
	err = r.WithSubject(ctx, scope.SubjectID, epoch, true, func(context.Context) error { return errors.New("must not run") })
	require.ErrorIs(t, err, interfaces.ErrLearningCollectionDisabled)
}

func TestLearningPagedHistoryExceedsOldCap(t *testing.T) {
	db := setupLearningTestDB(t)
	repo := NewLearningRepository(db)
	scope := learningTestScope()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := make([]types.LearningEvent, 12017)
	for i := range events {
		events[i] = types.LearningEvent{ID: fmt.Sprintf("event-%06d", i), TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: scope.KnowledgeBaseID, Slug: "a", Type: types.LearningEventAnswerCite, Weight: 1, OccurredAt: at}
	}
	require.NoError(t, db.CreateInBatches(events, 200).Error)
	count := 0
	last := ""
	require.NoError(t, repo.WithSubject(t.Context(), scope.SubjectID, 0, false, func(ctx context.Context) error {
		after := time.Time{}
		for {
			rows, err := repo.ListEventsPaged(ctx, scope, after, last, 997)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				if last != "" && row.ID <= last {
					return fmt.Errorf("duplicate or reordered id %s", row.ID)
				}
				last = row.ID
				after = row.OccurredAt
				count++
			}
			if len(rows) < 997 {
				break
			}
		}
		return nil
	}))
	require.Equal(t, len(events), count)
}

func TestLearningNestedTransactionRechecksEpochAndConsent(t *testing.T) {
	db := setupLearningTestDB(t)
	r := NewLearningRepository(db)
	scope := learningTestScope()
	require.NoError(t, r.WithSubject(t.Context(), scope.SubjectID, 0, false, func(ctx context.Context) error {
		require.ErrorIs(t, r.WithSubject(ctx, scope.SubjectID, 1, true, func(context.Context) error { t.Fatal("nested stale epoch entered callback"); return nil }), interfaces.ErrLearningEpochAdvanced)
		require.NoError(t, r.UpsertSubjectPrefs(ctx, &types.LearningSubjectPrefs{TenantID: scope.TenantID, SubjectID: scope.SubjectID, CollectDisabled: true}))
		require.ErrorIs(t, r.WithSubject(ctx, scope.SubjectID, 1, true, func(context.Context) error { t.Fatal("nested collection bypassed consent"); return nil }), interfaces.ErrLearningCollectionDisabled)
		return nil
	}))
}

func TestPostgresProjectionMigrationUpDownPreservesRecords(t *testing.T) {
	db := postgresLearningTestDB(t)
	down, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", "000093_learning_projection_identity.down.sql"))
	require.NoError(t, err)
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "versioned", "000093_learning_projection_identity.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(down)).Error)
	require.NoError(t, db.Exec("INSERT INTO learning_quiz_attempts (id,tenant_id,subject_id,knowledge_base_id,quiz_item_id,slug,chosen_key,answered_at) VALUES ('sentinel',1,'s','kb','q','concept/old','A',now())").Error)
	require.NoError(t, db.Exec(string(up)).Error)
	var attempt types.LearningQuizAttempt
	require.NoError(t, db.First(&attempt, "id = ?", "sentinel").Error)
	require.Equal(t, "concept/old", attempt.Slug)
	require.Empty(t, attempt.OriginalSlug)
	require.True(t, db.Migrator().HasColumn(&types.MasteryState{}, "replay_hash"))
	require.NoError(t, db.Exec(string(down)).Error)
	var count int64
	require.NoError(t, db.Table("learning_quiz_attempts").Where("id = ?", "sentinel").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.False(t, db.Migrator().HasColumn(&types.LearningQuizAttempt{}, "original_slug"))
}
