import {shallowRef} from 'vue'
import type {ObjectiveViewResponse,LearningPathPlan} from '@/api/learning/objectives'
import {summarizeNodes} from './learning/learningOverview'
import {summarizeEstimates,estimateLabels,estimateExplanation} from './learning/learningEstimates'
import {nodeStateLabels} from './learning/learningEvents'
import {recallDue,recallSummary} from './learning/recallFeedback'
import {pathActionLabel,pathReasonLabel} from './learning/pathLabels'
import type {LearningSessionSnapshot} from './learning/learningSession'

export interface AssistantLearningSnapshot {view:ObjectiveViewResponse|null;plan:LearningPathPlan|null;trajectory:string}
export function snapshotFields(snapshot:AssistantLearningSnapshot|null,slug:string|null,session?:LearningSessionSnapshot|null):Record<string,string>|null {
 if(!snapshot&&!session)return null
 const fields:Record<string,string>={}
 const view=session?session.view:snapshot?.view
 const plan=session?session.plan:snapshot?.plan
 fields.learning_ui='有效阅读自动更新阅读进度与下一步，不提高能力估计，无需逐页确认。可选纠偏按钮为「纠正：已经熟悉」「纠正：仍有困难」，另有「标记已读」。只有存在已审核验证目标及可用题目时才显示验证入口，请勿虚构按钮。没有题库只影响客观验证，用户仍可反馈「已经熟悉」；不得把无需确认说成无法纠偏。模型估计、自评和验证事实分别说明；以当前快照为准，不以历史轨迹覆盖当前状态。'
 if(view?.nodes){
  fields.recall_schedule=`主动加入间隔复习 ${view.nodes.filter(n=>n.review?.active).length} 个；到期 ${view.nodes.filter(n=>recallDue(n.review)).length} 个。界面提供「加入间隔复习」「开始回忆」和四档自评反馈，FSRS-6 自动安排个人复习，不产生客观验证证据；旧 SM-2 安排在下一次反馈时切换。`
  if(slug)fields.current_recall=recallSummary(view.nodes.find(n=>n.slug===slug)?.review)
  const s=summarizeNodes(view.nodes)
  fields.kb_progress=`知识点 ${s.total} 个；已接触 ${s.covered}；未开始 ${s.counts.unseen}；学习中 ${s.counts.learning}；自认已会 ${s.counts.self_known}；验证通过 ${s.counts.verified}；待巩固 ${s.counts.review}。自认和验证分别统计，不能换算为掌握概率。`
  if(slug){const n=view.nodes.find(n=>n.slug===slug);fields.current_node=n?`${nodeStateLabels[n.state]}；阅读记录 ${n.reads} 条；引用 ${n.cites} 条；验证目标 ${n.objective_verified}/${n.objective_total}${n.updated?'；内容已变化，需要重新确认':''}`:'当前知识点没有学习记录'}
  if(view.nodes.some(n=>n.estimate)){
   const model=summarizeEstimates(view.nodes)
   fields.kb_progress=`知识点 ${model.total} 个；客观验证通过 ${model.verified} 个。当前学习阶段与待巩固数量以 model_progress 为准；历史声明不能覆盖当前阅读进度与检查事实。`
   fields.model_progress=`算法画像：${model.covered}/${model.total} 已接触；检查支持 ${model.counts.familiar}；自认熟悉 ${model.counts.self_reported}；建议巩固 ${model.counts.review}。${model.uncertain} 个节点缺少独立作答依据。阅读和自评不改变表现指数；没有独立检查时不显示能力数值。仅已作答目标的指数未完成本地概率校准，不代表整页掌握，不能把参数敏感性范围称为置信区间；FSRS 只预测已有回忆记录的材料保持率。推荐使用已提供的同一份路径，不要求用户逐页确认。`
   if(slug){const n=view.nodes.find(n=>n.slug===slug);if(n?.estimate)fields.current_node=`${estimateLabels[n.estimate.level]}；${estimateExplanation(n)}；已审核目标 ${n.objective_verified}/${n.objective_total} 通过。`}
  }
 }else fields.kb_progress='学习状态暂不可用，不能推断用户尚未学习。'
 if(session){
  const req=session.request
  const depth=({aware:'了解概念',operate:'完成操作',analyze:'分析应用'} as Record<string,string>)[req.depth||'aware']||'了解概念'
  fields.learning_scope=`与学习导航共用的本轮计划：${session.label}；预算 ${req.time_budget_minutes??15} 分钟；深度 ${depth}；已临时跳过 ${req.excluded_slugs?.length||0} 个节点；${req.use_memory===false?'未启用':'已启用'}长期记忆参考。请依据这份计划回答下一步；不要自行替换为全库默认计划。`
  fields.learning_plan_status=session.status==='ready'?`计划已更新；规则 ${plan?.policy_version||''}`:session.status==='loading'?'正在重新规划，下一步尚未就绪；当前统计为上次记录，不能宣称已同步。':'重新规划失败，当前统计为上次记录；请建议在学习导航中重试，不要虚构计划。'
 }
 fields.next_recommended=plan?plan.steps.filter(s=>!s.completed).map((s,i)=>`${i+1}. ${s.title||s.slug}：${pathActionLabel(s.action)}，约 ${s.minutes} 分钟；${s.reason?.detail||pathReasonLabel(s.reason?.code)}；完成条件：${s.done_when}`).join('；')||'本轮没有待执行步骤，可调整范围。':session?.status==='loading'?'当前选择的学习路径正在更新，请等待。':'推荐暂不可用。'
 fields.recent_trajectory=snapshot?.trajectory||'暂无可用的近期活动。'
 return fields
}

