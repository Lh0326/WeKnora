package learning

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Health DTO aliases live on the interfaces package like every read-path
// contract in this layer, so the handler depends on interfaces only.
type (
	KnowledgeHealth       = interfaces.KnowledgeHealth
	HealthFolder          = interfaces.HealthFolder
	HealthExpert          = interfaces.HealthExpert
	HealthRisk            = interfaces.HealthRisk
	HealthMaintenanceMark = interfaces.HealthMaintenanceMark
)

// Risk kinds of the health view.
const (
	riskSinglePoint = "single_point"
	riskStaleDoc    = "stale_doc"
)

// healthMaintenanceWindow bounds the maintenance-marks panel: one month of
// self-assessment labels is a work queue, older ones are history.
const healthMaintenanceWindow = 30 * 24 * time.Hour

// KnowledgeHealth assembles the owner/admin org view of one KB: coverage
// counts, expert nodes, single-person and stale-document risks, folder
// roll-ups and the recent self-assessment maintenance marks. The whole
// path is deterministic — folded rows, node pages and event labels through
// pure arithmetic, zero LLM calls — so the dashboard is reproducible and
// free to render server-side or client-side.
//
// No principal is resolved here, deliberately. The route is owner/admin
// gated (OwnedWikiKBOrAdmin + KBAccessRead, the activity-feed precedent),
// and KBAccessRead rewrites the effective tenant onto the context before
// this runs — that tenant is the only identity the aggregate needs. Every
// subject contributes counts and nothing else: no subject id, name or
// per-person row ever leaves this function, so there is no viewer-side
// forgery surface at all.
func (s *Service) KnowledgeHealth(ctx context.Context, kbID string) (*KnowledgeHealth, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, ErrNoLearningScope
	}
	pages, err := s.nodePages(ctx, kbID)
	if err != nil {
		return nil, err
	}
	// Unlike the personal views, a mastery read failure is fatal: silently
	// aggregating an empty row set would paint "nobody knows anything" on
	// an owner's dashboard — a false org signal, worse than a 500.
	rows, err := s.repo.ListMasteryByKB(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}

	// Folder names are display-only labels; a missing name degrades to the
	// id-less root convention rather than failing the view (GetProgress's
	// own softness, same reason).
	folderNames := map[string]string{}
	if folders, err := s.wikiRepo.ListAllFolders(ctx, kbID); err == nil {
		for _, f := range folders {
			if f != nil && f.ID != "" {
				folderNames[f.ID] = f.Name
			}
		}
	} else {
		logger.Warnf(ctx, "learning: health folder read failed (kb %s): %v", kbID, err)
	}
	// Maintenance marks are an advisory panel: a read failure leaves the
	// queue empty and the rest of the dashboard standing.
	var marks []types.LearningEvent
	if m, err := s.repo.ListMaintenanceMarks(ctx, tenantID, kbID, time.Now().Add(-healthMaintenanceWindow)); err != nil {
		logger.Warnf(ctx, "learning: health maintenance read failed (kb %s): %v", kbID, err)
	} else {
		marks = m
	}
	return deriveKnowledgeHealth(rows, pages, folderNames, marks, time.Now()), nil
}

