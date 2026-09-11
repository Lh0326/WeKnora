import type {LearningReviewInput,LearningReviewStatus,RecallAction} from '@/api/learning/objectives'

export function recallDue(status?:LearningReviewStatus,now=Date.now()):boolean{
 return !!status?.active&&(status.content_changed||status.due||Date.parse(status.due_at)<=now)
}
export function recallSummary(status?:LearningReviewStatus):string{
 if(!status)return '想长期记住这个知识点，可以加入间隔复习。'
 if(!status.active)return '已暂停复习推荐；历史反馈保留，可随时继续。'
 if(status.content_changed)return '材料已更新；核对新内容后，本次反馈会重新安排间隔。'
 if(recallDue(status))return '本次复习已到期，先尝试回忆，再核对材料。'
 return `下次复习：${new Date(status.due_at).toLocaleString('zh-CN',{month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit'})} · ${status.interval_days?`间隔 ${status.interval_days} 天`:'短时重学'}`
}

/** One mounted account/KB/node owns a submission. A lost response retries the
 * exact request, while a scope change discards any late response. */
export function createRecallFeedback(save:(kb:string,input:LearningReviewInput)=>Promise<LearningReviewStatus>,id:()=>string=()=>crypto.randomUUID()){
 let kb='',slug='',generation=0,pending:LearningReviewInput|null=null,busy=false
 function setTarget(nextKB:string,nextSlug:string){generation++;kb=nextKB;slug=nextSlug;pending=null;busy=false}
 async function send(action:RecallAction,status?:LearningReviewStatus){
  if(busy||!kb||!slug)return null
  if(pending&&pending.action!==action)throw Error('unresolved_recall_feedback')
  pending??={slug,action,revision:status?.revision||'',content_version:status?.content_version||'',request_id:id()}
  const gen=generation,scope=kb,input={...pending};busy=true
  try{const result=await save(scope,input);if(gen!==generation)return null;pending=null;return result}
  finally{if(gen===generation)busy=false}
 }
 return {setTarget,send,discard:()=>{generation++;pending=null;busy=false}}
}
