package learning

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTopicEpochFenceExercisesAcceptedVerdict(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive control", true: "delete during model"}[deleted], func(t *testing.T) {
			svc, repo, wiki, _, fake := maintenanceFixture(t)
			t.Setenv("LEARNING_ENABLE", "true")
			fake.responses = []*types.ChatResponse{{Content: `{"maps":{"rag":{"slug":"concept/rag","confidence":0.99}}}`, FinishReason: "stop"}}
			if deleted {
				fake.onCall = func() {
					if err := repo.DeleteProfileData(t.Context(), 1, "web_user:alice", false); err != nil {
						t.Fatal(err)
					}
				}
			}
			pages, _ := wiki.ListAll(t.Context(), testKB)
			bySlug := map[string]*types.WikiPage{}
			for _, p := range pages {
				bySlug[p.Slug] = p
			}
			svc.mapTopicsForScope(t.Context(), 1, "web_user:alice", testKB, "m", []*types.MemoryTopicStat{{Topic: "RAG", NormalizedKey: "rag"}}, pages, bySlug)
			if repo.applyCalls.Load() != 1 {
				t.Fatalf("accepted mapping never reached the transaction: calls=%d", repo.applyCalls.Load())
			}
			want := 1
			if deleted {
				want = 0
			}
			if len(repo.maps) != want {
				t.Fatalf("maps=%d want=%d", len(repo.maps), want)
			}
			count := 0
			for _, e := range repo.snapshotEvents() {
				if e.Type == types.LearningEventTopicSignal {
					count++
				}
			}
			if count != want {
				t.Fatalf("topic events=%d want=%d", count, want)
			}
		})
	}
}

func TestDetachedLearningContextCannotOutliveDeletion(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	old := logger.CloneContext(svc.CaptureCollectionContext(ctx))
	if err := svc.DeleteProfile(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAgentRead(old, testKB, "concept/rag"); !errors.Is(err, interfaces.ErrLearningEpochAdvanced) {
		t.Fatalf("old detached context accepted: %v", err)
	}
	if len(repo.snapshotEvents()) != 0 {
		t.Fatal("old task resurrected profile")
	}
	if err := svc.RecordWikiRead(ctx, testKB, "concept/rag", "normal"); err != nil {
		t.Fatal(err)
	}
	if len(repo.snapshotEvents()) != 1 {
		t.Fatal("a new deliberate operation after optOut=false must remain legal")
	}
}

func TestQuizVersionCheckedWithoutMaintenance(t *testing.T) {
	for _, scenario := range []string{"changed chunk", "changed title", "removed chunks", "empty references", "deleted page", "unknown version"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, wiki := readFixture(t)
			ctx := collectorCtx(1, "alice")
			item := &types.LearningQuizItem{ID: "q-version", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", CorrectKey: "A", Status: types.LearningQuizStatusActive}
			if err := storeGroundedQuiz(t, svc, repo, ctx, item); err != nil {
				t.Fatal(err)
			}
			if questions, err := svc.TakeQuiz(ctx, testKB, item.Slug); err != nil || len(questions) != 1 {
				t.Fatalf("positive control: %v %v", questions, err)
			}
			page, _ := wiki.GetBySlug(ctx, testKB, item.Slug)
			switch scenario {
			case "changed chunk":
				svc.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "revised"
			case "changed title":
				page.Title = "Revised meaning"
			case "removed chunks":
				delete(svc.chunkRepo.(*stubChunkRepo).chunks, "c1")
			case "empty references":
				page.ChunkRefs = nil
			case "deleted page":
				wiki.pages[testKB+"|concept"] = nil
			case "unknown version":
				repo.quizItems[0].EvidenceHash = ""
			}
			if questions, err := svc.TakeQuiz(ctx, testKB, item.Slug); err != nil || len(questions) != 0 {
				t.Fatalf("stale question served: %v %v", questions, err)
			}
			if _, err := svc.SubmitAnswer(ctx, testKB, item.ID, "A", ""); !errors.Is(err, ErrQuizNotFound) {
				t.Fatalf("stale answer scored: %v", err)
			}
			if len(repo.quizAttempts) != 0 || len(repo.snapshotEvents()) != 0 {
				t.Fatal("invalid evidence generated personal records")
			}
		})
	}
}

