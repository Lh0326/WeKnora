import { shallowReadonly, shallowRef, type InjectionKey } from 'vue'
import type { ColdStartRequest, LearningPathPlan, ObjectiveViewResponse } from '@/api/learning/objectives'

export interface LearningSessionSnapshot {
  kbId: string
  label: string
  request: ColdStartRequest
  status: 'loading' | 'ready' | 'error'
  view: ObjectiveViewResponse | null
  plan: LearningPathPlan | null
  updatedAt: number | null
}

/** Owned by one KB screen, never shared through a module-global cache. */
export function createLearningSession() {
  const snapshot = shallowRef<LearningSessionSnapshot | null>(null)
  let scope = '', kb = '', generation = 0

  function setScope(identity: string, kbId: string) {
    if (scope === identity && kb === kbId) return
    scope = identity
    kb = kbId
    generation++
    snapshot.value = null
  }

  function begin(kbId: string, request: ColdStartRequest, label: string) {
    if (!kbId || kbId !== kb) return null
    const current = ++generation
    // Copy selections: editing the settings drawer cannot silently change
    // the scope of the plan currently displayed or sent to the assistant.
    const frozen: ColdStartRequest = {
      ...request,
      goal_slugs: [...(request.goal_slugs || [])],
      goal_objectives: [...(request.goal_objectives || [])],
      excluded_slugs: [...(request.excluded_slugs || [])],
    }
    snapshot.value = { kbId, label, request: frozen, status: 'loading', view: snapshot.value?.view ?? null, plan: null, updatedAt: null }
    return {
      complete(view: ObjectiveViewResponse, plan: LearningPathPlan) {
        if (current !== generation) return
        snapshot.value = { kbId, label, request: frozen, status: 'ready', view, plan, updatedAt: Date.now() }
      },
      fail() {
        if (current !== generation || !snapshot.value) return
        snapshot.value = { ...snapshot.value, status: 'error', plan: null }
      },
    }
  }

  function clear() { generation++; snapshot.value = null }
  // Consumers cannot mutate the channel. The producer retains its response
  // objects, avoiding a second independent recommendation request.
  return { snapshot: shallowReadonly(snapshot), setScope, begin, clear }
}

export type LearningSession = ReturnType<typeof createLearningSession>
export const learningSessionKey: InjectionKey<LearningSession> = Symbol('learning-session')
