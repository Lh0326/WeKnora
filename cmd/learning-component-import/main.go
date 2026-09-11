// Imports a reviewed prototype pack into the explicitly selected local KB.
// It writes shared definitions only: never a person's actions or mastery.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/url"
	"os"
)

func run() error {
	kbID := flag.String("kb", "", "local knowledge-base ID")
	file := flag.String("file", "", "component pack JSON")
	draftOutput := flag.String("draft-output", "", "generate candidates to this JSON file without importing")
	draftSlugs := flag.String("draft-slugs", "", "comma-separated Wiki slugs for draft generation")
	draftSources := flag.String("draft-sources", "", "optional offline evaluation pages JSON; never imported into the KB")
	draftLimit := flag.Int("draft-limit", 4, "maximum draft candidates, 1–8")
	draftModelSource := flag.String("draft-model-source", "wiki", "use the KB's configured wiki or summary model")
	draftModelID := flag.String("draft-model", "", "optional existing tenant model ID for this draft call only")
	flag.Parse()
	if *kbID == "" || (*file == "") == (*draftOutput == "") || (*draftSources != "" && *draftOutput == "") {
		return fmt.Errorf("explicit -kb and exactly one of -file or -draft-output are required")
	}
	_ = godotenv.Load(".env")
	_ = godotenv.Overload(".env.local")
	if os.Getenv("DB_USER") == "" || os.Getenv("DB_NAME") == "" {
		return fmt.Errorf("local database configuration is missing")
	}
	// Deliberately local: never follow a production DB_HOST from an environment file.
	dsn := url.URL{Scheme: "postgres", User: url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD")), Host: "127.0.0.1:5432", Path: os.Getenv("DB_NAME"), RawQuery: "sslmode=disable&connect_timeout=5"}
	db, err := gorm.Open(postgres.Open(dsn.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return fmt.Errorf("could not connect to local development database")
	}
	var kb types.KnowledgeBase
	if err = db.Where("id = ?", *kbID).First(&kb).Error; err != nil {
		return fmt.Errorf("selected local KB unavailable")
	}
	if *draftOutput != "" {
		return generateDraft(db, &kb, *draftSlugs, *draftSources, *draftOutput, *draftLimit, *draftModelSource, *draftModelID)
	}
	var pages []*types.WikiPage
	if err = db.Where("knowledge_base_id = ? AND tenant_id = ?", *kbID, kb.TenantID).Find(&pages).Error; err != nil {
		return err
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var pack struct {
		Components []types.ComponentDefinition `json:"components"`
	}
	if err = json.Unmarshal(data, &pack); err != nil {
		return err
	}
	rows, err := learning.ValidateComponentPack(kb.TenantID, *kbID, pack.Components, pages)
	if err != nil {
		return err
	}
	repo := repository.NewLearningRepository(db).(interfaces.LearningComponentRepository)
	if err = repo.SaveComponents(context.Background(), rows); err != nil {
		return err
	}
	fmt.Printf("Imported %d grounded components into the selected local KB. Personal history unchanged.\n", len(rows))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
