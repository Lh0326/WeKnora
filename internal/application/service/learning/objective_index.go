package learning

import "github.com/Tencent/WeKnora/internal/types"

type objectiveFamilyKey struct{ objective, family string }

// Request-local indexes avoid scanning the full user history for every
// objective or bank item. They only partition facts: the existing derivation
// still checks identity, contract versions, independence and eligibility.
type objectiveFactIndex struct {
	quiz         map[string][]types.LearningQuizAttempt
	tasks        map[string][]types.LearningTaskAttempt
	quizItems    map[string]bool
	quizFamilies map[objectiveFamilyKey]bool
	taskItems    map[string]bool
	taskFamilies map[objectiveFamilyKey]bool
}

func indexObjectiveFacts(attempts []types.LearningQuizAttempt, legacyByItem map[string]string, tasks []types.LearningTaskAttempt) objectiveFactIndex {
	idx := objectiveFactIndex{
		quiz: map[string][]types.LearningQuizAttempt{}, tasks: map[string][]types.LearningTaskAttempt{},
		quizItems: map[string]bool{}, quizFamilies: map[objectiveFamilyKey]bool{},
		taskItems: map[string]bool{}, taskFamilies: map[objectiveFamilyKey]bool{},
	}
	for _, a := range attempts {
		id := a.ObjectiveID
		if id == "" {
			id = legacyByItem[a.QuizItemID]
		}
		idx.quiz[id] = append(idx.quiz[id], a)
		idx.quizItems[a.QuizItemID] = true
		idx.quizFamilies[objectiveFamilyKey{a.ObjectiveID, a.FamilyID}] = true
	}
	for _, a := range tasks {
		idx.tasks[a.ObjectiveID] = append(idx.tasks[a.ObjectiveID], a)
		idx.taskItems[a.TaskID] = true
		idx.taskFamilies[objectiveFamilyKey{a.ObjectiveID, a.FamilyID}] = true
	}
	return idx
}

func (idx objectiveFactIndex) quizSeen(item types.LearningQuizItem) bool {
	return idx.quizItems[item.ID] || idx.quizFamilies[objectiveFamilyKey{item.ObjectiveID, item.FamilyID}]
}

func (idx objectiveFactIndex) taskSeen(task types.LearningTask) bool {
	return idx.taskItems[task.ID] || idx.taskFamilies[objectiveFamilyKey{task.ObjectiveID, task.FamilyID}]
}
