import type {LearningPlanSettings,LearningNodeView} from '@/api/learning/objectives'

export function learningScopeSlugs(nodes:LearningNodeView[],folder:string|null):string[]{
 return folder===null?[]:nodes.filter(n=>(n.folder_id||'')===folder).map(n=>n.slug)
}

function copySettings(value:LearningPlanSettings):LearningPlanSettings{
 if(typeof value?.revision!=='string'||typeof value.folder_id!=='string'||typeof value.limit_to_folder!=='boolean'||typeof value.use_memory!=='boolean'||!['aware','operate','analyze'].includes(value.depth)||!Number.isInteger(value.time_budget_minutes)||value.time_budget_minutes<1||value.time_budget_minutes>120||!Array.isArray(value.goal_objectives)||!value.goal_objectives.every(g=>typeof g==='string'))throw Error('invalid_saved_scope')
 return {...value,goal_objectives:[...value.goal_objectives]}
}

/** Owned by the account/KB component. Only a successful current GET or CAS
 * updates the revision; a failed read can never overwrite unknown defaults. */
export function createLearningPreferences(api:{load:(kb:string)=>Promise<LearningPlanSettings>;save:(kb:string,input:LearningPlanSettings)=>Promise<LearningPlanSettings>}){
 let kb='',generation=0,revision:string|null=null
 function setScope(next:string){generation++;kb=next;revision=null}
 async function load(){const scope=kb,gen=++generation;revision=null;const value=copySettings(await api.load(scope));if(gen!==generation||kb!==scope)return null;revision=value.revision;return value}
 async function save(input:Omit<LearningPlanSettings,'revision'>){
  if(revision===null)throw Error('saved_scope_not_loaded')
  const scope=kb,gen=generation,value=copySettings({...input,revision})
  const result=copySettings(await api.save(scope,value))
  if(gen!==generation||kb!==scope)return null
  revision=result.revision;return result
 }
 return {setScope,load,save,clear:()=>setScope('')}
}

export function learningPreferenceError(error:unknown){
 if((error as {response?:{status?:number}})?.response?.status===409)return '其他页面已更新保存的范围，本轮仍可学习；请重新读取已保存范围后再调整。'
 return '学习范围未能同步保存，本轮仍可学习。请重新读取后再调整；尚未覆盖已保存的范围。'
}
