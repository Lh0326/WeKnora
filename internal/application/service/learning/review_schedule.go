package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// SM-2 interval/ease recurrence from https://www.super-memory.com/english/ol/sm2.htm.
// Product adaptations: again -> 10-minute relearning, intervals capped at one
// year, early successful practice does not lengthen a schedule. Self-report
// only: these events never contribute objective evidence or mastery weights.
const ReviewPolicyVersion = "sm2-recall-v1"

type reviewRecord struct {
	Policy               string `json:"policy"`
	Sequence             int    `json:"sequence"`
	RequestID            string `json:"request_id"`
	PreviousRevision     string `json:"previous_revision"`
	ClientContentVersion string `json:"client_content_version"`
}

type recallState struct {
	interfaces.LearningReviewStatus
	sequence int
}

var reviewActionTypes = map[string]string{
	"enroll": types.LearningEventReviewEnroll, "pause": types.LearningEventReviewPause,
	"again": types.LearningEventReviewAgain, "hard": types.LearningEventReviewHard,
	"good": types.LearningEventReviewGood, "easy": types.LearningEventReviewEasy,
}

func reviewAction(kind string) string {
	for action, t := range reviewActionTypes {
		if kind == t {
			return action
		}
	}
	return ""
}

func advanceRecall(old recallState, action, content string, at time.Time) recallState {
	next := old
	if old.ContentVersion != content || old.Ease < 1.3 || math.IsNaN(old.Ease) || math.IsInf(old.Ease, 0) {
		next = recallState{LearningReviewStatus: interfaces.LearningReviewStatus{Ease: 2.5, ContentVersion: content, PolicyVersion: ReviewPolicyVersion}}
	}
	next.EarlyPractice = false
	if action == "pause" {
		next.Active = false
		return next
	}
	if action == "enroll" {
		next.Active = true
		if next.DueAt.IsZero() || next.DueAt.Before(at) {
			next.DueAt = at
		}
		return next
	}
	next.Active = true
	next.LastRating = action
	// Re-reading on the same day is useful practice, but cannot turn several
	// quick clicks into weeks of supposedly demonstrated retention.
	if action != "again" && at.Before(next.DueAt) {
		next.EarlyPractice = true
		return next
	}
	quality := map[string]int{"again": 0, "hard": 3, "good": 4, "easy": 5}[action]
	if quality < 3 {
		next.Repetitions = 0
		next.IntervalDays = 0
		next.DueAt = at.Add(10 * time.Minute)
	} else {
		switch next.Repetitions {
		case 0:
			next.IntervalDays = 1
		case 1:
			next.IntervalDays = 6
		default:
			next.IntervalDays = int(math.Ceil(float64(next.IntervalDays) * next.Ease))
		}
		if next.IntervalDays > 365 {
			next.IntervalDays = 365
		}
		next.Repetitions++
		next.DueAt = at.Add(time.Duration(next.IntervalDays) * 24 * time.Hour)
	}
	q := float64(5 - quality)
	next.Ease = math.Max(1.3, next.Ease+0.1-q*(0.08+q*0.02))
	return next
}

func projectRecall(events []types.LearningEvent) *recallState {
	type fact struct {
		event  types.LearningEvent
		record reviewRecord
	}
	facts := []fact{}
	seen := map[string]bool{}
	for _, e := range events {
		if reviewAction(e.Type) == "" || seen[e.ID] {
			continue
		}
		var record reviewRecord
		if json.Unmarshal(e.ReviewData, &record) != nil || record.Policy != ReviewPolicyVersion || record.Sequence < 1 || e.ContentVersion == "" || e.OccurredAt.IsZero() {
			continue
		}
		facts = append(facts, fact{e, record})
		seen[e.ID] = true
	}
	if len(facts) == 0 {
		return nil
	}
	// Occurrence order also preserves history when Wiki aliases merge two
	// former nodes. Writes keep this timestamp monotonic within the node.
	sort.Slice(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if !a.event.OccurredAt.Equal(b.event.OccurredAt) {
			return a.event.OccurredAt.Before(b.event.OccurredAt)
		}
		if a.record.Sequence != b.record.Sequence {
			return a.record.Sequence < b.record.Sequence
		}
		return a.event.ID < b.event.ID
	})
	state := recallState{}
	for _, f := range facts {
		state = advanceRecall(state, reviewAction(f.event.Type), f.event.ContentVersion, f.event.OccurredAt)
		state.Revision = f.event.ID
		state.sequence = f.record.Sequence
	}
	return &state
}

