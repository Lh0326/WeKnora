/** Read-only presentation of the subject-wide export, never a mastery projection. */
type Row = Record<string, unknown>
const row = (value: unknown): Row => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Row : {}
const text = (value: unknown, fallback = '未记录'): string => typeof value === 'string' && value !== '' ? value : fallback
const rows = (value: unknown): Row[] => Array.isArray(value) ? value.map(row) : []
const escapeHTML = (value: unknown): string => String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]!)
const count = (value: unknown): string => Array.isArray(value) ? String(value.length) : value === null ? '0' : '未提供'
const version = (value: unknown): string => text(value, '版本未知')
const result = (value: unknown): string => value === true ? '通过' : value === false ? '未通过' : '结果未记录'
const eligibility = (value: unknown): string => value === true ? '当时计入检查证据' : value === false ? '当时未计入检查证据' : '证据资格未记录'
const assistance = (value: unknown): string => ({ open_book: '开卷', closed_book: '闭卷', assistant_helped: '使用助手或额外帮助' } as Record<string, string>)[text(value)] ?? text(value, '作答条件未记录')

function table(headers: string[], values: unknown[][]): string {
  if (!values.length) return '<p class="muted">此部分没有返回记录。</p>'
  return `<div class="table-wrap"><table><thead><tr>${headers.map(h => `<th>${escapeHTML(h)}</th>`).join('')}</tr></thead><tbody>${values.map(cells => `<tr>${cells.map(cell => `<td>${escapeHTML(cell)}</td>`).join('')}</tr>`).join('')}</tbody></table></div>`
}

function when(value: unknown): string {
  if (typeof value !== 'string' || !value || value.startsWith('0001-')) return '时间未记录'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? `时间格式未知：${value}` : date.toLocaleString('zh-CN', { hour12: false, timeZoneName: 'short' })
}

const eventNames: Record<string, string> = {
  source_read: '原文阅读', node_read: '节点阅读', wiki_tool_read: '页面阅读', wiki_deep_read: '页面深读',
  node_known: '自评已了解', node_review: '自评需复习', self_assess_up: '自评更熟悉',
  self_assess_down_all: '自评不熟悉', self_assess_down_doc_gap: '自评存在内容缺口',
  self_assess_down_doc_updated: '自评需要学习更新', self_assess_down_quiz_easy: '自评题目过易',
  quiz_correct: '题目答对', quiz_wrong: '题目答错', quiz_unsure: '题目不确定',
  answer_cite: '问答引用触达', cross_ref: '跨问题触达', re_ask: '再次提问',
  topic_signal: '话题关联信号', backfill_cite: '历史引用回填', agent_read: '助手代查页面',
  review_again: '回忆自评：忘记', review_hard: '回忆自评：吃力',
  review_good: '回忆自评：想起', review_easy: '回忆自评：轻松',
}

function eventInfo(event: Row): { category: string; label: string; detail: string } {
  const type = text(event.event_type, '')
  if (type === 'component_learning') {
    const fact = row(event.review_data)
    switch (fact.action) {
      case 'open': return { category: '其他', label: '打开学习目标', detail: '接触记录，尚不代表完成阅读' }
      case 'read': return { category: '阅读', label: '学习单元阅读', detail: '已记录阅读机会；不等于通过检查' }
      case 'known': return { category: '自评', label: '自评已了解', detail: '本人声明，不是独立检查结论' }
      case 'difficult': return { category: '自评', label: '自评有困难', detail: '本人声明，不是检查失败记录' }
      case 'recall': return { category: '回忆自评', label: '回忆反馈', detail: ({ 1: '忘记', 2: '吃力', 3: '想起', 4: '轻松' } as Record<string, string>)[String(fact.rating)] ?? '回忆档位未记录' }
      case 'check': return { category: '检查', label: '学习单元检查', detail: `${fact.correct === true ? '答对' : fact.correct === false ? '未答对（可能包含不确定）' : '结果未记录'}；${eligibility(fact.eligible)}；题目 ${text(fact.check_id)}` }
      default: return { category: '其他', label: `学习单元动作：${text(fact.action)}`, detail: '未识别动作，原始数据保留在 JSON' }
    }
  }
  const category = ['source_read', 'node_read', 'wiki_tool_read', 'wiki_deep_read'].includes(type) ? '阅读'
    : type.startsWith('self_assess_') || ['node_known', 'node_review'].includes(type) ? '自评'
    : ['review_again', 'review_hard', 'review_good', 'review_easy'].includes(type) ? '回忆自评'
    : ['quiz_correct', 'quiz_wrong', 'quiz_unsure'].includes(type) ? '检查' : '其他'
  return { category, label: eventNames[type] ?? `其他事件：${text(type)}`, detail: category === '检查' ? '事件痕迹；资格与作答条件见题目记录，不据此认证能力' : category === '其他' ? '间接活动或系统痕迹，不等于本人阅读或检查通过' : '历史操作记录' }
}