// deriveKnowledgeHealth is the pure, table-testable core of the health
// view. Deterministic by construction: fixed input order (repo rows and
// marks arrive ordered; pages are keyed, not positional), map iteration
// only feeds sorted collectors, and every threshold is a named constant
// from constants.go.
func deriveKnowledgeHealth(
	rows []types.MasteryState,
	pages []*types.WikiPage,
	folderNames map[string]string,
	marks []types.LearningEvent,
	now time.Time,
) *KnowledgeHealth {
	// Machine principals are not people. API-key calls (subject ids of the
	// form "api_*:...") legitimately write learning events — they answer
	// questions and read pages like anyone else — but counting an API key
	// in "N 人有学习记录" or "who is familiar with this" misleads the owner
	// about the org's human coverage. Drop them from every aggregate; the
	// machine subject's own export/delete paths are unaffected.
	rows = slices.DeleteFunc(rows, func(r types.MasteryState) bool {
		return r.SubjectID != "" && strings.HasPrefix(r.SubjectID, "api_")
	})

	// Node universe lookups: a mastery row whose slug no longer resolves
	// to an entity/concept page (page deleted or renamed between reads)
	// describes a node the org view cannot show, so it never enters the
	// aggregates. SubjectsActive still counts it — the person was active in
	// this KB even if their node vanished.
	titleBySlug := map[string]string{}
	for _, p := range pages {
		if p != nil && p.Slug != "" {
			titleBySlug[p.Slug] = p.Title
		}
	}

	// Per node: the derived bands and bench, computed from each (subject,
	// row) pair's decayed probability. A zero row (no evidence, no
	// timestamps) would read as the raw sigmoid(0)=0.5 — "a statement
	// nobody earned", ListMasteryView's own words — and is zeroed here for
	// the same honesty.
	type nodeAgg struct {
		covered   map[string]bool // subjects at/above the covered band
		familiar  map[string]bool // subjects at/above the familiar band
		learnedBy map[string]bool // subjects with any folded evidence
		best      float64         // max p_eff across subjects
	}
	subjects := map[string]bool{}
	nodeBySlug := map[string]*nodeAgg{}
	for i := range rows {
		r := &rows[i]
		subjects[r.SubjectID] = true
		if _, isNode := titleBySlug[r.Slug]; !isNode {
			continue
		}
		agg := nodeBySlug[r.Slug]
		if agg == nil {
			agg = &nodeAgg{
				covered: map[string]bool{}, familiar: map[string]bool{}, learnedBy: map[string]bool{},
			}
			nodeBySlug[r.Slug] = agg
		}
		p := EffectiveP(StateFromModel(r), now)
		if r.EvidenceCount == 0 && r.LastEvidenceAt.IsZero() {
			p = 0
		}
		if p > agg.best {
			agg.best = p
		}
		if r.EvidenceCount > 0 {
			agg.learnedBy[r.SubjectID] = true
		}
		if p >= LevelTouchedUp {
			agg.covered[r.SubjectID] = true
		}
		if p >= LevelFamiliarUp {
			agg.familiar[r.SubjectID] = true
		}
	}

	health := &KnowledgeHealth{
		NodesTotal:     len(pages),
		SubjectsActive: len(subjects),
		Folders:        []HealthFolder{},
		Experts:        []HealthExpert{},
		Risks:          []HealthRisk{},
		Maintenance:    []HealthMaintenanceMark{},
	}

	// One pass over the node pages drives the summary, the expert list, the
	// single-person risks and the folder roll-ups together — every count is
	// keyed by slug, never by row position.
	type folderAgg struct {
		folder HealthFolder
	}
	folderIndex := map[string]*folderAgg{}
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		fa := folderIndex[p.FolderID]
		if fa == nil {
			fa = &folderAgg{folder: HealthFolder{FolderID: p.FolderID, FolderName: folderNames[p.FolderID]}}
			folderIndex[p.FolderID] = fa
		}
		fa.folder.TotalNodes++
		agg := nodeBySlug[p.Slug]
		if agg == nil {
			continue // never touched: counts toward totals only
		}
		if len(agg.covered) > 0 {
			health.NodesCovered++
			fa.folder.CoveredNodes++
		}
		// FamiliarUsers is the folder's bus factor: the deepest familiar
		// bench any of its nodes has.
		if n := len(agg.familiar); n > fa.folder.FamiliarUsers {
			fa.folder.FamiliarUsers = n
		}
		if n := len(agg.familiar); n >= 1 {
			health.Experts = append(health.Experts, HealthExpert{
				Slug: p.Slug, Title: p.Title, FamiliarCount: n,
			})
			// Exactly one familiar person and real evidence behind it: the
			// org loses this knowledge with one departure.
			if n == 1 && len(agg.learnedBy) > 0 {
				health.Risks = append(health.Risks, HealthRisk{
					Kind: riskSinglePoint, Key: p.Slug, Title: p.Title,
					FamiliarCount: n, BestPEff: agg.best,
					Note: "only one person is familiar with this knowledge",
				})
			}
		}
	}

	// stale_doc: a source document everyone learned from and has since
	// forgotten. Group node pages by their SourceRefs doc id; a doc is
	// stale when the best p_eff over every covering (node, subject) pair
	// fell below the covered band while at least one covering pair still
	// carries folded evidence — learned before, decayed now.
	type docAgg struct {
		title        string
		best         float64
		learnedPairs int // distinct (node, subject) pairs with evidence
	}
	docs := map[string]*docAgg{}
	for _, p := range pages {
		if p == nil {
			continue
		}
		agg := nodeBySlug[p.Slug]
		seenDocs := map[string]bool{}
		for _, ref := range p.SourceRefs {
			id, title := sourceRefParts(ref)
			if id == "" || seenDocs[id] {
				continue
			}
			seenDocs[id] = true
			d := docs[id]
			if d == nil {
				d = &docAgg{title: title}
				docs[id] = d
			}
			if agg != nil {
				if agg.best > d.best {
					d.best = agg.best
				}
				d.learnedPairs += len(agg.learnedBy)
			}
		}
	}
	for id, d := range docs {
		if d.best >= LevelTouchedUp || d.learnedPairs == 0 {
			continue
		}
		health.Risks = append(health.Risks, HealthRisk{
			Kind: riskStaleDoc, Key: id, Title: d.title,
			FamiliarCount: d.learnedPairs, BestPEff: d.best,
			Note: "learned before but everyone's mastery has decayed",
		})
	}

	// Maintenance marks: the org's content-work queue, grouped by
	// (kind, slug) across subjects — three people flagging the same doc
	// gap is one strong mark, not three.
	type markKey struct{ kind, slug string }
	type markAgg struct {
		mark HealthMaintenanceMark
	}
	markIndex := map[markKey]*markAgg{}
	for _, ev := range marks {
		key := markKey{kind: ev.Type, slug: ev.Slug}
		m := markIndex[key]
		if m == nil {
			m = &markAgg{mark: HealthMaintenanceMark{Kind: ev.Type, Slug: ev.Slug, Title: titleBySlug[ev.Slug]}}
			markIndex[key] = m
		}
		m.mark.Count++
		if ev.OccurredAt.After(m.mark.LatestAt) {
			m.mark.LatestAt = ev.OccurredAt
		}
	}
	for _, m := range markIndex {
		health.Maintenance = append(health.Maintenance, m.mark)
	}
	for id := range folderIndex {
		health.Folders = append(health.Folders, folderIndex[id].folder)
	}

	// Deterministic output order everywhere: count desc, then title, then
	// key — two opens of the same moment render identically.
	sort.Slice(health.Folders, func(i, j int) bool {
		a, b := health.Folders[i], health.Folders[j]
		if a.TotalNodes != b.TotalNodes {
			return a.TotalNodes > b.TotalNodes
		}
		if a.FolderName != b.FolderName {
			return a.FolderName < b.FolderName
		}
		return a.FolderID < b.FolderID
	})
	sort.Slice(health.Experts, func(i, j int) bool {
		a, b := health.Experts[i], health.Experts[j]
		if a.FamiliarCount != b.FamiliarCount {
			return a.FamiliarCount > b.FamiliarCount
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.Slug < b.Slug
	})
	sort.Slice(health.Risks, func(i, j int) bool {
		a, b := health.Risks[i], health.Risks[j]
		if a.FamiliarCount != b.FamiliarCount {
			return a.FamiliarCount > b.FamiliarCount
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Key < b.Key
	})
	sort.Slice(health.Maintenance, func(i, j int) bool {
		a, b := health.Maintenance[i], health.Maintenance[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Slug < b.Slug
	})
	return health
}