func TestQuizRegeneratesSameStemAndHonorsDisabled(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "new evidence same stem", true: "manual veto"}[disabled], func(t *testing.T) {
			svc, repo, wiki, kbs, fake := maintenanceFixture(t)
			wiki.pages[testKB+"|concept"] = wiki.pages[testKB+"|concept"][:1]
			item := &types.LearningQuizItem{ID: "old", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", Question: "Same stem?", CorrectKey: "A", Status: types.LearningQuizStatusActive}
			if disabled {
				item.Status = types.LearningQuizStatusDisabled
			}
			if err := storeGroundedQuiz(t, svc, repo, t.Context(), item); err != nil {
				t.Fatal(err)
			}
			svc.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "new evidence"
			fake.responses = []*types.ChatResponse{{Content: `{"questions":[{"question":"Same stem?","options":{"A":"a","B":"b","C":"c","D":"d"},"correct_key":"B","explanation":"new answer","chunk_refs":["c1"]}]}`, FinishReason: "stop"}}
			if err := svc.runQuizPass(t.Context(), kbs.kbs[testKB], "m"); err != nil {
				t.Fatal(err)
			}
			// Stage 2: regenerated items enter as draft (LLM output is
			// practice until human review), so the fresh item's lifecycle
			// status is draft — the regeneration contract itself (same
			// stem re-adjudicated against new evidence, old key replaced,
			// disabled veto honored) is unchanged.
			fresh := 0
			for _, it := range repo.quizItems {
				if it.Status == types.LearningQuizStatusDraft {
					fresh++
					if it.CorrectKey != "B" {
						t.Fatal("old key retained")
					}
				}
			}
			want := 1
			if disabled {
				want = 0
			}
			if fresh != want {
				t.Fatalf("draft=%d want=%d", fresh, want)
			}
		})
	}
}

func TestQuizGenerationRejectsMidModelEvidenceEdit(t *testing.T) {
	svc, repo, wiki, kbs, fake := maintenanceFixture(t)
	wiki.pages[testKB+"|concept"] = wiki.pages[testKB+"|concept"][:1]
	fake.responses = []*types.ChatResponse{{Content: `{"questions":[{"question":"Q?","options":{"A":"a","B":"b","C":"c","D":"d"},"correct_key":"A","explanation":"old evidence","chunk_refs":["c1"]}]}`, FinishReason: "stop"}}
	fake.onCall = func() { svc.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "edited while generating" }
	if err := svc.runQuizPass(t.Context(), kbs.kbs[testKB], "m"); err != nil {
		t.Fatal(err)
	}
	if len(repo.quizItems) != 0 {
		t.Fatal("published a model result from an obsolete snapshot")
	}
}