function eventTitle(event: Row): string {
  const title = text(row(event.review_data).title, '')
  if (title) return title
  const slug = text(event.slug, '')
  return event.event_type === 'component_learning' ? '未命名学习单元'
    : slug && !/[0-9a-f]{8}-[0-9a-f-]{27,}/i.test(slug) ? slug : '未命名历史节点'
}

function groupEvents(events: Row[]): unknown[][] {
  const groups = new Map<string, Row[]>()
  const recordedVersions = new Map<string, Map<string, string>>()
  const targetKey = (event: Row) => JSON.stringify([event.event_type === 'component_learning' ? 'KC' : 'Wiki', event.slug])
  // Human labels are local to each object and follow its first observed versions.
  // They never claim to be the publisher's revision number or the current version.
  for (const event of [...events].reverse()) {
    if (typeof event.content_version !== 'string' || !event.content_version) continue
    const versions = recordedVersions.get(targetKey(event)) ?? new Map<string, string>()
    if (!versions.has(event.content_version)) versions.set(event.content_version, `记录版本 ${versions.size + 1}`)
    recordedVersions.set(targetKey(event), versions)
  }
  for (const event of events) {
    // KC / Wiki and each recorded version remain separate, including unknown versions.
    const key = JSON.stringify([event.event_type === 'component_learning' ? 'KC' : 'Wiki', event.slug, event.content_version ?? ''])
    const group = groups.get(key) ?? []
    group.push(event)
    groups.set(key, group)
  }
  return [...groups.values()].map(group => {
    const newest = group[0]!
    const categories = group.map(eventInfo)
    const latestSelf = categories.find(info => info.category === '自评')
    const label = recordedVersions.get(targetKey(newest))?.get(text(newest.content_version)) ?? '版本未知'
    return [eventTitle(newest), newest.event_type === 'component_learning' ? '学习单元' : 'Wiki / 历史节点', label,
      categories.filter(info => info.category === '阅读').length,
      latestSelf?.label ?? '无自评记录', categories.filter(info => info.category === '检查').length,
      categories.filter(info => info.category === '回忆自评').length, when(newest.occurred_at)]
  })
}

