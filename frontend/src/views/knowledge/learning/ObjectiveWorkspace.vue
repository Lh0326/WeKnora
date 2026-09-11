<template>
 <section class="objective-workspace" aria-label="目标与学习路径">
  <div class="workspace-heading"><h3>学习导航</h3><button class="text-button" :disabled="busy" @click="refresh()">{{ busy ? '更新中…' : '刷新' }}</button></div>
  <p v-if="error" class="task-error" role="alert">{{ error }}</p>
  <LearningOverview v-if="view?.nodes" :nodes="view.nodes" @open="emit('open',$event)" />
  <div class="session-settings"><span>{{ activeModuleName }} · 本轮</span><div><button v-for="minutes in [5,15,30]" :key="minutes" :class="{chosen:budget===minutes}" :disabled="busy" @click="budget=minutes;refresh(true)">{{ minutes }} 分钟</button><button class="text-button" :disabled="busy" @click="openSettings">设置</button></div></div>
  <p v-if="preferenceError" class="hint" role="alert">{{ preferenceError }} <button class="text-button" :disabled="busy" @click="restorePreferences">重新读取已保存范围</button></p>
  <p v-if="notice" class="update-notice" role="status">{{ notice }}</p>
  <p v-if="busy && !progress" class="hint">正在读取学习记录…</p>
  <div ref="pathAnchor"><PathPanel :plan="plan" :busy="busy || actionBusy" @open="emit('open',$event)" @challenge="challenge" @known="markKnown" @skip="skipOnce" /></div>
  <button v-if="excluded.length" class="text-button" :disabled="busy" @click="excluded=[];refresh()">恢复本轮跳过的 {{ excluded.length }} 个知识点</button>
  <section v-if="modules.length" class="module-section" aria-label="模块学习覆盖">
   <div class="workspace-heading"><h4>模块与盲区</h4><button v-if="moduleId!==null" class="text-button" :disabled="busy" @click="chooseModule(null)">回到全库</button></div>
   <p class="hint">按待巩固和未掌握数量排序，点击模块集中学习。</p>
   <button v-for="group in modules.slice(0,showAllModules?modules.length:5)" :key="group.id" class="module-row" :class="{selected:moduleId===group.id}" :disabled="busy" @click="chooseModule(group.id)" @mouseenter="emit('highlight',group.id)" @mouseleave="emit('highlight',null)">
    <span class="module-heading"><strong>{{ group.name }}</strong><small>{{ group.known }}/{{ group.total }} 已会（含自认）</small></span>
    <span class="module-bar" role="img" :aria-label="`${group.name}：未开始 ${group.counts.unseen}，学习中 ${group.counts.learning}，自认已会 ${group.counts.self_known}，验证通过 ${group.counts.verified}，待巩固 ${group.counts.review}`"><i v-for="state in learningStatesInBar" :key="state" :style="{width:`${group.counts[state]/group.total*100}%`,background:nodeStateColors[state]}" /></span>
    <span class="module-caption"><span>{{ group.counts.review ? `${group.counts.review} 待巩固 · ` : '' }}{{ group.counts.unseen }} 未开始 · {{ group.counts.learning }} 学习中</span><span>集中学习 →</span></span>
   </button>
   <button v-if="modules.length>5" class="text-button" @click="showAllModules=!showAllModules">{{ showAllModules?'收起模块':`查看全部 ${modules.length} 个模块` }}</button>
  </section>
  <details v-if="progress && progress.total_objectives" class="verification-details"><summary>客观验证 · {{ progress.verified_objectives }}/{{ progress.total_objectives }} 个目标通过</summary><ObjectiveProgress :summary="progress" /></details>
  <div class="workspace-links">
   <button class="text-button" @click="evidenceSlug=''; evidenceOpen=true">查看验证依据</button>
   <button class="text-button" @click="openSettings">调整学习范围</button>
  </div>
  <t-drawer v-model:visible="settingsOpen" header="学习范围" size="480px" :footer="false" @close="cancelSettings">
   <button @click="cancelSettings">返回学习</button>
   <p class="hint">按你的掌握记录、先修关系和可用时间规划。所有知识点均可学习，没有题库也可直接确认已会。</p>
   <p class="hint">学习模块、目标、深度、时长与记忆偏好会随账号保存；已有基础选项与临时跳过仅影响本轮。</p>
   <p v-if="preferenceNotice" class="hint" role="status">{{ preferenceNotice }}</p>
   <div class="controls">
    <label>学习深度 <select v-model="depth"><option value="aware">了解概念</option><option value="operate">完成操作</option><option value="analyze">分析应用</option></select></label>
    <label>本次时长（分钟）<input v-model.number="budget" type="number" min="1" max="120" /></label>
    <label><input v-model="fastTrack" type="checkbox" />已有基础，直接尝试验证</label>
    <label><input v-model="useMemory" type="checkbox" />参考我的长期记忆和常用文档</label>
   </div>
   <p class="hint">{{ personalizationLabel(plan?.personalization) }} 这些关联只影响推荐顺序，不会将知识点标为已会。</p>
   <p class="hint">不勾选具体目标时，按学习深度自动选择。</p>
   <label v-for="o in published" :key="o.objective_id" class="goal"><input v-model="selected" type="checkbox" :value="o.objective_id" />{{ o.title }}</label>
   <button v-if="selected.length" @click="selected=[]">清空所选目标，按学习深度规划</button>
   <p v-if="!published.length" class="hint">本库暂无已审核验证目标，将按知识点内容与模块顺序规划阅读。</p>
   <p v-if="error" role="alert">{{ error }}</p>
   <button :disabled="busy" @click="applySettings">应用范围</button>
  </t-drawer>
  <t-drawer v-model:visible="evidenceOpen" header="目标证据" size="560px" :footer="false">
   <button @click="evidenceOpen=false">返回路径</button>
   <article v-for="o in evidence" :key="o.objective_id" class="evidence">
    <h4>{{ o.title }}</h4><p>{{ o.behavior }}</p>
    <p>状态：{{ stateLabel(o.state) }} · {{ o.objective_status==='published'?'已审核目标':'未发布目标' }}</p>
    <p>已通过题族 {{ o.evidence.families_passed.length }}；合格失败 {{ o.evidence.eligible_failures }}；旧记录 {{ o.evidence.legacy_attempts }}</p>
    <p>读 {{ o.exposure.reads }} 次 / 引用 {{ o.exposure.cites }} 次；自述 {{ o.self_report.direction || '无' }}；{{ o.self_report.skipped?'已选择跳过':'保留在路径中' }}</p>
    <p>最近合格通过：{{ displayTime(o.recency.last_verified_at) }}</p>
    <details><summary>查看核验依据</summary><p>目标 {{ o.objective_id }}</p><p>内容 {{ o.content_version }} · 规则 {{ o.contract_version }}</p><p>题族：{{ o.evidence.families_passed.join('、') || '暂无' }}</p></details>
   </article>
   <p v-if="!evidence.length">该节点暂无可验证目标。</p>
  </t-drawer>
  <t-drawer v-model:visible="taskOpen" :header="task?.title || '结构化任务'" size="560px" :footer="false">
   <button @click="taskOpen=false">返回路径</button>
   <p v-if="taskError" class="task-error" role="alert">{{ taskError }}</p>
   <div v-if="task" class="task-content"><p>{{ task.scenario }}</p>
    <p class="hint">按{{ task.assistance_mode==='open_book'?'开卷':'闭卷' }}条件完成。重复尝试可以练习，但不增加独立证据。</p>
    <label v-for="field in task.fields" :key="field.id" class="task-field">{{ field.label }}<select v-model="answers[field.id]" :disabled="!!verdict"><option disabled value="">请选择</option><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select></label>
    <label><input v-model="helped" type="checkbox" :disabled="!!verdict" />本次使用了助手或额外帮助</label>
    <p v-if="task?.mode==='practice'" class="hint">当前任务已尝试过，本次仅作练习。</p>
    <button class="task-submit" :disabled="taskBusy || !!verdict || !task.fields.every(f=>answers[f.id])" @click="gradeTask">{{ taskBusy ? '正在检查…' : '提交检查' }}</button>
    <div v-if="verdict" class="task-verdict" :class="{passed:verdict.passed && verdict.eligible}" role="status"><p><strong>{{ verdict.passed?'本次关键检查全部通过':'还有检查未通过，可以回看材料' }}</strong></p><p>{{ verdict.eligible?'本次结果已计入验证证据。':practiceReason(verdict.grade_reason) }}</p><p v-for="check in verdict.checks" :key="check.id">{{ check.passed?'✓':'○' }} {{ task.fields.find(f=>f.id===check.id)?.label || check.id }}：{{ check.passed?'通过':'未通过' }}</p><button @click="taskOpen=false">{{ verdict.passed ? '返回查看进度' : '返回学习路径' }}</button></div>
   </div>
   <p v-else-if="taskLoading" class="hint" role="status">正在加载任务…</p>
   <button v-else-if="taskError" @click="challenge(taskTarget.slug,taskTarget.objective)">重新加载任务</button>
   <p v-else>当前没有可用的已审核任务，请先阅读材料或更换目标。</p>
  </t-drawer>
 </section>
