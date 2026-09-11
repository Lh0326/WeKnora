package learning

import (
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Opt-in integration tests. The caller supplies a dedicated test database;
// each case owns one random schema and never migrates the public schema.
func recallPostgresFixture(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("LEARNING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LEARNING_TEST_POSTGRES_DSN for PostgreSQL verification")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), config)
	require.NoError(t, err)
	adminPool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminPool.Close()) })
	schema := "codex_recall_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	scoped := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		scoped = u.String()
	}
	db, err := gorm.Open(postgres.Open(scoped), config)
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.AutoMigrate(&types.LearningObjective{}, &types.LearningQuizItem{}, &types.LearningQuizAttempt{}, &types.LearningTask{}, &types.LearningTaskAttempt{}, &types.LearningSubjectPrefs{}, &types.LearningPlanPreference{}, &types.LearningBackfillMark{}, &types.LearningSubjectEpoch{}, &types.LearningEvent{}, &types.MasteryState{}, &types.MemoryWikiMap{}, &types.LearningSkip{}))
	s, _, _ := readFixture(t)
	s.repo = repository.NewLearningRepository(db)
	t.Setenv("LEARNING_ENABLE", "true")
	return s, db
}
func TestPostgresRecallServicePersistenceAndDeletion(t *testing.T) {
	s, db := recallPostgresFixture(t)
	exerciseRecallDatabaseLoop(t, s, db)
}
func TestPostgresRecallServiceConcurrentFeedback(t *testing.T) {
	s, db := recallPostgresFixture(t)
	exerciseRecallAtomicFeedback(t, s, db)
}
func TestPostgresLearningPlanPreferenceConflictAndZeroValues(t *testing.T) {
	s, db := recallPostgresFixture(t)
	exercisePlanPreferencePersistence(t, s, db)
}
