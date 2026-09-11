import { get, post, put } from '../../utils/request'

// Stage-4 objective layer API: separated progress, objective view, and
// the short-path planner. All numbers are evidence counts — never a
// "mastery probability" — and the UI labels match these field names.

export interface ObjectiveEvidence {
  families_passed: string[]
  families_passed_historic: string[]
  eligible_passes: number
  eligible_failures: number
  last_pass_at?: string
  last_failure_at?: string
  contract_met_at?: string
  stale_passes: number
  legacy_attempts: number
  source: string
  unknown?: boolean
}

export interface ObjectiveViewEntry {
  slug: string
  objective_id: string
  title: string
  behavior: string
  capability_type: string
  contract_type: string
  contract_version: string
  content_version: string
  objective_status: string
  state: string // unverified | partial | verified | conflicting | stale_content
  evidence: ObjectiveEvidence
  exposure: { reads: number; cites: number; agent_reads: number; last_at?: string }
  self_report: { direction?: string; at?: string; skipped: boolean }
  recency: { last_verified_at?: string; last_evidence_at?: string; last_exposure_at?: string }
  path_status: string // available | user_retired | challenge_pending
}

export interface LearningNodeView {
 estimate?:LearningEstimate;
 review?:LearningReviewStatus;
 slug:string; title:string; folder_id:string; folder_name:string;
 state:'unseen'|'learning'|'self_known'|'verified'|'review'; reads:number;cites:number;
 last_read_at?:string; declared_at?:string;updated:boolean;objective_total:number;objective_verified:number;
}
export interface ObjectiveViewResponse {
 nodes?:LearningNodeView[];
  projection_version: string
  entries: ObjectiveViewEntry[]
  legacy_by_node?: Record<string, number>
}

export interface ObjectiveProgressSummary {
  verified_objectives: number
  total_objectives: number
  conflicting_objectives: number
  stale_objectives: number
  partial_objectives: number
  untested_objectives: number
  unverified_objectives: number
  contact_nodes: number
  total_nodes: number
  self_report_count: number
  legacy_attempt_count: number
  goal_set_version?: string
  goal_verified_objectives: number
  goal_total_objectives: number
}

export interface LearningPathStep {
  id: string
  completed: boolean
  slug: string
  title?: string
  objective?: string
  action: string // overview | read | bridge | practice | verify | review
  minutes: number
  done_when: string
  eligibility: string // default | override
  requires?: string
  next?: string
  reason: { code: string; detail?: string; evidence?: string[] }
}

export interface LearningPathPlan {
  personalization?: 'available' | 'disabled' | 'unavailable' | 'no_signals'
  policy_version: string
  steps: LearningPathStep[]
  degrade?: string
}

export interface ColdStartRequest {
 use_memory?:boolean;
 goal_slugs?:string[];
  excluded_slugs?: string[]
  goal_objectives?: string[]
  depth?: string
  time_budget_minutes?: number
  fast_track?: boolean
}
export type EstimateLevel = 'unseen'|'introduced'|'developing'|'self_reported'|'familiar'|'review'
export interface LearningEstimate {
 model_version:string;content_version?:string;level:EstimateLevel;familiarity:number;lower:number;upper:number;
 performance_observed?:boolean;self_report?:string;read_priority?:number;coverage:number;opportunities:number;expected_gain:number;information_gain?:number;answers:number;corrections:number;basis:string;last_study_at?:string;last_answer_at?:string;last_answer_prediction?:number;
 memory?:{model_version:string;stability:number;difficulty:number;retrievability:number;due_at:string;observations:number};
}
export type RecallAction='enroll'|'pause'|'again'|'hard'|'good'|'easy'
export interface LearningReviewStatus {
 revision:string; content_version:string; content_changed:boolean; policy_version:string;
 active:boolean; due:boolean; due_at:string; interval_days:number; repetitions:number;
 ease:number; last_rating?:string; early_practice:boolean;
}
export interface LearningReviewInput {slug:string;action:RecallAction;revision:string;content_version:string;request_id:string}
export async function updateReviewSchedule(kb:string,input:LearningReviewInput):Promise<LearningReviewStatus>{
 const res=await post(`/api/v1/learning/kb/${kb}/recall`,input);return (res as any).data??res
}

export interface LearningPlanSettings {
 revision:string; limit_to_folder:boolean; folder_id:string; depth:string;
 time_budget_minutes:number; use_memory:boolean; goal_objectives:string[];
}
export async function getPlanPreferences(kbId:string):Promise<LearningPlanSettings>{
 const r=await get(`/api/v1/learning/kb/${kbId}/plan-preferences`);return (r as any).data??r
}
export async function savePlanPreferences(kbId:string,input:LearningPlanSettings):Promise<LearningPlanSettings>{
 const r=await put(`/api/v1/learning/kb/${kbId}/plan-preferences`,input);return (r as any).data??r
}

export async function getObjectiveProgress(kbId: string, goals: string[] = []): Promise<ObjectiveProgressSummary> {
  const query = new URLSearchParams(); goals.forEach(id => query.append("goal", id))
  const res = await get(`/api/v1/learning/kb/${kbId}/objective-progress?${query}`)
  return (res as any).data ?? res
}

export async function getObjectiveView(kbId: string): Promise<ObjectiveViewResponse> {
  const res = await get(`/api/v1/learning/kb/${kbId}/objectives`)
  return (res as any).data ?? res
}

export async function postShortPath(kbId: string, req: ColdStartRequest): Promise<LearningPathPlan> {
  const res = await post(`/api/v1/learning/kb/${kbId}/path`, req)
  return (res as any).data ?? res
}

export interface StructuredTask {
 mode: string; family_id: string;
 id: string; title: string; scenario: string; objective_id: string;
 fields: Array<{id: string; label: string; type: string; options: string[]}>;
 assistance_mode: string; content_version: string; rubric_version: string;
}
export interface TaskVerdict { passed: boolean; eligible: boolean; grade_reason: string; checks: Array<{id:string;passed:boolean}> }
export async function getTasks(kbId: string, objective: string): Promise<StructuredTask[]> {
 const r=await get(`/api/v1/learning/kb/${kbId}/tasks?objective_id=${encodeURIComponent(objective)}`)
 return (r as any).data ?? r
}
export async function submitTask(kbId: string, task: string, answers: Record<string,string>, mode: string): Promise<TaskVerdict> {
 const r=await post(`/api/v1/learning/kb/${kbId}/tasks/${encodeURIComponent(task)}/answer`, {answers,assistance_mode:mode})
 return (r as any).data ?? r
}

export async function setNodeState(kbId:string,slug:string,state:'read'|'known'|'review') {
 return post(`/api/v1/learning/kb/${kbId}/node-state`,{slug,state})
}