// Each widget owns its cache. Scope changes discard data synchronously; late
// replies cannot put another user's/KB's profile back into the current view.
export interface AssistantSnapshotOptions { trajectoryOnly?: boolean }
export function createAssistantLearningContext(fetcher=fetchSnapshot){
 const snapshot=shallowRef<AssistantLearningSnapshot|null>(null)
 let scope='',generation=0,at=0,flight:Promise<void>|null=null,mode=false
 function reset(nextScope:string){scope=nextScope;generation++;at=0;flight=null;snapshot.value=null}
 async function ensure(kbId:string,nextScope:string,force=false,options:AssistantSnapshotOptions={}):Promise<void>{
  if(nextScope!==scope||mode!==!!options.trajectoryOnly){reset(nextScope);mode=!!options.trajectoryOnly}
  if(!kbId)return
  if(flight&&!force)return flight
  if(!force&&snapshot.value&&Date.now()-at<120000)return
  const gen=++generation
  flight=fetcher(kbId,options).then(result=>{if(gen===generation){snapshot.value=result;at=Date.now()}}).catch(()=>{if(gen===generation)snapshot.value=null}).finally(()=>{if(gen===generation)flight=null})
  return flight
 }
 return {snapshot,ensure,reset}
}
async function fetchSnapshot(kbId:string,options:AssistantSnapshotOptions={}):Promise<AssistantLearningSnapshot>{
 const {getObjectiveView,postShortPath}=await import('@/api/learning/objectives')
 const {getLearningTimeline}=await import('@/api/learning')
 const [view,plan,events]=await Promise.allSettled([options.trajectoryOnly?Promise.resolve(null):getObjectiveView(kbId),options.trajectoryOnly?Promise.resolve(null):postShortPath(kbId,{depth:'aware',time_budget_minutes:15}),getLearningTimeline(kbId,1,8)])
 const timeline:any=events.status==='fulfilled'?((events.value as any).data??events.value):[]
 const labels:Record<string,string>={source_read:'原文已读',node_known:'确认已会',node_review:'需要再学',node_read:'标记已读',wiki_tool_read:'阅读',wiki_deep_read:'深入阅读',quiz_correct:'答对',quiz_wrong:'答错',answer_cite:'问答引用'}
 return {view:view.status==='fulfilled'?view.value:null,plan:plan.status==='fulfilled'?plan.value:null,trajectory:Array.isArray(timeline)?'以下历史记录按时间从新到旧排列：'+timeline.slice(0,8).map(e=>`${e.occurred_at||''} ${labels[e.event_type]||'学习活动'}「${e.title||e.slug}」`).join('；'):''}
}
