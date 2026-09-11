import type {ObjectiveViewEntry,ColdStartRequest,LearningNodeView,LearningPathPlan,LearningReviewInput,LearningReviewStatus} from '../../src/api/learning/objectives'
const read=new Set<string>(['concept/rag']),passed=new Set<string>(['task'])
const preferences=new Map<string,string>([['entity/weknora','known'],['concept/permissions','review']])
let failPath=false
export function failNextPath(){failPath=true}
export const fixtureNodes=[{slug:'concept/rag',title:'检索与生成',folder_id:'rag',folder_name:'检索增强生成'},{slug:'concept/config',title:'检索配置',folder_id:'rag',folder_name:'检索增强生成'},{slug:'concept/embedding',title:'向量表示',folder_id:'rag',folder_name:'检索增强生成'},{slug:'entity/weknora',title:'WeKnora',folder_id:'basics',folder_name:'知识库基础'},{slug:'concept/permissions',title:'知识库访问权限',folder_id:'basics',folder_name:'知识库基础'}]
export function readNode(slug:string){read.add(slug)}
export function verifyConcept(){passed.add('concept')}
const defs=[{id:'concept',slug:'concept/rag',title:'理解检索与生成的分工',behavior:'能区分检索证据与模型生成的责任',capability:'concept',contract:'concept_two_family'},{id:'task',slug:'concept/config',title:'选择检索配置',behavior:'根据场景约束选择检索方案',capability:'operation',contract:'task_checks'}]
function entries():ObjectiveViewEntry[]{return defs.map(o=>({objective_id:o.id,slug:o.slug,title:o.title,behavior:o.behavior,capability_type:o.capability,contract_type:o.contract,contract_version:'objective-contract-v1',content_version:'v1',objective_status:'published',state:passed.has(o.id)?'verified':'unverified',evidence:{families_passed:passed.has(o.id)?['fixture-a','fixture-b']:[],families_passed_historic:[],eligible_passes:passed.has(o.id)?2:0,eligible_failures:0,stale_passes:0,legacy_attempts:0,source:'quiz',unknown:!passed.has(o.id)},exposure:{reads:read.has(o.slug)?1:0,cites:0,agent_reads:0},self_report:{skipped:false},recency:{},path_status:'available'}))}
function nodes():LearningNodeView[]{return fixtureNodes.map(n=>{const o=defs.find(o=>o.slug===n.slug);const stored=JSON.parse(localStorage.getItem(recallKey)||'{}')[n.slug];const review=recallFor(n.slug);const pref=stored?.node_state||(stored?.last_rating?(stored.last_rating==='again'?'review':'known'):preferences.get(n.slug));const state=pref==='review'?'review':o&&passed.has(o.id)?'verified':pref==='known'?'self_known':read.has(n.slug)?'learning':'unseen';return {...n,state,review,reads:read.has(n.slug)?1:0,cites:0,updated:false,objective_total:o?1:0,objective_verified:o&&passed.has(o.id)?1:0}})}
export async function getObjectiveView(){return {projection_version:'fixture',entries:entries(),nodes:nodes()}}
export async function setNodeState(_kb:string,slug:string,state:'read'|'known'|'review'){if(!fixtureNodes.some(n=>n.slug===slug))throw Error('missing node');preferences.set(slug,state);if(state==='read')read.add(slug);const all=JSON.parse(localStorage.getItem(recallKey)||'{}');if(all[slug]){all[slug].node_state=state;localStorage.setItem(recallKey,JSON.stringify(all))};return {success:true}}
export async function getObjectiveProgress(_kb:string,goals:string[]=[]){const es=entries(),selected=goals.length?es.filter(e=>goals.includes(e.objective_id)):es;return{verified_objectives:passed.size,total_objectives:2,conflicting_objectives:0,stale_objectives:0,partial_objectives:0,unverified_objectives:2-passed.size,untested_objectives:2-passed.size,contact_nodes:read.size,total_nodes:5,self_report_count:0,legacy_attempt_count:0,goal_verified_objectives:selected.filter(e=>e.state==='verified').length,goal_total_objectives:selected.length}}
export async function postShortPath(_kb:string,req:ColdStartRequest):Promise<LearningPathPlan>{
 if(failPath){failPath=false;throw Error('injected fixture failure')}
 let spent=0;const steps:LearningPathPlan['steps']=[]
 const recall=nodes().find(n=>n.review?.due&&!req.excluded_slugs?.includes(n.slug)&&(!req.goal_slugs?.length||req.goal_slugs.includes(n.slug)))
 if(recall&&(req.time_budget_minutes||15)>=2){spent=2;steps.push({id:recall.slug+':recall',completed:false,slug:recall.slug,title:recall.title,action:'recall',minutes:2,done_when:'尝试回忆、核对材料并提交难度反馈',eligibility:'self_report',reason:{code:'plan_reason_scheduled_recall'}})}
 const candidates=nodes().filter(n=>!['verified','self_known'].includes(n.state)&&!req.excluded_slugs?.includes(n.slug)&&(!req.goal_slugs?.length||req.goal_slugs.includes(n.slug))).sort((a,b)=>Number(b.state==='review')-Number(a.state==='review'))
 for(const n of candidates){const objective=defs.find(o=>o.slug===n.slug);if(req.goal_objectives?.length&&(!objective||!req.goal_objectives.includes(objective.id)))continue
 if(steps.some(s=>s.slug===n.slug)||steps.length>=5)continue
 const action=n.state==='review'?'read':read.has(n.slug)?'confirm':'read',minutes=action==='confirm'?1:3
 if(spent+minutes>(req.time_budget_minutes||15))continue;spent+=minutes
 steps.push({id:n.slug+':'+action,completed:false,slug:n.slug,title:n.title,objective:objective?.id,action,minutes,done_when:'理解后确认学会，或标记需要再学',eligibility:'default',reason:{code:n.state==='review'?'plan_reason_user_review':action==='confirm'?'plan_reason_confirm_understanding':'plan_reason_docorder_fallback'}})
 }
 return{policy_version:'engineering-fixture',personalization:req.use_memory===false?'disabled':'no_signals',steps,degrade:steps.length?'':'plan_degrade_covered'}
}
export async function getTasks(){return[{id:'task-item',title:'检索配置（工程夹具）',scenario:'本场景同时需要关键字匹配和语义匹配。请选择检索方式。',objective_id:'task',fields:[{id:'mode',label:'检索方式',type:'select',options:['关键词检索','向量检索','混合检索']}],assistance_mode:'open_book',content_version:'v1',rubric_version:'v1'}]}
export async function submitTask(_kb:string,_id:string,answers:Record<string,string>,mode:string){const pass=answers.mode==='混合检索';if(pass&&mode!=='assistant_helped')passed.add('task');return{passed:pass,eligible:mode!=='assistant_helped',grade_reason:mode==='assistant_helped'?'assistance_not_strict':'eligible_independent',checks:[{id:'mode',passed:pass}]}}
// Persist only this labelled engineering fixture's selections across reloads.
const preferenceKey='weknora:engineering-fixture:plan-preferences:v1'
const defaultPreferences=()=>({revision:'',limit_to_folder:false,folder_id:'',depth:'aware',time_budget_minutes:15,use_memory:true,goal_objectives:[] as string[]})
export async function getPlanPreferences(){const raw=localStorage.getItem(preferenceKey);return raw?JSON.parse(raw):defaultPreferences()}
export async function savePlanPreferences(_kb:string,input:ReturnType<typeof defaultPreferences>){const current=await getPlanPreferences();if(current.revision!==input.revision)throw {response:{status:409}};const saved={...input,revision:crypto.randomUUID()};localStorage.setItem(preferenceKey,JSON.stringify(saved));return saved}