func TestReconcileMergesEverySourceAndSurvivesAliasRemoval(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	page := &types.WikiPage{Slug: "concept/c", PageType: "concept", Aliases: types.StringArray{"a", "b"}}
	wiki.addPage(testKB, page)
	scope := newReadScope(1, "web_user:alice")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, slug := range []string{"concept/a", "concept/b", "concept/c"} {
		e := types.LearningEvent{ID: slug, TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, Slug: slug, Weight: 1, Type: types.LearningEventAnswerCite, OccurredAt: at.Add(time.Duration(i) * time.Hour)}
		if err := repo.AppendEvent(t.Context(), &e); err != nil {
			t.Fatal(err)
		}
		row := &types.MasteryState{TenantID: 1, SubjectID: scope.SubjectID, KnowledgeBaseID: testKB, Slug: slug}
		FoldEvent(FoldState{}, Event{Weight: 1, OccurredAt: e.OccurredAt}).ApplyTo(row)
		if err := repo.UpsertMastery(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	runner := newTestRunner(repo, wiki, kbsWithTestKB())
	for round := 0; round < 2; round++ {
		if err := runner.runOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		rows, _ := repo.ListMastery(t.Context(), scope)
		if len(rows) != 1 || rows[0].Slug != "concept/c" || rows[0].EvidenceCount != 3 || rows[0].ReplayHash == "" {
			t.Fatalf("round %d lost merged evidence: %+v", round, rows)
		}
		page.Aliases = nil
	}
	for _, e := range repo.snapshotEvents() {
		if e.Slug != "concept/c" {
			t.Fatal("event identity was stranded")
		}
		if e.ID != "concept/c" && e.OriginalSlug != e.ID {
			t.Fatal("original provenance lost")
		}
	}
}

func TestReconcileDiscoversFirstLostFold(t *testing.T) {
	repo := newStubRepo()
	wiki := &stubWikiRepo{pages: map[string][]*types.WikiPage{}}
	wiki.addPage(testKB, &types.WikiPage{Slug: "concept/a", PageType: "concept"})
	e := &types.LearningEvent{ID: "first", TenantID: 1, SubjectID: "s", KnowledgeBaseID: testKB, Slug: "concept/a", Type: types.LearningEventAnswerCite, Weight: 1, OccurredAt: time.Now()}
	if err := repo.AppendEvent(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if err := newTestRunner(repo, wiki, kbsWithTestKB()).runOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := repo.GetMastery(t.Context(), newReadScope(1, "s"), "concept/a")
	if err != nil || row == nil || row.EvidenceCount != 1 || row.ReplayHash == "" || row.ProjectionVersion != FoldProjectionVersion {
		t.Fatalf("first lost fold not reconstructed: %+v %v", row, err)
	}
}

func TestActualServiceRollsBackQuizAndReadOnFoldFailure(t *testing.T) {
	for _, kind := range []string{"read", "quiz"} {
		t.Run(kind, func(t *testing.T) {
			svc, _, _ := readFixture(t)
			t.Setenv("LEARNING_ENABLE", "true")
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			pool.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = pool.Close() })
			if err = db.AutoMigrate(&types.LearningEvent{}, &types.MasteryState{}, &types.LearningSubjectEpoch{}, &types.LearningSubjectPrefs{}, &types.LearningQuizItem{}, &types.LearningQuizAttempt{}); err != nil {
				t.Fatal(err)
			}
			svc.repo = repository.NewLearningRepository(db)
			ctx := collectorCtx(1, "alice")
			_, _, hash, err := svc.currentQuizEvidence(ctx, 1, testKB, "concept/rag")
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.repo.UpsertQuizItem(ctx, &types.LearningQuizItem{ID: "q", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", CorrectKey: "A", EvidenceHash: hash, Status: types.LearningQuizStatusActive}); err != nil {
				t.Fatal(err)
			}
			if err = db.Exec("CREATE TRIGGER reject_projection BEFORE INSERT ON mastery_states BEGIN SELECT RAISE(ABORT, 'injected projection failure'); END").Error; err != nil {
				t.Fatal(err)
			}
			if kind == "quiz" {
				_, err = svc.SubmitAnswer(ctx, testKB, "q", "A", "")
			} else {
				err = svc.RecordWikiRead(ctx, testKB, "concept/rag", "normal")
			}
			if err == nil {
				t.Fatal("projection failure must propagate to transaction boundary")
			}
			for _, table := range []string{"learning_events", "learning_quiz_attempts", "mastery_states"} {
				var count int64
				if err = db.Table(table).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("partial %s rows committed: %d", table, count)
				}
			}
			if err = db.Exec("DROP TRIGGER reject_projection").Error; err != nil {
				t.Fatal(err)
			}
			if kind == "quiz" {
				_, err = svc.SubmitAnswer(ctx, testKB, "q", "A", "")
			} else {
				err = svc.RecordWikiRead(ctx, testKB, "concept/rag", "normal")
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := svc.repo.ListMastery(ctx, newReadScope(1, "web_user:alice"))
			if err != nil || len(rows) != 1 {
				t.Fatalf("positive control failed: %+v %v", rows, err)
			}
			if kind == "read" {
				agents := make([]types.LearningEvent, 501)
				for i := range agents {
					agents[i] = types.LearningEvent{ID: "crowding-" + strconv.Itoa(i), TenantID: 1, SubjectID: "web_user:alice", KnowledgeBaseID: testKB, Slug: "concept/rag", Type: types.LearningEventAgentRead, OccurredAt: time.Now()}
				}
				if err = db.CreateInBatches(agents, 100).Error; err != nil {
					t.Fatal(err)
				}
				if err = svc.RecordWikiRead(ctx, testKB, "concept/rag", "normal"); err != nil {
					t.Fatal(err)
				}
				after, err := svc.repo.GetMastery(ctx, newReadScope(1, "web_user:alice"), "concept/rag")
				if err != nil || after == nil || after.EvidenceCount != rows[0].EvidenceCount {
					t.Fatalf("agent traces evicted human dedup evidence: %+v %v", after, err)
				}
			}
		})
	}
}
func TestAnswerCallbackRetryCannotBecomeNegativeEvidence(t *testing.T) {
	svc, repo, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	ctx := collectorCtx(1, "alice")
	msg := answerMessage(&types.SearchResult{ID: "c1", KnowledgeBaseID: testKB})
	svc.RecordAnswerTouches(ctx, msg)
	svc.RecordAnswerTouches(ctx, msg)
	events := repo.snapshotEvents()
	if len(events) != 1 || events[0].Weight <= 0 {
		t.Fatalf("callback retry created false re-ask: %+v", events)
	}
}
func TestAliasResolutionRejectsAmbiguityAndLiveNodeNames(t *testing.T) {
	pages := []*types.WikiPage{{Slug: "concept/c", Aliases: types.StringArray{"a", "b"}}, {Slug: "concept/d", Aliases: types.StringArray{"a"}}, {Slug: "concept/b"}}
	_, aliases := canonicalAliasIndex(pages)
	if aliases["concept/a"] != "" || aliases["concept/b"] != "" {
		t.Fatalf("ambiguous or live identity was aliased: %+v", aliases)
	}
}

func TestActualQuizEvidenceChangeDuringSubmissionRollsBack(t *testing.T) {
	svc, _, _ := readFixture(t)
	t.Setenv("LEARNING_ENABLE", "true")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	if err = db.AutoMigrate(&types.LearningEvent{}, &types.MasteryState{}, &types.LearningSubjectEpoch{}, &types.LearningSubjectPrefs{}, &types.LearningQuizItem{}, &types.LearningQuizAttempt{}); err != nil {
		t.Fatal(err)
	}
	svc.repo = repository.NewLearningRepository(db)
	ctx := collectorCtx(1, "alice")
	_, _, hash, err := svc.currentQuizEvidence(ctx, 1, testKB, "concept/rag")
	if err != nil {
		t.Fatal(err)
	}
	item := &types.LearningQuizItem{ID: "during-submit", TenantID: 1, KnowledgeBaseID: testKB, Slug: "concept/rag", CorrectKey: "A", EvidenceHash: hash, Status: types.LearningQuizStatusActive}
	if err = svc.repo.UpsertQuizItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	changed := false
	if err = db.Callback().Create().After("gorm:create").Register("audit:edit_evidence", func(tx *gorm.DB) {
		if tx.Statement.Table == "mastery_states" {
			svc.chunkRepo.(*stubChunkRepo).chunks["c1"].Content = "changed during grading"
			changed = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SubmitAnswer(ctx, testKB, item.ID, "A", ""); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("stale submission accepted: %v", err)
	}
	if !changed {
		t.Fatal("test never reached the grading writes")
	}
	for _, table := range []string{"learning_events", "learning_quiz_attempts", "mastery_states"} {
		var n int64
		if err = db.Table(table).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("%s retained stale grading writes: %d %v", table, n, err)
		}
	}
	if err = db.Callback().Create().Remove("audit:edit_evidence"); err != nil {
		t.Fatal(err)
	}
	_, _, item.EvidenceHash, err = svc.currentQuizEvidence(ctx, 1, testKB, item.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.repo.UpsertQuizItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SubmitAnswer(ctx, testKB, item.ID, "A", ""); err != nil {
		t.Fatalf("positive control: %v", err)
	}
}

func TestBenchHistoryUsesProductionLookback(t *testing.T) {
	now := time.Now()
	old := now.Add(-touchLookback - time.Hour)
	history := []types.LearningEvent{{Slug: "concept/old", Type: types.LearningEventQuizWrong, OccurredAt: old}}
	in := BenchHistoryInput(BenchRecommendInput{}, history, nil, now)
	if in.QuizStruggled["concept/old"] || len(in.LastVisit) != 0 || len(in.Recent) != 0 {
		t.Fatalf("expired behaviour entered production input: %+v", in)
	}
	history[0].OccurredAt = now.Add(-time.Minute)
	in = BenchHistoryInput(BenchRecommendInput{}, history, nil, now)
	if !in.QuizStruggled["concept/old"] || len(in.LastVisit) != 1 {
		t.Fatal("positive control: recent wrong answer missing")
	}
}
