/**
 * Learning tab API (topic 4: knowledge network & guided learning).
 * Module shape follows api/wiki/index.ts: typed wrappers over the shared
 * request helpers, one function per backend route.
 *
 * Every endpoint derives the subject from the caller's principal — there
 * is no subject parameter to pass (or to abuse).
 */
import { get, post, put, del } from '../../utils/request';

// ---- response payload types ----

/** 今日学习摘要：答题数/正确数/今日点亮/连续天数（读时从事件派生）。 */
export interface TodaySummary {
  answers: number;
  correct_count: number;
  lit_today: number;
  streak_days: number;
}

export interface LearningProgress {
  total_nodes: number;
  lit_nodes: number;
  levels: Record<string, number>;
  units: { folder_id: string; folder_name: string; total: number; lit: number }[];
  today?: TodaySummary;
}

export interface MasteryView {
  slug: string;
  level: string;
  p_eff: number;
  evidence_count: number;
  low_confidence: boolean;
  last_evidence_at: string;
  /** 最新一次原始事件时间（含零权重活动：48h内重读、答「不确定」）——星图闪烁依据。 */
  last_activity_at: string;
  /** 7 天内最新一次自评（悬停回显用；不参与档位计算）。 */
  self_assess?: { direction: 'up' | 'down'; event_type: string; occurred_at: string };
  /** 节点页中文标题（读时批量解析）；页面不存在时为空。 */
  title?: string;
  /** 档位内进度（0–1，p_eff 在当前档区间的位置）与下档路径提示。 */
  tier_progress?: number;
  next_tier_hint?: string;
}

export interface Recommendation {
  slug: string;
  title: string;
  reason: string;
  has_quiz: boolean;
  /** 展示层附加（读时派生）：节点所属文件夹、可用题数、当前档位。 */
  folder_name?: string;
  quiz_count?: number;
  level?: string;
  /** 可解释性数值：有效掌握度、证据构成；faded=有历史但跌出点亮线（已淡化）。 */
  p_eff?: number;
  evidence_count?: number;
  positive_count?: number;
  negative_count?: number;
  faded?: boolean;
  /** 档位内进度（0–1）与下档路径提示（直接证据门控的解锁条件）。 */
  tier_progress?: number;
  next_tier_hint?: string;
}

export interface QuizSourceDoc {
  knowledge_id: string;
  /** 文档标题（来自页面 SourceRefs）；解析不到时为空。 */
  title?: string;
  chunk_count: number;
}

export interface QuizQuestion {
  id: string;
  question: string;
  options: Record<string, string>;
  chunk_refs: string[];
  /** 出处文档（chunk→文档确定性解析），供作答后溯源跳转。 */
  source_docs?: QuizSourceDoc[];
}

export interface AnswerResult {
  correct: boolean;
  correct_key: string;
  explanation: string;
  chunk_refs: string[];
  /** 确定性复习计划：档位预计保持 N 天（衰减到降档阈值的时长）。 */
  next_review_days?: number;
  /** 「不确定」申报：按声明处理（零权重事件），不奖不罚。 */
  unsure?: boolean;
  /** 作答前后的有效掌握度（快变量正反馈：45% → 58%）。 */
  p_eff_before?: number;
  p_eff_after?: number;
}

export interface TimelineItem {
  event_type: string;
  slug: string;
  /** 触达页面的中文标题（读时批量解析）；页面已不存在时为空，前端回退显示 slug。 */
  title: string;
  page_type?: string;
  weight: number;
  occurred_at: string;
  session_id: string;
  message_id: string;
}

export interface LearningSettings {
  collect_disabled: boolean;
}

/** 被动变化（遗忘动态）：读时派生、绝不落库——与时间线的主动事件分离。 */
export interface PassiveChange {
  slug: string;
  title?: string;
  /** 证据挣到的档位（不衰减）与遗忘侵蚀后的当前档位。 */
  anchor_level: string;
  view_level: string;
  base_p: number;
  p_eff: number;
  days_idle: number;
  stability_days: number;
  /** 距降档还剩的天数；已降档时缺省（提示改为"尽快复习"）。 */
  next_review_days?: number;
  demoted: boolean;
  low_confidence: boolean;
  last_evidence_at: string;
}

