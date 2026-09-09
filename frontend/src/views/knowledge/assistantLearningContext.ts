import { getLearningMastery, getLearningProgress, getLearningRecommend, getLearningTimeline } from '@/api/learning/index'

/**
 * Learning snapshot for the KB knowledge assistant.
 *
 * The assistant answers "我的学习情况如何 / 下一步学什么" questions from REAL
 * learning-subsystem data instead of guessing: progress totals, the current
 * page's mastery state, the recommender's next steps (with attribution) and
 * the recent trajectory. All of it rides the existing host-context query
 * prefix (see utils/embedContext.ts) — no backend change, JWT-scoped APIs.
 *
 * Failure is silent by design: without a snapshot the assistant behaves
 * exactly as before (it just doesn't get the learning fields).
 */

const TTL_MS = 120_000

const TIER_LABEL: Record<string, string> = {
  mastered: '已验证',
  familiar: '熟悉',
  touched: '已接触',
  unseen: '未覆盖',
}
const EVENT_LABEL: Record<string, string> = {
  answer_cite: '问答引用',
  re_ask: '复问',
  cross_ref: '跨会话重访',
  quiz_correct: '校验答对',
  quiz_wrong: '校验答错',
  quiz_unsure: '校验不确定',
  wiki_deep_read: '深读',
  wiki_read: '阅读',
  wiki_tool_read: '页面阅读',
  agent_read: '助手查页',
  self_assess_up: '自评更熟',
  backfill_cite: '历史回填',
  topic_signal: '话题信号',
}

interface Snapshot {
  progressLine: string
  masteryBySlug: Map<string, { level: string; p_eff: number; evidence: number; last: string; quizHint?: string }>
  recLine: string
  trajectoryLine: string
}

let cache: Snapshot | null = null
let cacheKb = ''
let cacheAt = 0
let inflight: Promise<void> | null = null

function pct(v: number): number {
  return Math.round(Math.max(0, Math.min(1, v)) * 100)
}

function dayLabel(iso: string): string {
  const d = new Date(iso)
  const now = new Date()
  const diff = Math.round(
    (new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() -
      new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()) / 86400000,
  )
  return diff <= 0 ? '今天' : diff === 1 ? '昨天' : `${d.getMonth() + 1}/${d.getDate()}`
}

async function fetchSnapshot(kbId: string): Promise<Snapshot> {
  const [progressRes, masteryRes, recRes, timelineRes] = await Promise.all([
    getLearningProgress(kbId),
    getLearningMastery(kbId),
    getLearningRecommend(kbId, 5),
    getLearningTimeline(kbId, 1, 10),
  ])
  const progress: any = (progressRes as any).data ?? progressRes
  const mastery: any[] = ((masteryRes as any).data ?? masteryRes) || []
  const recs: any[] = ((recRes as any).data ?? recRes) || []
  const timeline: any[] = ((timelineRes as any).data ?? timelineRes) || []

  const levels = progress?.levels || {}
  const today = progress?.today
  const progressLine =
    `覆盖 ${progress?.lit_nodes ?? 0}/${progress?.total_nodes ?? 0} · ` +
    `${TIER_LABEL.mastered}${levels.mastered ?? 0} ${TIER_LABEL.familiar}${levels.familiar ?? 0} ` +
    `${TIER_LABEL.touched}${levels.touched ?? 0} ${TIER_LABEL.unseen}${levels.unseen ?? 0}` +
    (today?.answers ? ` · 今日答题${today.answers}（对${today.correct_count}）` : '')

  const map = new Map<
    string,
    { level: string; p_eff: number; evidence: number; last: string; quizHint?: string }
  >()
  for (const m of mastery) {
    if (!m?.slug) continue
    map.set(m.slug, {
      level: TIER_LABEL[m.level] || m.level,
      p_eff: pct(m.p_eff ?? 0),
      evidence: m.evidence_count ?? 0,
      last: m.last_activity_at || m.last_evidence_at || '',
      quizHint: m.next_tier_hint || undefined,
    })
  }

  const recLine = recs
    .slice(0, 5)
    .map((r, i) => `${i + 1}.${r.title || r.slug}（${r.reason || ''}${r.p_eff != null ? '，p_eff ' + pct(r.p_eff) + '%' : ''}）`)
    .join('；')

  const trajectoryLine = timeline
    .slice(0, 10)
    .map((e) => `${dayLabel(e.occurred_at)}${EVENT_LABEL[e.event_type] || e.event_type}「${e.title || e.slug}」`)
    .join('；')

  return { progressLine, masteryBySlug: map, recLine, trajectoryLine }
}

/** TTL-guarded refresh; concurrent callers share one flight. */
export function ensureAssistantLearning(kbId: string): Promise<void> {
  if (!kbId) return Promise.resolve()
  if (cache && cacheKb === kbId && Date.now() - cacheAt < TTL_MS) return Promise.resolve()
  if (inflight && cacheKb === kbId) return inflight
  cacheKb = kbId
  inflight = fetchSnapshot(kbId)
    .then((snap) => {
      cache = snap
      cacheAt = Date.now()
    })
    .catch(() => {
      // keep whatever cache we had (or none) — silent degradation
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

/** Invalidate so the next ensure refetches (e.g. after an answer landed). */
export function invalidateAssistantLearning(): void {
  cacheAt = 0
}

/**
 * Host-context fields for the assistant: KB progress, the active page's
 * mastery row (if any), top recommendations and the recent trajectory.
 */
export function assistantLearningFieldsFor(slug: string | null): Record<string, string> | null {
  if (!cache) return null
  const fields: Record<string, string> = {
    kb_progress: cache.progressLine,
    next_recommended: cache.recLine || '（暂无——节点都已覆盖或尚未开始）',
    recent_trajectory: cache.trajectoryLine || '（还没有学习事件）',
  }
  const node = slug ? cache.masteryBySlug.get(slug) : null
  if (slug) {
    fields.current_node = node
      ? `${TIER_LABEL[node.level] || node.level}，有效熟悉度 ${node.p_eff}%，证据 ${node.evidence} 条${node.quizHint ? '，' + node.quizHint : ''}`
      : '未覆盖，尚无学习记录'
  }
  return fields
}