func recallStatus(state *recallState, content string, now time.Time) *interfaces.LearningReviewStatus {
	if state == nil {
		return nil
	}
	status := state.LearningReviewStatus
	status.ContentChanged = state.ContentVersion != content
	status.ContentVersion = content
	status.Due = state.Active && (status.ContentChanged || !state.DueAt.After(now))
	return &status
}

func (s *Service) UpdateReviewSchedule(ctx context.Context, kb string, input interfaces.LearningReviewInput) (*interfaces.LearningReviewStatus, error) {
	scope, err := resolveReadScope(ctx, kb)
	if err != nil {
		return nil, err
	}
	kind := reviewActionTypes[input.Action]
	if _, err := uuid.Parse(input.RequestID); err != nil || len(input.RequestID) > 36 || kind == "" || input.Slug == "" || len(input.Slug) > 512 || len(input.Revision) > 36 || len(input.ContentVersion) > 64 {
		return nil, fmt.Errorf("%w: invalid recall action", ErrInvalidLearningRequest)
	}
	var result *interfaces.LearningReviewStatus
	err = s.runSubject(ctx, scope.SubjectID, false, func(tx context.Context) error {
		page, err := s.currentLearningPage(tx, kb, input.Slug)
		if err != nil {
			return err
		}
		content := nodeContentVersion(page)
		all, err := listEventWindow(tx, s.repo, scope, time.Time{})
		if err != nil {
			return err
		}
		events := []types.LearningEvent{}
		for _, e := range all {
			if e.Slug == input.Slug {
				events = append(events, e)
			}
		}
		state := projectRecall(events)
		for _, e := range events {
			var record reviewRecord
			if json.Unmarshal(e.ReviewData, &record) == nil && record.RequestID == input.RequestID {
				if e.Type != kind || record.PreviousRevision != input.Revision || record.ClientContentVersion != input.ContentVersion {
					return fmt.Errorf("%w: recall request reused with different input", ErrInvalidLearningRequest)
				}
				result = recallStatus(state, content, time.Now())
				return nil
			}
		}
		current := ""
		if state != nil {
			current = state.Revision
		}
		if current != input.Revision {
			return interfaces.ErrLearningReviewConflict
		}
		if input.Action != "enroll" && (state == nil || input.ContentVersion != content) {
			return interfaces.ErrLearningReviewConflict
		}
		if input.Action != "enroll" && input.Action != "pause" && !state.Active {
			return interfaces.ErrLearningReviewConflict
		}
		sequence := 1
		if state != nil {
			sequence = state.sequence + 1
		}
		data, _ := json.Marshal(reviewRecord{Policy: ReviewPolicyVersion, Sequence: sequence, RequestID: input.RequestID, PreviousRevision: input.Revision, ClientContentVersion: input.ContentVersion})
		now := time.Now()
		// Keep preference ordering monotonic across server clock corrections.
		for _, e := range events {
			if (isNodePreference(e.Type) || reviewAction(e.Type) != "") && !now.After(e.OccurredAt) {
				now = e.OccurredAt.Add(time.Microsecond)
			}
		}
		event := types.LearningEvent{ID: uuid.NewString(), TenantID: scope.TenantID, SubjectID: scope.SubjectID, KnowledgeBaseID: kb, Slug: input.Slug, Type: kind, ContentVersion: content, ReviewData: data, OccurredAt: now, Weight: 0}
		if input.Action == "again" {
			if err := s.repo.RemoveSkip(tx, scope, input.Slug); err != nil {
				return err
			}
		}
		if input.Action == "hard" || input.Action == "good" || input.Action == "easy" {
			if err := s.repo.AddSkip(tx, scope, input.Slug, now); err != nil {
				return err
			}
		}
		if err := s.repo.AppendEvent(tx, &event); err != nil {
			return err
		}
		events = append(events, event)
		result = recallStatus(projectRecall(events), content, now)
		return nil
	})
	return result, err
}
