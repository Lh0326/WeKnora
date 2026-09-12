package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Stage 2: the strict verification review chain. LLM drafts (quiz items,
// tasks, objectives) are practice-only until a HUMAN content review
// approves them. The review stamp — reviewer identity, decision, note and
// change kind — is written exclusively by these service entry points from
// an authenticated principal; the generation paths can never call them,
// and no LLM "审核通过" output can produce a review record.

// ErrReviewRejected is returned when a review decision or its deterministic
// pre-checks fail; the subject item stays un-promoted.
var ErrReviewRejected = errors.New("learning: content review rejected")

// ReviewDecision is the review API payload.
type ReviewDecision struct {
	Decision    string `json:"decision"`               // approve | reject
	Reason      string `json:"reason"`                 // machine-readable reason code (e.g. ambiguous_answer)
	Note        string `json:"note"`                   // free-form human note
	ChangeKind  string `json:"change_kind"`            // typographic | semantic | '' (undetermined → conservative semantic)
	ObjectiveID string `json:"objective_id,omitempty"` // item review: bind/repair the objective link
	FamilyID    string `json:"family_id,omitempty"`    // item review: explicit family override
}

// reviewerPrincipal extracts the reviewing human from the request
// context. Machine principals (api_*) cannot review: an API key relaying
// an LLM verdict is exactly the self-approval this chain forbids.
func reviewerPrincipal(ctx context.Context) (string, error) {
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.StorageID() == "" {
		return "", errors.New("learning: review requires an authenticated principal")
	}
	if principal.Type != types.PrincipalWebUser {
		return "", errors.New("learning: machine principals cannot content-review")
	}
	return principal.StorageID(), nil
}

func (r *ReviewDecision) normalize() error {
	r.Decision = strings.ToLower(strings.TrimSpace(r.Decision))
	if r.Decision != "approve" && r.Decision != "reject" {
		return fmt.Errorf("%w: decision must be approve|reject", ErrReviewRejected)
	}
	switch r.ChangeKind {
	case "", "typographic", "semantic":
	default:
		return fmt.Errorf("%w: change_kind must be typographic|semantic|empty", ErrReviewRejected)
	}
	return nil
}

// conservativeChangeKind maps an undeclared change kind to semantic —
// when the reviewer cannot determine typographic vs semantic, the safe
// reading is that evidence must be re-verified.
func conservativeChangeKind(kind string) string {
	if kind == "typographic" {
		return "typographic"
	}
	return "semantic"
}

// FamilyFingerprint digests the objective link plus the NORMALIZED
// correct-answer text: rewording the stem or reshuffling options keeps
// the answer text, keeps the fingerprint, and therefore inherits the
// existing family — a clone never founds a new independent family.
func FamilyFingerprint(objectiveID string, options types.QuizOptions, correctKey string) string {
	answer := strings.ToLower(strings.Join(strings.Fields(options[correctKey]), " "))
	h := sha256.New()
	h.Write([]byte(objectiveID))
	h.Write([]byte{0})
	h.Write([]byte(answer))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// ReviewQuizItem applies a human content review to one draft item.
// Deterministic pre-checks run first (existence, KB scope, live evidence
// hash, structural validity, objective existence); the reviewer's
// approve/reject decision is then recorded with the audit fields. On
// approve with a fingerprint collision against another published item of
// the same objective, the family is merged to the existing one — the
// clone-detection guard. The final semantic judgment (answer ambiguity,
// distractor quality) is the reviewer's: the API records the rejection
// reason instead of pretending to automate it.
func (s *Service) ReviewQuizItem(ctx context.Context, kbID, itemID string, input interfaces.ReviewDecisionInput) (*types.LearningQuizItem, error) {
	dec := reviewDecisionOf(input)
	if err := dec.normalize(); err != nil {
		return nil, err
	}
	reviewer, err := reviewerPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	item, err := s.repo.GetQuizItemByID(ctx, scope.TenantID, itemID)
	if err != nil || item == nil || item.KnowledgeBaseID != scope.KnowledgeBaseID {
		return nil, ErrQuizNotFound
	}

	if dec.Decision == "reject" {
		// Rejection keeps the row as history, out of every serving and
		// scoring path. The reason code is the audit trail (e.g.
		// ambiguous_answer for a fully-cited but ambiguous key).
		item.Status = types.LearningQuizStatusDisabled
		item.Reviewer = reviewer
		item.ReviewNote = strings.TrimSpace(dec.Reason + " " + dec.Note)
		item.ChangeKind = conservativeChangeKind(dec.ChangeKind)
		if err := s.repo.UpsertQuizItem(ctx, item); err != nil {
			return nil, err
		}
		return item, nil
	}

	// ---- deterministic approve-time pre-checks ----
	// 1. Live evidence: the item must still match the current material.
	// (quizEvidenceMatches requires serving status; review needs the raw
	// comparison so drafts can be approved against live evidence.)
	if _, _, hash, err := s.currentQuizEvidence(ctx, item.TenantID, item.KnowledgeBaseID, item.Slug); err != nil || hash == "" || hash != item.EvidenceHash {
		return nil, fmt.Errorf("%w: evidence hash no longer matches current material", ErrReviewRejected)
	}
	// 2. Structural validity (four DISTINCT options, valid key, grounded
	// refs). Duplicate-valued options make the answer ambiguous by
	// construction — rejected deterministically, no semantics needed.
	seen := map[string]bool{}
	for _, key := range []string{"A", "B", "C", "D"} {
		val := normalizeOptionText(item.Options[key])
		if val != "" && seen[val] {
			return nil, fmt.Errorf("%w: duplicate-valued options make the answer ambiguous", ErrReviewRejected)
		}
		seen[val] = true
	}
	// 3. Structural validity proper (four options, valid key, grounded refs).
	page, _, _, err := s.currentQuizEvidence(ctx, item.TenantID, item.KnowledgeBaseID, item.Slug)
	if err != nil || page == nil {
		return nil, ErrReviewRejected
	}
	if validateQuizDraft(quizDraft{
		Question: item.Question, Options: map[string]string(item.Options),
		CorrectKey: item.CorrectKey, Explanation: item.Explanation, ChunkRefs: item.ChunkRefs,
	}, page) == nil {
		return nil, fmt.Errorf("%w: item fails structural validation", ErrReviewRejected)
	}
	// 3. Objective link: strict verification needs a real objective.
	objectiveID := strings.TrimSpace(dec.ObjectiveID)
	if objectiveID == "" {
		objectiveID = item.ObjectiveID
	}
	if objectiveID == "" {
		return nil, fmt.Errorf("%w: strict items must bind to an objective", ErrReviewRejected)
	}
	objective, err := s.verificationObjective(ctx, scope.TenantID, kbID, objectiveID, item.Slug, types.ObjectiveContractConceptTwoFamily)
	if err != nil {
		return nil, err
	}
	item.ObjectiveVersion = objective.ContentVersion

	// 4. Family discipline: explicit override wins; otherwise the
	// fingerprint decides — a collision with a published item of the same
	// objective FORCES the existing family (clones never found families).
	item.ObjectiveID = objectiveID
	fingerprint := FamilyFingerprint(objectiveID, item.Options, item.CorrectKey)
	family := strings.TrimSpace(dec.FamilyID)
	if family == "" {
		family = item.FamilyID
	}
	items, err := s.repo.ListQuizItemsByKB(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	{
		for _, other := range items {
			if other.ID == item.ID || other.ObjectiveID != objectiveID {
				continue
			}
			if other.Status == types.LearningQuizStatusPublished && other.FamilyFingerprint == fingerprint && other.FamilyID != "" {
				if family == "" || family != other.FamilyID {
					family = other.FamilyID // clone merge: same fingerprint → same family
				}
			}
		}
	}
	if family == "" {
		family = "fam-" + fingerprint[:12]
	}
	now := time.Now()
	item.ObjectiveID = objectiveID
	item.FamilyID = family
	item.FamilyFingerprint = fingerprint
	item.AssistanceMode = defaultIfEmpty(item.AssistanceMode, types.AssistanceClosedBook)
	if item.AssistanceMode != types.AssistanceClosedBook {
		return nil, fmt.Errorf("%w: concept contract v1 requires closed-book conditions", ErrReviewRejected)
	}
	item.RubricVersion = defaultIfEmpty(item.RubricVersion, "mcq-rubric-v1")
	item.ScorerVersion = types.MCQScorerVersion
	item.ChangeKind = conservativeChangeKind(dec.ChangeKind)
	// First publish stamps the content version; a semantic re-review of
	// an already-published item bumps it — old attempts keep their frozen
	// version and read as stale evidence in the derivation.
	if item.ContentVersion == "" {
		item.ContentVersion = "v1"
	} else if item.Status == types.LearningQuizStatusPublished && item.ChangeKind == "semantic" {
		item.ContentVersion = bumpVersion(item.ContentVersion)
		objective.ContentVersion = bumpVersion(objective.ContentVersion)
		objective.ReviewNote = "Semantic item correction requires renewed evidence: " + item.ID
		objective.Reviewer = reviewer
		objective.PublishedAt = &now
		if err := s.repo.UpsertObjective(ctx, objective); err != nil {
			return nil, err
		}
		item.ObjectiveVersion = objective.ContentVersion
	}
	item.Status = types.LearningQuizStatusPublished
	item.Reviewer = reviewer
	item.PublishedAt = &now
	item.ReviewNote = dec.Note
	if err := s.repo.UpsertQuizItem(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// ReviewObjective publishes or retires an objective definition — the same
// human-review discipline for the definition layer.
func (s *Service) ReviewObjective(ctx context.Context, kbID, objectiveID string, input interfaces.ReviewDecisionInput) (*types.LearningObjective, error) {
	dec := reviewDecisionOf(input)
	if err := dec.normalize(); err != nil {
		return nil, err
	}
	reviewer, err := reviewerPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	objectives, err := s.repo.ListObjectives(ctx, scope.TenantID, scope.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	var target *types.LearningObjective
	for i := range objectives {
		if objectives[i].ID == objectiveID {
			target = &objectives[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("learning: objective %s not found", objectiveID)
	}
	kind := conservativeChangeKind(dec.ChangeKind)
	now := time.Now()
	if dec.Decision == "reject" {
		target.Status = types.LearningObjectiveStatusRetired
	} else {
		if len(target.ContractParams) > 0 || target.Title == "" || target.Behavior == "" || (target.ContractType != types.ObjectiveContractConceptTwoFamily && target.ContractType != types.ObjectiveContractTaskChecks) {
			return nil, ErrReviewRejected
		}
		page, _, hash, err := s.currentQuizEvidence(ctx, scope.TenantID, kbID, target.Slug)
		if err != nil {
			return nil, err
		}
		if page == nil || hash == "" {
			return nil, fmt.Errorf("%w: missing source", ErrReviewRejected)
		}
		if target.EvidenceHash != "" && target.EvidenceHash != hash && kind == "typographic" {
			return nil, fmt.Errorf("%w: changed source needs semantic review", ErrReviewRejected)
		}
		target.EvidenceHash = hash
		target.SourceRefs = types.RefList(page.ChunkRefs)
		target.ContentVersion = defaultIfEmpty(target.ContentVersion, "v1")
		if kind == "semantic" && target.Status == types.LearningObjectiveStatusPublished {
			target.ContentVersion = bumpVersion(target.ContentVersion)
		}
		target.Status = types.LearningObjectiveStatusPublished
		target.PublishedAt = &now
	}
	// The review trail lands on the definition row itself.
	target.Reviewer = reviewer
	target.ReviewNote = strings.TrimSpace(dec.Reason + " " + dec.Note)
	target.ChangeKind = kind
	target.ContractVersion = defaultIfEmpty(target.ContractVersion, types.ObjectiveContractVersion)
	if err := s.repo.UpsertObjective(ctx, target); err != nil {
		return nil, err
	}
	return target, nil
}

func defaultIfEmpty(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func bumpVersion(v string) string {
	if v == "" {
		return "v2"
	}
	if len(v) > 1 && v[0] == 'v' {
		var n int
		if _, err := fmt.Sscanf(v[1:], "%d", &n); err == nil {
			return fmt.Sprintf("v%d", n+1)
		}
	}
	return v + ".1"
}

func pageTypeOfSlug(slug string) string {
	if i := strings.IndexByte(slug, '/'); i > 0 {
		return slug[:i]
	}
	return "concept"
}

// strictQuizEligibility decides — server-side, frozen on the attempt —
// whether a quiz submission can EVER count as strict objective evidence,
// and why. Practice reasons keep the attempt visible with feedback but
// never promote: draft items, unreviewed legacy items, feedback-exposed
// retries, and assistance modes that do not match the item's designed
// closed-book condition.
func strictQuizEligibility(item *types.LearningQuizItem, declaredAssistance string, priorAttempts int) (bool, string) {
	if item.Status == types.LearningQuizStatusActive {
		return false, "practice_legacy"
	}
	if priorAttempts > 0 {
		return false, "feedback_retry"
	}
	declared := defaultIfEmpty(declaredAssistance, defaultIfEmpty(item.AssistanceMode, types.AssistanceClosedBook))
	if declared == types.AssistanceAssistantHelp || declared != defaultIfEmpty(item.AssistanceMode, types.AssistanceClosedBook) {
		return false, "assistance_not_strict"
	}
	switch item.Status {
	case types.LearningQuizStatusPublished:
		if defaultIfEmpty(item.AssistanceMode, types.AssistanceClosedBook) != types.AssistanceClosedBook {
			return false, "practice_unsupported_mode"
		}
		if item.Reviewer == "" || item.PublishedAt == nil || item.ObjectiveID == "" || item.FamilyID == "" || item.ObjectiveVersion == "" {
			return false, "practice_unreviewed"
		}
		return true, "eligible_independent"
	case types.LearningQuizStatusDraft:
		return false, "practice_draft"
	default:
		// active legacy serving, disabled, stale, unknown.
		return false, "practice_legacy"
	}
}

// reviewDecisionOf converts the transport DTO into the service-layer
// decision value.
func reviewDecisionOf(in interfaces.ReviewDecisionInput) ReviewDecision {
	return ReviewDecision{
		Decision: in.Decision, Reason: in.Reason, Note: in.Note,
		ChangeKind: in.ChangeKind, ObjectiveID: in.ObjectiveID, FamilyID: in.FamilyID,
	}
}

// normalizeOptionText folds an option to its comparison form.
func normalizeOptionText(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(v), " "))
}
