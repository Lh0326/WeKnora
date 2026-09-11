package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Offline evaluation inputs remain outside the database. The exact same
// generator and screener are used, but these pages cannot be imported as real
// KB sources unless they independently exist there and pass source validation.
type draftFixturePages struct {
	interfaces.WikiPageRepository
	pages []*types.WikiPage
}

func (p *draftFixturePages) ListAll(context.Context, string) ([]*types.WikiPage, error) {
	return p.pages, nil
}

func generateDraft(db *gorm.DB, kb *types.KnowledgeBase, slugsCSV, sourcesFile, output string, limit int, modelSource, modelID string) error {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, kb.TenantID)
	kbRepo := repository.NewKnowledgeBaseRepository(db)
	modelService := appservice.NewModelService(repository.NewModelRepository(db), kbRepo, nil, nil, nil, nil)
	var pages interfaces.WikiPageRepository = repository.NewWikiPageRepository(db)
	sourceMode := "selected_local_kb_pages"
	if sourcesFile != "" {
		data, err := os.ReadFile(sourcesFile)
		if err != nil {
			return err
		}
		var fixtures []*types.WikiPage
		if err = json.Unmarshal(data, &fixtures); err != nil {
			return err
		}
		for _, p := range fixtures {
			if p != nil {
				p.TenantID = kb.TenantID
				p.KnowledgeBaseID = kb.ID
			}
		}
		pages = &draftFixturePages{pages: fixtures}
		sourceMode = "offline_evaluation_only"
	}
	svc := learning.NewService(nil, pages, nil, modelService, kbRepo, nil)
	slugs := []string{}
	for _, s := range strings.Split(slugsCSV, ",") {
		if s = strings.TrimSpace(s); s != "" {
			slugs = append(slugs, s)
		}
	}
	if modelID != "" {
		modelSource = ""
	}
	result, err := svc.DraftComponents(ctx, kb.ID, interfaces.ComponentDraftRequest{Slugs: slugs, Limit: limit, ModelSource: modelSource, ModelID: modelID})
	if err != nil {
		return err
	}
	modelID = result.ModelID
	record := struct {
		SourceMode  string                           `json:"source_mode"`
		ModelID     string                           `json:"model_id"`
		GeneratedAt time.Time                        `json:"generated_at"`
		Result      *interfaces.ComponentDraftResult `json:"result"`
	}{sourceMode, modelID, time.Now().UTC(), result}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
		return err
	}
	fmt.Printf("Prepared %d candidates (%s); shared materials and personal history unchanged.\n", len(result.Candidates), result.Status)
	return nil
}
