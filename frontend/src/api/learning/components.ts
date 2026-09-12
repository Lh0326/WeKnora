import {get,post} from '@/utils/request'

export interface ComponentCheck {id:string;family:string;question:string;options:Record<string,string>}
export interface ComponentRelevance {weight:number;sources:number;links:{kind:string;label:string;shared_targets:number;reason:string}[]}
export interface ComponentPlanInfo {policy_version:string;exact:boolean;expanded:number;candidates:number;utility:number;upper_bound:number;check_budget:number;notice?:string;topic_scope?:string;focus_goal_id?:string;focus_goal_title?:string;focus_reason?:string;stage?:string;progression?:string;limitations?:string[]}
export interface LearningComponent {
 id:string;version:string;available:boolean;source_problem?:string
 relevance?:ComponentRelevance
 material:{key:string;title:string;topic:string;condition:string;goal:string;explanation:string;example:string;minutes:number;provenance:string;prerequisites:string[];related:string[];sources:{slug:string;quote:string;role:string}[];checks:ComponentCheck[]}
 state:{level:string;reads:number;checks:number;passes:number;practice:number;legacy_touches:number;legacy_component_touches?:number;familiarity:number;performance_observed?:boolean;lower:number;upper:number;basis:string;self_report?:string;last_study_at?:string;due_at?:string;seen_check_ids:string[]}
}
export interface ComponentStep {id:string;title:string;action:string;reason:string;minutes:number;utility?:number;conditional?:boolean;prerequisite_ids?:string[]}
export interface ComponentView {model_version:string;components:LearningComponent[];steps:ComponentStep[];budget:number;used_minutes:number;relevance_status?:string;plan?:ComponentPlanInfo}
export interface ComponentAction {component_id:string;version:string;action:string;operation_id:string;session_id:string;check_id?:string;answer?:string;helped?:boolean;rating?:number}
export interface ComponentActionResult {recorded:boolean;duplicate:boolean;read_after_seconds:number;correct?:boolean;eligible:boolean;explanation?:string;state?:LearningComponent['state']}
export async function getComponents(kb:string,minutes=15,goal='',topic=''):Promise<ComponentView>{const r:any=await get(`/api/v1/learning/kb/${kb}/components?minutes=${minutes}&goal=${encodeURIComponent(goal)}${topic?`&topic=${encodeURIComponent(topic)}`:''}`);return r.data??r}
export async function componentAction(kb:string,action:ComponentAction):Promise<ComponentActionResult>{const r:any=await post(`/api/v1/learning/kb/${kb}/components/action`,action);return r.data??r}
