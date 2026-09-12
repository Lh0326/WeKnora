import test from 'node:test'
import assert from 'node:assert/strict'
import { renderLearningProfileHTML } from './profileExport'

const exportedAt = '2026-09-12T10:00:00Z'
const event = (action: string, extra: Record<string, unknown> = {}) => ({
  knowledge_base_id: 'current', slug: 'kc:one', event_type: 'component_learning',
  content_version: 'version-one', occurred_at: exportedAt,
  review_data: { title: '参数边界', action, model_version: 'model-v2' }, ...extra,
})
const payload = (extra: Record<string, unknown> = {}) => ({
  exported_at: exportedAt, events: [], quiz_attempts: [], task_attempts: [],
  kb_summary: [{ kb_id: 'current', kb_name: '当前课程', exists: true }], ...extra,
})

test('the readable export includes other KBs and identifies the current KB without implying it is the only scope', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('read'), event('read', { knowledge_base_id: 'other' })],
    kb_summary: [{ kb_id: 'other', kb_name: '历史课程', exists: false }, { kb_id: 'current', kb_name: '当前课程', exists: true }] }), 'current')
  assert.match(html, /当前账号在所有知识库/)
  assert.match(html, /1\. 当前课程 · 当前库/)
  assert.match(html, /2\. 历史课程/)
  assert.match(html, /可能已删除或不可访问/)
  assert.match(html, /学习事件 2 条/)
})

test('KC, Wiki and different content versions cannot collapse into one summary row', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('read'), event('known', { content_version: 'version-two' }),
    event('read', { event_type: 'wiki_tool_read', review_data: null })] }), 'current')
  const overview = html.slice(html.indexOf('<h3>按记录对象'), html.indexOf('<details>'))
  assert.equal((overview.match(/<tr>/g) ?? []).length, 4) // header plus three distinct records
  assert.match(overview, /学习单元/)
  assert.match(overview, /Wiki \/ 历史节点/)
  assert.match(overview, /记录版本 1/)
  assert.match(overview, /记录版本 2/)
  assert.doesNotMatch(overview, /version-one|version-two/)
  assert.match(html, /version-one/)
  assert.match(html, /version-two/)
  assert.match(html, /旧版本不能自动作为新版本证据/)
})

test('read, self-report, recall, practice and independent check remain different evidence', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('read'), event('known'), event('difficult'),
    event('recall', { review_data: { action: 'recall', rating: 2 } }),
    event('check', { review_data: { action: 'check', correct: true, eligible: false } }),
    event('check', { review_data: { action: 'check', correct: false, eligible: true } }),
    event('check', { review_data: { action: 'check' } })] }), 'current')
  assert.match(html, /已记录阅读机会；不等于通过检查/)
  assert.match(html, /本人声明，不是独立检查结论/)
  assert.match(html, /自评有困难；本人声明，不是检查失败记录/)
  assert.match(html, /回忆反馈；吃力/)
  assert.match(html, /答对；当时未计入检查证据/)
  assert.match(html, /未答对（可能包含不确定）；当时计入检查证据/)
  assert.match(html, /结果未记录；证据资格未记录/)
})

test('agent activity is not counted as human reading and raw logits never become a probability', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('read', { event_type: 'agent_read' })],
    mastery: [{ knowledge_base_id: 'current', logit: 0.999991, evidence_count: 900, level: 'mastered' }] }), 'current')
  assert.match(html, /助手代查页面/)
  assert.match(html, /间接活动或系统痕迹，不等于本人阅读/)
  assert.doesNotMatch(html, /0\.999991|900|mastered/)
  assert.match(html, /不在此转换为当前掌握概率/)
})

test('opening a learning target is a known contact action and does not count as completed reading', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('open')] }), 'current')
  const overview = html.slice(html.indexOf('<h3>按记录对象'), html.indexOf('<details>'))
  assert.match(overview, /<td>记录版本 1<\/td><td>0<\/td><td>无自评记录<\/td>/)
  assert.match(html, /打开学习目标；接触记录，尚不代表完成阅读/)
  assert.doesNotMatch(html, /未识别动作/)
})

test('latest self-report uses actual instants, including differing timezone offsets', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('known', { occurred_at: '2026-09-12T10:00:00+08:00' }),
    event('difficult', { occurred_at: '2026-09-12T03:00:00Z' })] }), 'current')
  const overview = html.slice(html.indexOf('<h3>按记录对象'), html.indexOf('<details>'))
  assert.match(overview, /自评有困难/)
  assert.doesNotMatch(overview, /自评已了解/)
})