export interface PassiveChangesSummary {
  demoted_count: number;
  due_soon_count: number;
  items: PassiveChange[];
}

interface Envelope<T> {
  success: boolean;
  data: T;
  total?: number;
}

// ---- endpoints ----

export function getLearningProgress(kbId: string) {
  return get<Envelope<LearningProgress>>(`/api/v1/learning/kb/${kbId}/progress`);
}

export function getLearningMastery(kbId: string) {
  return get<Envelope<MasteryView[]>>(`/api/v1/learning/kb/${kbId}/map`);
}

export function getLearningRecommend(kbId: string, limit = 5) {
  return get<Envelope<Recommendation[]>>(`/api/v1/learning/kb/${kbId}/recommend?limit=${limit}`);
}

// 遗忘动态：非用户操作（被动）导致的档位/掌握度变化，读时派生。
export function getLearningChanges(kbId: string, limit = 20) {
  return get<Envelope<PassiveChangesSummary>>(`/api/v1/learning/kb/${kbId}/changes?limit=${limit}`);
}

export function getLearningQuiz(kbId: string, slug: string) {
  return get<Envelope<QuizQuestion[]>>(`/api/v1/learning/kb/${kbId}/quiz?slug=${encodeURIComponent(slug)}`);
}

export function submitLearningAnswer(kbId: string, itemId: string, chosenKey: string) {
  return post<Envelope<AnswerResult>>(`/api/v1/learning/kb/${kbId}/quiz/${itemId}/answer`, {
    chosen_key: chosenKey,
  });
}

// Reading a wiki page is a deliberate low-trust touch (§3.3.6 signal);
// the backend dedupes per slug per 48h window, so fire-and-forget is safe.
export function recordWikiRead(kbId: string, slug: string) {
  return post<Envelope<null>>(`/api/v1/learning/kb/${kbId}/read`, { slug });
}

export type SelfAssessDirection = 'up' | 'down';
export type SelfAssessDownReason = 'all' | 'doc_gap' | 'doc_updated' | 'quiz_easy';

/** Skill-matrix self-assessment: up lifts the score into the mastered band (tier still gated on quiz proof); down demotes by reason. */
export function selfAssess(kbId: string, slug: string, direction: SelfAssessDirection, reason?: SelfAssessDownReason) {
  return post<Envelope<null>>(`/api/v1/learning/kb/${kbId}/self-assess`, { slug, direction, reason });
}

export function getLearningTimeline(kbId: string, page = 1, pageSize = 20) {
  return get<Envelope<TimelineItem[]> & { total: number }>(
    `/api/v1/learning/kb/${kbId}/timeline?page=${page}&page_size=${pageSize}`,
  );
}

export interface LearningExportPayload {
  exported_at: string;
  events: unknown[];
  mastery: unknown[];
  topic_maps: unknown[];
  quiz_attempts: unknown[];
}

export function exportLearningProfile() {
  return get<Envelope<LearningExportPayload>>(`/api/v1/learning/export`);
}

export function deleteLearningProfile(optOut: boolean) {
  return del<Envelope<null>>(`/api/v1/learning/profile${optOut ? '?opt_out=true' : ''}`);
}

export function getLearningSettings() {
  return get<Envelope<LearningSettings>>(`/api/v1/learning/settings`);
}

export function updateLearningSettings(collectDisabled: boolean) {
  return put<Envelope<LearningSettings>>(`/api/v1/learning/settings`, {
    collect_disabled: collectDisabled,
  });
}

// Convenience export for callers that only need the raw JSON download.
export function downloadLearningProfile(): Promise<string> {
  return exportLearningProfile().then((res: any) => JSON.stringify(res.data ?? res, null, 2));
}