/** All interpolated payload values are escaped as text; no script, link or remote asset is emitted. */
export function renderLearningProfileHTML(value: unknown, currentKbId: string): string {
  const payload = row(value)
  if (typeof payload.exported_at !== 'string' || !(Array.isArray(payload.events) || payload.events === null)) {
    throw new Error('学习记录响应不完整')
  }
  const sections = ['events', 'quiz_attempts', 'task_attempts', 'objectives', 'mastery', 'topic_maps', 'skips', 'plan_preferences'] as const
  const summaries = rows(payload.kb_summary)
  const kbIds = new Set(summaries.map(kb => text(kb.kb_id, '-')))
  for (const key of sections) for (const item of rows(payload[key])) kbIds.add(text(item.knowledge_base_id, '-'))
  const ids = [...kbIds].sort((a, b) => a === currentKbId ? -1 : b === currentKbId ? 1 : a.localeCompare(b))
  const scoped = (key: typeof sections[number], id: string) => rows(payload[key]).filter(item => text(item.knowledge_base_id, '-') === id)
  const name = (id: string) => text(summaries.find(kb => kb.kb_id === id)?.kb_name, id === '-' ? '未标注知识库' : '未命名知识库')
  const availability = (id: string) => {
    const exists = summaries.find(kb => kb.kb_id === id)?.exists
    return exists === true ? '导出时知识库可解析' : exists === false ? '导出时知识库无法解析（可能已删除或不可访问）' : '知识库有效性未提供'
  }
  const kbCount = (key: typeof sections[number], id: string) => Array.isArray(payload[key]) || payload[key] === null ? scoped(key, id).length : '未提供'
  const overview = table(['知识库', '范围标记', '学习事件', '题目作答', '结构化任务', '知识库状态'], ids.map(id => [name(id), id === currentKbId ? '当前打开的知识库' : '其他知识库', kbCount('events', id), kbCount('quiz_attempts', id), kbCount('task_attempts', id), availability(id)]))
  const bodies = ids.map((id, index) => {
    const timestamp = (event: Row) => Number.isNaN(Date.parse(text(event.occurred_at))) ? -Infinity : Date.parse(text(event.occurred_at))
    const events = scoped('events', id).sort((a, b) => timestamp(b) - timestamp(a))
    const attempts = scoped('quiz_attempts', id)
    const tasks = scoped('task_attempts', id)
    const objectives = scoped('objectives', id)
    const states: Record<string, string> = { unverified: '尚未验证', partial: '部分证据', verified: '满足该目标验证契约', conflicting: '证据有冲突', stale_content: '内容版本已变化', legacy_unverified: '历史记录，未验证' }
    return `<section><h2>${index + 1}. ${escapeHTML(name(id))}${id === currentKbId ? ' · 当前库' : ''}</h2>
      <p class="muted">${escapeHTML(availability(id))}</p>
      <h3>按记录对象与版本回顾</h3><p class="muted">阅读与检查列为事件条数；自评列为该版本最近一次声明。“记录版本 1、2…”是各对象在本次导出内的编号，不代表发布顺序或当前版本；完整标识保留在明细。不同版本、Wiki 节点和学习单元分开列出；这里不推断当前学习状态。</p>
      ${table(['记录对象', '类型', '记录时内容版本', '阅读事件', '最近自评', '检查事件', '回忆自评事件', '最近事件时间'], groupEvents(events))}
      <details><summary>完整事件明细（${events.length} 条）</summary><p class="muted">知识库标识：${escapeHTML(id)}</p>${table(['时间', '对象', '对象标识', '分类', '动作与结果', '完整内容版本', '来源痕迹'], events.map(event => {
        const info = eventInfo(event), fact = row(event.review_data)
        const session = event.session_id || fact.session_id
        const source = [event.event_type === 'component_learning' ? `学习单元操作；模型版本 ${text(fact.model_version)}` : 'Wiki / 问答 / 历史事件', session ? `会话 ${text(session)}` : '', event.message_id ? `消息 ${text(event.message_id)}` : '', event.original_slug ? `原节点 ${text(event.original_slug)}` : ''].filter(Boolean).join('；')
        return [when(event.occurred_at), eventTitle(event), text(event.slug), info.category, `${info.label}；${info.detail}`, version(event.content_version), source]
      }))}</details>
      <details><summary>题目作答与结构化任务（${kbCount('quiz_attempts', id)} + ${kbCount('task_attempts', id)} 条）</summary>
      <p class="muted">这是作答表的记录，可能与上述事件描述同一次操作，不能相加成学习次数。旧记录缺少资格或版本时保留未知；题干与来源正文不在本导出中。</p>
      ${table(['时间', '类型 / 对象', '结果', '当时证据资格', '作答条件', '内容 / 契约版本'], [...attempts.map(a => [when(a.answered_at), `题目 ${text(a.quiz_item_id)} · ${text(a.slug)}`, a.chosen_key === 'E' || a.chosen_key === '?' ? '不确定' : result(a.is_correct), eligibility(a.eligible), assistance(a.assistance_mode), `${version(a.content_version)} / ${version(a.contract_version)}`]), ...tasks.map(a => [when(a.submitted_at), `任务 ${text(a.task_id)} · ${text(a.slug)}`, result(a.is_passed), eligibility(a.eligible), assistance(a.assistance_mode), `${version(a.content_version)} / ${version(a.contract_version)}`])])}</details>
      <details><summary>接口返回的目标证据状态（${kbCount('objectives', id)} 项）</summary><p class="muted">仅转述服务端已返回的目标状态，可能不是完整目标清单；缺失不等于零掌握。来源引用保留供核对，正文与当前可用性未在此文件中核验。</p>
      ${table(['目标', '服务端状态', '内容 / 契约版本', '来源引用', '证据来源'], objectives.map(o => [text(o.title, text(o.slug)), states[text(o.state)] ?? `未识别状态：${text(o.state)}`, `${version(o.content_version)} / ${version(o.contract_version)}`, Array.isArray(o.source_refs) ? o.source_refs.filter(v => typeof v === 'string').join('；') || '未记录' : '未记录', text(o.source)]))}</details>
      </section>`
  }).join('')
  return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><title>WeKnora 学习记录</title>
  <style>body{margin:0;background:#f5f7f6;color:#24332e;font:15px/1.7 system-ui,sans-serif}main{max-width:1120px;margin:auto;padding:32px 20px}h1{font-size:28px;margin:0}h2{font-size:21px}h3{font-size:16px}.muted{color:#5e6f67;font-size:13px}.note,section{background:#fff;border:1px solid #dae3de;border-radius:12px;padding:20px;margin:20px 0}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse;font-size:13px}th,td{border-bottom:1px solid #dfe6e2;padding:10px;text-align:left;vertical-align:top;overflow-wrap:anywhere;min-width:85px;max-width:340px;white-space:pre-wrap}th{background:#edf4ef}details{margin-top:18px}summary{cursor:pointer;font-weight:600}ul{padding-left:22px}@media print{body{background:white}main{max-width:none;padding:0}section,.note{border:0;padding:8px 0}details>*{display:block}table{font-size:10px}.table-wrap{overflow:visible}}</style></head><body><main>
  <h1>我的学习记录</h1><p>用于离线回顾与打印的历史记录，不是能力认证。需要打印明细时，请先展开相应章节。</p>
  <p class="muted">数据导出时间：${escapeHTML(when(payload.exported_at))} · 范围：当前账号在所有知识库中的个人学习数据（包括共享库记录），不是仅当前库。</p>
  <div class="note"><strong>如何使用这份记录</strong><ul><li>先看在哪些知识库有记录，再按对象和版本查看阅读、自评、检查与回忆反馈。</li><li>阅读表示接触过内容；自评与回忆反馈表示本人感受；检查需结合当时条件与资格理解。它们不合成为掌握率，也不代表当前仍能完成任务。</li><li>内容版本是记录时标识。本文件未查询学习单元当前定义、来源正文或来源是否仍有效，旧版本不能自动作为新版本证据。</li><li>如需完整字段或程序处理，请另存“原始数据 JSON”。HTML 是便于阅读的摘要；JSON 是接口返回的完整数据副本，不承诺一键恢复。</li></ul></div>
  <h2>知识库概览</h2>${overview}${!kbIds.has(currentKbId) ? '<p class="muted">本次导出未返回当前打开知识库的记录；不据此推断该库没有知识内容。</p>' : ''}
  <p class="muted">学习事件 ${escapeHTML(count(payload.events))} 条；题目作答 ${escapeHTML(count(payload.quiz_attempts))} 条；结构化任务 ${escapeHTML(count(payload.task_attempts))} 条。各类记录可能描述同一操作，不相加。</p>
  ${bodies}<section><h2>原始数据中还包含什么</h2>${table(['数据类别', '接口返回条数', '含义'], [['历史状态投影', count(payload.mastery), '历史算法的内部状态；不在此转换为当前掌握概率'], ['话题映射', count(payload.topic_maps), '兴趣或记忆关联，不等于学习表现'], ['跳过偏好', count(payload.skips), '本人设置的推荐偏好，不等于验证通过'], ['学习计划偏好', count(payload.plan_preferences), '时间、目标等个人设置']])}<p class="muted">“未提供”表示本次接口没有该字段；空列表表示该类未返回记录。事件权重、内部模型参数及完整偏好保留在 JSON，不作为本摘要的能力评价。</p></section>
  </main></body></html>`
}
