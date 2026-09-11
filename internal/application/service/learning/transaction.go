package learning

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// runSubject captures an operation's generation before work starts. All reads,
// dedup decisions and writes in fn use the same subject-locked transaction.
func (s *Service) runSubject(ctx context.Context, subject string, collect bool, fn func(context.Context) error) error {
	epoch, err := s.operationEpoch(ctx, subject)
	if err != nil {
		return err
	}
	err = s.repo.WithSubject(ctx, subject, epoch, collect, fn)
	if errors.Is(err, interfaces.ErrLearningCollectionDisabled) {
		return nil
	}
	return err
}

func (s *Service) RecordWikiRead(ctx context.Context, kbID, slug, tier string) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	epoch, err := s.operationEpoch(ctx, scope.SubjectID)
	if err != nil {
		return err
	}
	// The interactive caller needs to distinguish a saved read from disabled
	// telemetry; silently swallowing this outcome produced false UI feedback.
	return s.repo.WithSubject(ctx, scope.SubjectID, epoch, true, func(ctx context.Context) error { return s.recordWikiRead(ctx, kbID, slug, tier) })
}

func (s *Service) RecordAgentRead(ctx context.Context, kbID, slug string) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	return s.runSubject(ctx, scope.SubjectID, true, func(ctx context.Context) error { return s.recordAgentRead(ctx, kbID, slug) })
}

func (s *Service) RecordSelfAssess(ctx context.Context, kbID, slug string, up bool, reason string) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	return s.runSubject(ctx, scope.SubjectID, true, func(ctx context.Context) error { return s.recordSelfAssess(ctx, kbID, slug, up, reason) })
}

func (s *Service) RecordSkip(ctx context.Context, kbID, slug string, skipped bool) error {
	if !learningEnabled() {
		return nil
	}
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return err
	}
	// Explicit preferences remain available when telemetry is disabled.
	return s.runSubject(ctx, scope.SubjectID, false, func(ctx context.Context) error { return s.recordSkip(ctx, kbID, slug, skipped) })
}

func (s *Service) RecordAnswerTouches(ctx context.Context, message *types.Message) {
	if !learningEnabled() || message == nil || len(message.KnowledgeReferences) == 0 {
		return
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok || principal.StorageID() == "" {
		return
	}
	// Best effort at the answer boundary, atomic inside the learning layer.
	_ = s.runSubject(ctx, principal.StorageID(), true, func(ctx context.Context) error { return s.recordAnswerTouches(ctx, message) })
}

func (s *Service) SubmitAnswer(ctx context.Context, kbID, itemID, chosenKey, declaredAssistance string) (*AnswerResult, error) {
	scope, err := resolveReadScope(ctx, kbID)
	if err != nil {
		return nil, err
	}
	epoch, err := s.operationEpoch(ctx, scope.SubjectID)
	if err != nil {
		return nil, err
	}
	var result *AnswerResult
	err = s.repo.WithSubject(ctx, scope.SubjectID, epoch, true, func(ctx context.Context) error {
		var err error
		result, err = s.submitAnswer(ctx, kbID, itemID, chosenKey, declaredAssistance)
		return err
	})
	if errors.Is(err, interfaces.ErrLearningCollectionDisabled) {
		// Consent disables storage, not access to the explanation.
		item := s.findQuizItem(ctx, scope, itemID)
		if item == nil {
			return nil, ErrQuizNotFound
		}
		grade, err := GradeQuiz(item.CorrectKey, chosenKey, 0)
		if err != nil {
			return nil, err
		}
		return &AnswerResult{Correct: grade.Correct, CorrectKey: item.CorrectKey, Explanation: item.Explanation,
			ChunkRefs: []string(item.ChunkRefs), Unsure: grade.EventType == types.LearningEventQuizUnsure}, nil
	}
	return result, err
}

type capturedLearningEpoch struct {
	subject string
	epoch   int64
	err     error
}

func (s *Service) operationEpoch(ctx context.Context, subject string) (int64, error) {
	if v, ok := ctx.Value(types.LearningEpochContextKey).(capturedLearningEpoch); ok {
		if v.subject != subject {
			return 0, ErrNoLearningScope
		}
		return v.epoch, v.err
	}
	return s.repo.GetSubjectEpoch(ctx, subject)
}
func (s *Service) CaptureCollectionContext(ctx context.Context) context.Context {
	if ctx.Value(types.LearningEpochContextKey) != nil {
		return ctx
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return ctx
	}
	value := capturedLearningEpoch{subject: principal.StorageID()}
	if !learningEnabled() {
		value.err = interfaces.ErrLearningCollectionDisabled
	} else {
		value.epoch, value.err = s.repo.GetSubjectEpoch(ctx, value.subject)
	}
	return context.WithValue(ctx, types.LearningEpochContextKey, value)
}