</template>
<script setup lang="ts">
import {computed,ref,watch,onMounted,onUnmounted,onActivated,onDeactivated,nextTick,inject} from 'vue'
import ObjectiveProgress from './ObjectiveProgress.vue'
import LearningOverview from './LearningOverview.vue'
import {groupLearningNodes} from './learningOverview'
import {LEARNING_UPDATED,notifyLearningUpdated,nodeStateColors} from './learningEvents'
import PathPanel from './PathPanel.vue'
import {learningSessionKey} from './learningSession'
import {createLearningPreferences,learningScopeSlugs,learningPreferenceError} from './learningPreferences'
import {pathPersonalizationLabel as personalizationLabel} from './pathLabels'
import {getPlanPreferences,savePlanPreferences,getObjectiveView,getObjectiveProgress,postShortPath,getTasks,submitTask,setNodeState,type ObjectiveViewResponse,type ObjectiveProgressSummary,type LearningPathPlan,type StructuredTask,type TaskVerdict} from '@/api/learning/objectives'
const props=defineProps<{kbId:string}>()
const learningSession=inject(learningSessionKey,null)
const emit=defineEmits<{(e:'highlight',id:string|null):void;(e:'open',slug:string):void;(e:'quiz',slug:string,objective:string):void;(e:'disagree',slug:string):void;(e:'plan',plan:LearningPathPlan|null):void;(e:'view',view:ObjectiveViewResponse):void}>()
const view=ref<ObjectiveViewResponse|null>(null),progress=ref<ObjectiveProgressSummary|null>(null),plan=ref<LearningPathPlan|null>(null)
const settingsOpen=ref(false)
const notice=ref('')
const depth=ref('aware'),budget=ref(15),fastTrack=ref(false),selected=ref<string[]>([]),busy=ref(false),error=ref('')
const useMemory=ref(true)
const preferences=createLearningPreferences({load:getPlanPreferences,save:savePlanPreferences})
const preferenceError=ref(''),preferenceNotice=ref('')
const published=computed(()=>view.value?.entries?.filter(o=>o.objective_status==='published')||[])
const learningStatesInBar=['verified','self_known','learning','review','unseen'] as const
const modules=computed(()=>groupLearningNodes(view.value?.nodes||[]))
const moduleId=ref<string|null>(null),showAllModules=ref(false),excluded=ref<string[]>([]),actionBusy=ref(false)
const pathAnchor=ref<HTMLElement|null>(null)
async function chooseModule(id:string|null){moduleId.value=id;selected.value=[];excluded.value=[];emit('highlight',null);await refresh(true);await nextTick();pathAnchor.value?.scrollIntoView({block:'start',behavior:'smooth'})}
const activeModuleName=computed(()=>moduleId.value===null?'全库学习':modules.value.find(g=>g.id===moduleId.value)?.name || '所选模块')
const evidenceOpen=ref(false),evidenceSlug=ref('')
const evidence=computed(()=>view.value?.entries?.filter(o=>!evidenceSlug.value||o.slug===evidenceSlug.value)||[])
const taskOpen=ref(false),task=ref<StructuredTask|null>(null),answers=ref<Record<string,string>>({}),helped=ref(false),verdict=ref<TaskVerdict|null>(null),taskBusy=ref(false)
const taskLoading=ref(false),taskError=ref(''),taskTarget=ref({slug:'',objective:''})
let generation=0,taskGeneration=0
function stateLabel(state:string){return ({verified:'已验证',partial:'部分证据',conflicting:'证据冲突',stale_content:'内容变化，待复核',unverified:'未验证'} as Record<string,string>)[state]||state}
function displayTime(at?:string){return !at||at.startsWith('0001')?'暂无':new Date(at).toLocaleString()}
function practiceReason(reason:string){return ({feedback_retry:'重复尝试仅作练习，不增加独立证据。',collection_disabled:'已停止采集，本次结果未保存。',assistance_not_strict:'本次使用帮助或条件不符，仅作练习。'} as Record<string,string>)[reason]||'本次仅作练习，不增加独立证据。'}
async function refresh(persist=false){
 const gen=++generation,kb=props.kbId;busy.value=true;error.value='';notice.value=''
 const selectedModule=moduleId.value;let scopeLabel=activeModuleName.value
 const applied=settingsOpen.value&&!persist?savedSettings:{depth:depth.value,budget:budget.value,fastTrack:fastTrack.value,useMemory:useMemory.value,selected:selected.value}
 const request={goal_slugs:learningScopeSlugs(view.value?.nodes||[],selectedModule),excluded_slugs:[...excluded.value],goal_objectives:[...applied.selected],depth:applied.depth,time_budget_minutes:Math.round(applied.budget),fast_track:applied.fastTrack,use_memory:applied.useMemory}
 let sessionUpdate=learningSession?.begin(kb,request,scopeLabel)
 try {
  if(!Number.isFinite(request.time_budget_minutes)||request.time_budget_minutes<1||request.time_budget_minutes>120)throw Error('预算应为 1–120 分钟')
  const v=await getObjectiveView(kb);if(gen!==generation)return
  if(!Array.isArray(v.nodes))throw Error('backend_version')
  view.value=v;emit('view',v)
  const goals=request.goal_objectives.length?request.goal_objectives:published.value.filter(o=>request.depth==='analyze'||(request.depth==='aware'?o.capability_type==='concept':o.capability_type!=='analysis')).map(o=>o.objective_id)
  const goalSlugs=learningScopeSlugs(v.nodes||[],selectedModule)
  scopeLabel=activeModuleName.value
  if(request.goal_objectives.some(id=>!published.value.some(o=>o.objective_id===id)))throw Error('goals_unavailable')
  if(selectedModule!==null&&!goalSlugs.length)throw Error('module_unavailable')
  request.goal_slugs=goalSlugs
  sessionUpdate=learningSession?.begin(kb,request,scopeLabel)
  const [p,path]=await Promise.all([getObjectiveProgress(kb,goals),postShortPath(kb,request)])
  if(gen!==generation)return
  if(progress.value && p.verified_objectives > progress.value.verified_objectives)notice.value=`新增 ${p.verified_objectives-progress.value.verified_objectives} 个已验证目标，进度已更新。`
  if(!goals.length){p.goal_total_objectives=0;p.goal_verified_objectives=0;p.goal_set_version=undefined};progress.value=p;plan.value=path;emit('plan',path)
  sessionUpdate?.complete(v,path)
  if(persist){try{const saved=await preferences.save({limit_to_folder:selectedModule!==null,folder_id:selectedModule||'',depth:request.depth,time_budget_minutes:request.time_budget_minutes,use_memory:request.use_memory,goal_objectives:request.goal_objectives});if(gen===generation&&saved){preferenceError.value='';preferenceNotice.value='学习范围已保存，下次打开此知识库时自动恢复。'}}catch(e){if(gen===generation)preferenceError.value=learningPreferenceError(e)}}
 }catch(e){if(gen===generation){sessionUpdate?.fail();plan.value=null;emit('plan',null);error.value=e instanceof Error&&e.message==='backend_version'?'后端尚未更新到支持知识点状态的版本，请重新构建并重启后端。':e instanceof Error&&e.message==='goals_unavailable'?'所选验证目标已变更，请在设置中清空或重新选择目标。':e instanceof Error&&e.message==='module_unavailable'?'所选模块已没有可学习内容，请回到全库重新选择。':'学习数据更新失败，请检查服务并重试。'+(view.value?' 当前仍显示上次读取的记录。':'')}}finally{if(gen===generation)busy.value=false}
}
async function markKnown(slug:string){if(actionBusy.value)return;const kb=props.kbId;actionBusy.value=true;try{await setNodeState(kb,slug,'known');if(kb!==props.kbId)return;await refresh();notice.value='已确认学会，知识点已点亮，接下来优先学习其他内容。';notifyLearningUpdated(kb,slug)}catch{error.value='保存失败，尚未标记已会，请重试。'}finally{actionBusy.value=false}}
function skipOnce(slug:string){excluded.value=[...excluded.value,slug];refresh()}
let active=true
// Wake once at the nearest future due time; do not poll the whole KB.
let recallTimer:ReturnType<typeof setTimeout>|undefined
function scheduleRecallRefresh(){
 clearTimeout(recallTimer)
 if(!active||busy.value||error.value)return
 const times=(view.value?.nodes||[]).filter(n=>n.review?.active&&!n.review.due&&!n.review.content_changed).map(n=>Date.parse(n.review!.due_at)).filter(Number.isFinite)
 if(!times.length)return
 const remaining=times.reduce((a,b)=>Math.min(a,b),Infinity)-Date.now()
 recallTimer=setTimeout(()=>{if(!active)return;if(document.visibilityState!=='visible')return;if(remaining>2147480000)scheduleRecallRefresh();else refresh()},Math.max(1000,Math.min(remaining+100,2147480000)))
}
function visibleAgain(){if(active&&document.visibilityState==='visible')scheduleRecallRefresh()}
watch([view,busy],()=>{clearTimeout(recallTimer);if(!busy.value)scheduleRecallRefresh()})
function onLearningUpdated(e:Event){if((active||learningSession?.snapshot.value?.kbId===props.kbId)&&(e as CustomEvent).detail?.kbId===props.kbId&&!actionBusy.value)refresh()}
onMounted(()=>{window.addEventListener(LEARNING_UPDATED,onLearningUpdated);document.addEventListener('visibilitychange',visibleAgain)})
onUnmounted(()=>{++generation;++taskGeneration;clearTimeout(recallTimer);preferences.clear();window.removeEventListener(LEARNING_UPDATED,onLearningUpdated);document.removeEventListener('visibilitychange',visibleAgain)})
onActivated(()=>{const resume=!active;active=true;if(resume)refresh()});onDeactivated(()=>{active=false;clearTimeout(recallTimer)})
let savedSettings = {depth:'aware',budget:15,fastTrack:false,useMemory:true,selected:[] as string[]}
function openSettings(){savedSettings={depth:depth.value,budget:budget.value,fastTrack:fastTrack.value,useMemory:useMemory.value,selected:[...selected.value]};settingsOpen.value=true}
function cancelSettings(){depth.value=savedSettings.depth;budget.value=savedSettings.budget;fastTrack.value=savedSettings.fastTrack;useMemory.value=savedSettings.useMemory;selected.value=[...savedSettings.selected];settingsOpen.value=false}
async function applySettings(){await refresh(true);if(!error.value)settingsOpen.value=false}
async function challenge(slug:string,objective:string){
 const o=published.value.find(o=>o.objective_id===objective);if(!o)return
 if(o.contract_type!=='task_checks'){emit('quiz',slug,objective);return}
 const gen=++taskGeneration;taskBusy.value=false;task.value=null;taskOpen.value=true;verdict.value=null;helped.value=false;answers.value={};taskLoading.value=true;taskError.value='';taskTarget.value={slug,objective}
 try{const tasks=await getTasks(props.kbId,objective);if(gen!==taskGeneration)return;task.value=tasks?.[0]||null;for(const f of task.value?.fields||[])answers.value[f.id]=''}catch{if(gen===taskGeneration)taskError.value='任务加载失败，请重新加载。'}finally{if(gen===taskGeneration)taskLoading.value=false}
}
async function gradeTask(){
 if(!task.value||taskBusy.value)return;const kb=props.kbId,gen=taskGeneration;taskBusy.value=true;taskError.value=''
 try{const r=await submitTask(kb,task.value.id,answers.value,helped.value?'assistant_helped':task.value.assistance_mode);if(gen!==taskGeneration)return;verdict.value=r;await refresh()}catch{if(gen===taskGeneration)taskError.value='未收到检查结果，答案已保留。可先返回查看进度，再决定是否重试。'}finally{if(gen===taskGeneration)taskBusy.value=false}
}
watch(()=>props.kbId,()=>{++taskGeneration;moduleId.value=null;excluded.value=[];taskBusy.value=false;taskLoading.value=false;taskError.value='';selected.value=[];depth.value='aware';budget.value=15;fastTrack.value=false;useMemory.value=true;settingsOpen.value=false;view.value=null;progress.value=null;plan.value=null;emit('view',{projection_version:'unavailable',entries:[]});emit('plan',null);taskOpen.value=false;evidenceOpen.value=false;preferences.setScope(props.kbId);preferenceError.value='';preferenceNotice.value='';restorePreferences()},{immediate:true})
async function restorePreferences(){
 const kb=props.kbId,gen=++generation;busy.value=true;preferenceError.value=''
 learningSession?.begin(kb,{},'正在恢复学习范围')
 try{const p=await preferences.load();if(gen!==generation||!p)return;moduleId.value=p.limit_to_folder?p.folder_id:null;depth.value=p.depth;budget.value=p.time_budget_minutes;useMemory.value=p.use_memory;selected.value=[...p.goal_objectives];excluded.value=[];fastTrack.value=false;preferenceNotice.value=p.revision?'已恢复上次保存的学习范围。':''}
 catch{if(gen===generation)preferenceError.value='暂未读取到已保存的学习范围，当前设置仅用于本轮。可重新读取后再调整。'}
 if(gen===generation)await refresh()
}
defineExpose({refresh,restorePreferences})
</script>
<style scoped>
.objective-workspace{display:flex;flex-direction:column;gap:14px;padding:12px;background:var(--td-bg-color-container);color:var(--td-text-color-primary)}
.session-settings{display:flex;flex-direction:column;gap:9px;font-size:12px;color:var(--td-text-color-secondary);padding:12px;border-radius:8px;background:var(--td-bg-color-component,#f5f7f8)}.session-settings>div{display:flex;gap:6px;align-items:center}.session-settings button{font-size:12px;padding:5px 9px}.session-settings .chosen{border-color:var(--td-brand-color);color:var(--td-brand-color);background:var(--td-bg-color-container)}.session-settings .text-button{margin-left:auto}.module-section{display:flex;flex-direction:column;gap:10px;padding-top:16px;border-top:1px solid var(--td-component-border)}.module-section h4{font-size:14px}.module-row{display:flex;flex-direction:column;gap:7px;text-align:left;padding:10px;border:1px solid transparent;background:var(--td-bg-color-component,#f5f7f8)}.module-row:hover,.module-row.selected{border-color:var(--td-brand-color)}.module-heading,.module-caption{display:flex;justify-content:space-between;align-items:center;gap:10px}.module-heading strong{font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.module-heading small{font-size:11px;white-space:nowrap;color:var(--td-text-color-secondary)}.module-caption{font-size:10px;color:var(--td-text-color-secondary)}.module-bar{display:flex;height:5px;overflow:hidden;border-radius:6px}.module-bar i{height:100%}.verification-details{font-size:12px;color:var(--td-text-color-secondary);border-top:1px solid var(--td-component-border);padding-top:12px}.verification-details summary{cursor:pointer;margin-bottom:12px}
h3,h4,p{margin:0}.hint{font-size:12px;line-height:1.6;color:var(--td-text-color-secondary)}.controls{display:flex;flex-direction:column;gap:16px;margin:20px 0;font-size:14px}.controls input[type=number]{width:64px}.goal,.task-field{display:block;margin:10px 0;line-height:1.6}.task-field select{display:block;width:100%;margin-top:6px;padding:8px}button,select,input{color:inherit;background:var(--td-bg-color-container);border:1px solid var(--td-component-border);border-radius:5px;padding:6px}button{cursor:pointer}button:disabled{opacity:.5;cursor:default}.evidence{display:flex;flex-direction:column;gap:8px;padding:14px 0;border-bottom:1px solid var(--td-component-border)}
.workspace-heading,.workspace-links{display:flex;align-items:center;justify-content:space-between;gap:12px}.workspace-heading h3{font-size:16px;font-weight:600}.workspace-links{border-top:1px solid var(--td-component-border);padding-top:14px}.text-button{border:0;background:transparent;padding:0;font-size:13px;color:var(--td-text-color-secondary)}
.update-notice{font-size:12px;line-height:1.6;color:var(--td-success-color,#038626)}.task-content{display:flex;flex-direction:column;gap:16px;margin-top:20px;line-height:1.7}.task-content .task-field{margin:0}.task-submit{padding:10px;background:var(--td-brand-color,#07c05f);color:white}.task-verdict{display:flex;flex-direction:column;gap:8px;padding:16px;border-radius:8px;background:var(--td-bg-color-component,#f3f4f5)}.task-verdict.passed{background:var(--td-success-color-light,#edf8f1)}.task-error{margin:16px 0;color:var(--td-error-color,#c43);font-size:13px;line-height:1.6}
@media(max-height:800px){.objective-workspace{gap:10px}.session-settings{padding:9px 10px;gap:6px}}
</style>
