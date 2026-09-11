import test from 'node:test'
import assert from 'node:assert/strict'
import { createLearningSession } from './learningSession'
import { snapshotFields, type AssistantLearningSnapshot } from '../assistantLearningContext'
import type { LearningPathPlan, ObjectiveViewResponse } from '@/api/learning/objectives'

const view: ObjectiveViewResponse = { projection_version: 'test', entries: [], nodes: [] }
function plan(title: string): LearningPathPlan {
  return { policy_version: 'test', steps: [{ id: title, slug: title, title, action: 'read', minutes: 2, completed: false, done_when: '确认理解', eligibility: 'default', reason: { code: 'plan_reason_continue_module', detail: '继续你选择的检索模块' } }] }
}
const defaultSnapshot: AssistantLearningSnapshot = { view, plan: plan('全库默认内容'), trajectory: '' }

test('assistant uses exactly the five-minute module plan, including exclusions and the displayed explanation', () => {
  const session = createLearningSession()
  session.setScope('alice:tenant', 'kb')
  session.begin('kb', { goal_slugs: ['检索'], excluded_slugs: ['已跳过'], time_budget_minutes: 5, depth: 'operate' }, '检索模块')!.complete(view, plan('检索配置'))
  const fields = snapshotFields(defaultSnapshot, null, session.snapshot.value)!
  assert.match(fields.learning_scope!, /检索模块；预算 5 分钟；深度 完成操作；已临时跳过 1/)
  assert.match(fields.next_recommended!, /检索配置.*继续你选择的检索模块/)
  assert.doesNotMatch(fields.next_recommended!, /全库默认内容/)
})

test('a late plan cannot overwrite a newer module or a new user, even in the same KB', () => {
  const session = createLearningSession()
  session.setScope('alice', 'kb')
  const first = session.begin('kb', {}, '基础')!
  const second = session.begin('kb', {}, '应用')!
  second.complete(view, plan('应用'))
  first.complete(view, plan('基础'))
  assert.equal(session.snapshot.value?.plan?.steps[0]?.title, '应用')
  session.setScope('bob', 'kb')
  first.complete(view, plan('Alice 的内容'))
  assert.equal(session.snapshot.value, null)
  assert.equal(session.begin('other-kb', {}, '其他库'), null)
})

test('editing draft settings does not mutate the applied plan scope', () => {
  const session = createLearningSession()
  session.setScope('alice', 'kb')
  const request = { goal_slugs: ['基础'], excluded_slugs: ['a'], time_budget_minutes: 5 }
  const pending = session.begin('kb', request, '基础')!
  request.goal_slugs.push('应用')
  request.excluded_slugs.length = 0
  request.time_budget_minutes = 30
  pending.complete(view, plan('基础'))
  assert.deepEqual(session.snapshot.value?.request.goal_slugs, ['基础'])
  assert.deepEqual(session.snapshot.value?.request.excluded_slugs, ['a'])
  assert.equal(session.snapshot.value?.request.time_budget_minutes, 5)
})

test('loading, failure and an empty plan never fall back to unrelated cached recommendations', () => {
  const session = createLearningSession()
  session.setScope('alice', 'kb')
  const pending = session.begin('kb', { time_budget_minutes: 5 }, '检索')!
  let fields = snapshotFields(defaultSnapshot, null, session.snapshot.value)!
  assert.match(fields.next_recommended!, /正在更新/)
  pending.fail()
  fields = snapshotFields(defaultSnapshot, null, session.snapshot.value)!
  assert.match(fields.learning_plan_status!, /失败/)
  assert.match(fields.next_recommended!, /暂不可用/)
  session.begin('kb', {}, '检索')!.complete(view, { policy_version: 'test', steps: [] })
  fields = snapshotFields(defaultSnapshot, null, session.snapshot.value)!
  assert.match(fields.next_recommended!, /没有待执行步骤/)
  assert.doesNotMatch(fields.next_recommended!, /全库默认内容/)
})

test('independent KB screens never share selections or personal state', () => {
  const left = createLearningSession(), right = createLearningSession()
  left.setScope('alice', 'kb'); right.setScope('bob', 'kb')
  left.begin('kb', {}, 'Alice')!.complete(view, plan('Alice'))
  assert.equal(right.snapshot.value, null)
  const pending = left.begin('kb', {}, 'Alice')!
  left.clear()
  pending.complete(view, plan('late'))
  assert.equal(left.snapshot.value, null)
})