test('all payload text is escaped and the file contains no active script, links or remote assets', () => {
  const attack = '</style><script>alert("x")</script><img src=x onerror=alert(1)>&\''
  const html = renderLearningProfileHTML(payload({ kb_summary: [{ kb_id: 'current', kb_name: attack }],
    events: [event('read', { content_version: attack, review_data: { action: attack, title: attack, session_id: attack } })],
    objectives: [{ knowledge_base_id: 'current', title: attack, source_refs: [attack], state: attack }],
    quiz_attempts: [{ knowledge_base_id: 'current', quiz_item_id: attack, assistance_mode: attack }] }), 'current')
  assert.doesNotMatch(html, /<(?:script|img|iframe|a|link)\b|<[a-z][\w-]*\b[^>]*\s(?:href|src|onerror)=/)
  assert.match(html, /&lt;script&gt;/)
  assert.match(html, /&quot;x&quot;/)
  assert.match(html, /Content-Security-Policy/)
  assert.match(html, /default-src 'none'/)
})

test('missing collections and missing versions stay unknown, while explicit null arrays are empty', () => {
  const html = renderLearningProfileHTML({ exported_at: exportedAt, events: [event('read', { content_version: undefined })], quiz_attempts: null }, 'current')
  assert.match(html, /版本未知/)
  assert.match(html, /题目作答 0 条；结构化任务 未提供 条/)
  assert.match(html, /来源是否仍有效/)
  assert.match(html, /未提供/)
})

test('attempt records do not double event totals and unknown result or eligibility is not a failure', () => {
  const html = renderLearningProfileHTML(payload({ events: [event('check')],
    quiz_attempts: [{ knowledge_base_id: 'current', chosen_key: 'E', is_correct: false, assistance_mode: 'open_book' }, { knowledge_base_id: 'current' }],
    task_attempts: [{ knowledge_base_id: 'current', is_passed: true, eligible: false, assistance_mode: 'assistant_helped' }] }), 'current')
  assert.match(html, /学习事件 1 条；题目作答 2 条；结构化任务 1 条/)
  assert.match(html, /不能相加成学习次数/)
  assert.match(html, /<td>不确定<\/td>/)
  assert.match(html, /<td>结果未记录<\/td>/)
  assert.match(html, /<td>证据资格未记录<\/td>/)
  assert.match(html, /<td>开卷<\/td>/)
  assert.match(html, /<td>使用助手或额外帮助<\/td>/)
})

test('only server-provided objective states are translated and incomplete inventories remain explicit', () => {
  const html = renderLearningProfileHTML(payload({ objectives: [{ knowledge_base_id: 'other', title: '目标甲', state: 'verified', content_version: 'old', source_refs: ['concept/source'], source: 'attempts' }] }), 'current')
  assert.match(html, /满足该目标验证契约/)
  assert.match(html, /可能不是完整目标清单/)
  assert.match(html, /concept\/source/)
  assert.match(html, /未命名知识库/)
  assert.match(html, /知识库标识：other/)
})

test('empty exports and absent current KB do not imply the KB has no learning content', () => {
  const html = renderLearningProfileHTML(payload({ kb_summary: [] }), 'current')
  assert.match(html, /未返回当前打开知识库的记录/)
  assert.match(html, /不据此推断该库没有知识内容/)
  assert.doesNotMatch(html, /全部掌握|已完成学习/)
})

test('formatting does not mutate the machine export or discard future fields in the original object', () => {
  const source = payload({ events: [event('known'), event('read')], future_field: { untouched: ['full-copy'] } })
  const before = JSON.stringify(source)
  renderLearningProfileHTML(source, 'current')
  assert.equal(JSON.stringify(source), before)
})

test('an error envelope cannot masquerade as an empty successful export', () => {
  for (const invalid of [null, {}, { success: false, error: 'unavailable' }, { exported_at: exportedAt, events: 'bad' }]) {
    assert.throws(() => renderLearningProfileHTML(invalid, 'current'), /响应不完整/)
  }
})

test('the default view uses names and local version labels, keeping long identifiers inside details', () => {
  const id = 'aabbccdd-1234-4567-8900-aabbccddeeff'
  const hash = 'abcdef0123456789'.repeat(4)
  const html = renderLearningProfileHTML(payload({ kb_summary: [], events: [event('read', {
    knowledge_base_id: id, slug: `kc:${id}`, content_version: hash,
    review_data: { action: 'read', title: '工具参数选择' },
  })] }), id)
  const defaultView = html.replace(/<details>[\s\S]*?<\/details>/g, '')
  assert.match(defaultView, /未命名知识库/)
  assert.match(defaultView, /工具参数选择/)
  assert.match(defaultView, /记录版本 1/)
  assert.doesNotMatch(defaultView, new RegExp(`${id}|${hash}`))
  assert.match(html, new RegExp(id))
  assert.match(html, new RegExp(hash))
})