// UI fixture only. Production recurrence is exercised by Go/SQLite tests.
const recallKey='weknora:engineering-fixture:recall:v1'
function recallFor(slug:string):LearningReviewStatus|undefined{const s=JSON.parse(localStorage.getItem(recallKey)||'{}')[slug];return s?{...s,due:s.active&&(s.content_changed||Date.parse(s.due_at)<=Date.now())}:undefined}
let failRecall=false
export function failNextRecall(){failRecall=true}
export async function updateReviewSchedule(_kb:string,input:LearningReviewInput):Promise<LearningReviewStatus>{
 const all=JSON.parse(localStorage.getItem(recallKey)||'{}'),old=all[input.slug]
 if(old?.request_id===input.request_id)return recallFor(input.slug)!
 if((old?.revision||'')!==input.revision)throw {response:{status:409}}
 const active=input.action!=='pause',rating=['again','hard','good','easy'].includes(input.action),early=!!old&&input.action!=='again'&&Date.parse(old.due_at)>Date.now()
 const result:LearningReviewStatus={revision:crypto.randomUUID(),content_version:'fixture-v1',content_changed:false,policy_version:'engineering-fixture',active,due:active&&input.action==='enroll',due_at:input.action==='enroll'?new Date().toISOString():rating?(early?old.due_at:new Date(Date.now()+(input.action==='again'?600000:86400000)).toISOString()):old.due_at,interval_days:rating?(early?old.interval_days:input.action==='again'?0:1):(old?.interval_days||0),repetitions:rating?(early?old.repetitions:input.action==='again'?0:1):(old?.repetitions||0),ease:2.5,early_practice:rating&&early,last_rating:rating?input.action:old?.last_rating}
 all[input.slug]={...result,node_state:rating?(input.action==='again'?'review':'known'):old?.node_state,request_id:input.request_id};localStorage.setItem(recallKey,JSON.stringify(all))
 if(rating)preferences.set(input.slug,input.action==='again'?'review':'known')
 if(failRecall){failRecall=false;throw Error('fixture lost response after save')}
 return recallFor(input.slug)!
}
