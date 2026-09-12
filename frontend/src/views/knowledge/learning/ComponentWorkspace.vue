<template>
 <section v-if="view?.components.length" class="kc-workspace" aria-label="个人目标学习">
  <header><h3>学习导航</h3><button :disabled="loading" @click="emit('changed')">刷新</button></header>
  <p class="kc-muted">学完一步，再安排下一步 · 阅读自动记录 · 检查自愿参加</p>
  <small>全库学习记录</small>
  <div class="kc-count"><strong>{{ contacted }} / {{ view.components.length }}</strong><span>已接触目标</span><span>{{ view.components.filter(c=>c.state.passes>0).length }} 个有检查通过记录</span></div>
  <div class="kc-distribution" role="img" :aria-label="distributionText"><span v-for="part in distribution" :key="part.level" :style="{flex:part.count,background:componentColors[part.level]}" /></div>
  <div class="kc-legend"><span v-for="part in distribution" :key="part.level"><i :style="{background:componentColors[part.level]}" />{{ componentLabels[part.level] }} {{ part.count }}</span></div>
  <p v-if="unavailable" class="kc-muted">{{ unavailable }} 个目标来源待核对，已暂停推荐；其历史状态保留回查。</p>
  <p v-if="view.relevance_status==='available'" class="kc-muted">已参考你的记忆与常用资料，相关理由可在目标中查看。</p>
  <p v-else-if="view.relevance_status==='unavailable'" class="kc-muted">个人关联暂不可用，先根据目标和学习记录安排。</p>
  <label class="kc-scope">学习范围<select v-model="topic" @change="refresh"><option value="">全部模块</option><option v-for="module in modules" :key="module.topic" :value="module.topic">{{ module.topic }} · {{ module.count }} 个目标</option></select></label>
  <p class="kc-muted">{{ topic ? `当前聚焦「${topic}」；需要时补入其他模块的前置目标。` : '自动聚焦一个目标及其必要前置；也可以选择想学的模块。' }}</p>
  <p v-if="error" class="kc-error" role="alert">{{ error }}</p>
  <p v-if="notice" class="kc-muted" role="status">{{ notice }}</p>
  <p v-if="loading" class="kc-muted" role="status">正在更新学习路径…</p>
  <template v-if="!loading&&!error">
  <div v-if="view.plan?.focus_goal_title" class="kc-focus"><small>这条路径要达到的目标</small><strong>{{ view.plan.focus_goal_title }}</strong><p>{{ focusGoal?.material.goal }}</p></div>
  <div v-if="view.steps[0]" class="kc-next">
   <small>建议现在 · {{ componentActionLabels[view.steps[0].action] }} · 约 {{ view.steps[0].minutes }} 分钟</small>
   <h4>{{ view.steps[0].title }}</h4><p>{{ view.steps[0].reason }}</p>
   <button class="kc-primary" @click="open(view.steps[0].id,view.steps[0].action)">开始这一目标 →</button>
  </div>
  <p v-else class="kc-muted">{{ view.plan?.notice || '当前范围没有合适的下一步。可从星图选择目标，或调整范围；来源变更的材料需要先核对。' }}</p>
  <ol v-if="view.steps.length>1" class="kc-path"><li v-for="step in view.steps.slice(1,5)" :key="step.id"><button @click="open(step.id,step.action)">{{ step.title }}<small>{{ componentActionLabels[step.action] }}{{ step.conditional?' · 前置学习后再安排，可先预览':'' }}</small></button></li></ol>
  <p v-if="view.steps.length && view.plan?.notice" class="kc-muted">{{ view.plan.notice }}</p>
  <p v-if="view.plan?.progression" class="kc-muted">{{ view.plan.progression }}</p>
  </template>
  <details><summary>这些状态如何产生</summary><p>阅读自动点亮进度，不会把停留时间换成掌握概率。“自评熟悉”用于减少重复学习；“检查支持”表示至少两个不同题族有独立通过记录，最近一次检查也通过。两者分别展示，均不表示永久掌握。</p><p>旧页面的阅读保留为来源接触，不直接转成这些目标已会。先修线与相关线分别使用；同名概念在不同条件下可以对应不同目标。</p></details>
  <details v-if="view.plan&&!loading&&!error"><summary>为什么采用这个顺序</summary><p>{{ view.plan.focus_reason }}</p><p>先看你正在学的目标、困难与到期复习，再沿材料中明确记录的前置关系安排。属于同一模块或存在相关连线，都不会自动变成先修关系。</p><p>阅读记录只说明可以继续阅读；自评熟悉用于减少重复，检查记录单独保留。每次学习后重新计算，不把整条路径假定为已完成。</p><p>如果材料只有概念解释、没有操作或应用目标，排序本身无法补出应用能力；此时只作为阅读建议。</p><p v-for="limitation in view.plan.limitations||[]" :key="limitation">{{ limitation }}</p><details><summary>原型算法依据</summary><p>后台限制一次推荐的长度和可选检查占比，无需你设置学习时长。{{ view.plan.exact?'已比较当前目标及必要先修内的可行组合。':'已在计算上限内搜索当前目标及必要先修，不保证组合最优。' }} 比较的是声明的优先级，不是已测得的学习收益，也不代表目标选择全局最优。</p></details></details>
  <p class="kc-muted">{{ view.components[0]?.material.provenance }}</p>
 </section>
 <p v-else-if="error" class="kc-error" role="alert">目标学习加载失败，以下仍是原页面视图。<button @click="refresh">重试</button></p>
 <t-drawer :visible="!!active" :header="active?.material.title || '学习目标'" size="min(760px,96vw)" :footer="false" @close="close">
  <template v-if="active">
   <div class="kc-reader-top"><button @click="close">← 返回知识星图</button><span>{{ active.material.topic }} · 约 {{ active.material.minutes }} 分钟</span></div>
   <p class="kc-condition">适用条件：{{ active.material.condition }}</p>
   <p v-if="active.state.legacy_component_touches" class="kc-muted">保留了 {{ active.state.legacy_component_touches }} 条该目标旧版本的接触记录；当前版本的阅读和检查分别记录。</p>
   <details v-if="active.relevance?.links.length" class="kc-relevance"><summary>与你的需求有什么关系</summary><p v-for="(link,i) in active.relevance.links" :key="i">{{ link.reason }}</p><p v-if="active.relevance.sources>active.relevance.links.length" class="kc-muted">另有 {{ active.relevance.sources-active.relevance.links.length }} 处候选来源；重复路径已去重。</p></details>
   <h3>{{ active.material.goal }}</h3>
   <div class="kc-learning-links" aria-label="这个目标在学习路径中的位置">
    <template v-if="learningLinks.before.length"><small>先用到这些内容</small><button v-for="c in learningLinks.before" :key="c.id" @click="open(c.id)">{{ c.material.title }}</button></template>
    <p v-if="learningLinks.missing" class="kc-muted">有 {{ learningLinks.missing }} 个前置目标暂不可用，这段学习关系需要补齐。</p>
    <template v-if="learningLinks.after.length"><small>后续哪些目标会用到它（可预览）</small><button v-for="c in learningLinks.after" :key="c.id" @click="open(c.id)">{{ c.material.title }}<span>{{ c.material.goal }}</span></button></template>
    <p v-if="!learningLinks.before.length&&!learningLinks.after.length&&!learningLinks.missing" class="kc-muted">尚未记录该目标的前后依赖，先独立阅读；同模块的其他目标不代表更高阶能力。</p>
   </div>
   <div class="kc-status"><i :style="{background:componentColors[active.state.level]}" /><strong>{{ componentLabels[active.state.level] }}</strong><span>{{ active.state.reads }} 次阅读 · {{ active.state.passes }}/{{ active.state.checks }} 次独立检查通过</span><p>{{ active.state.basis }}</p></div>
   <details v-if="active.available"><summary>查看证据依据</summary><p v-if="active.state.checks===0">尚无独立检查，暂不显示能力数值。阅读进度和你的熟悉反馈已保留，你可以直接继续学习。</p><p v-else>已有 {{ active.state.checks }} 个独立题族的原型检查，{{ active.state.passes }} 次通过；少量题目不能代表全部应用能力。</p><p>{{ active.state.practice }} 次练习单独保留。阅读与自评不改变表现估计，检查只影响本目标；回忆反馈另行安排复习。</p><details v-if="active.state.performance_observed"><summary>实验性模型详情</summary><p>仅根据独立检查计算的指数为 {{ Math.round(active.state.familiarity*100) }}%；不同初始假设下为 {{ Math.round(active.state.lower*100) }}%–{{ Math.round(active.state.upper*100) }}%。参数尚未本地校准；该范围不是统计置信区间，也不能解释为真实掌握概率。</p></details></details>
   <p v-if="!active.available" class="kc-error">{{ active.source_problem }} 请先从下方来源回查。</p>
   <template v-else>
    <template v-if="mode==='read'">
     <h4>关键解释</h4><p class="kc-prose">{{ active.material.explanation }}</p>
     <h4>放到一个完整例子中</h4><p class="kc-example kc-prose">{{ active.material.example }}</p>
     <p class="kc-muted" role="status">{{ readMessage }}</p>
     <button v-if="readingPaused" :disabled="sending" @click="open(active.id)">继续阅读，重新记录进度</button>
     <div class="kc-actions"><button :disabled="sending" @click="act('known')">已经熟悉，减少重复学习</button><button :disabled="sending" @click="act('difficult')">仍有困难，再看看例子</button></div>
     <div class="kc-actions"><button v-if="active.material.checks.length" @click="startCheck">换个情境检查一下</button><button @click="mode='recall';recallRevealed=false">先尝试回忆</button></div>
     <p v-if="!active.material.checks.length" class="kc-muted">这个目标暂未配置检查题，可直接阅读与回忆；不会因此推断你已经掌握。</p>
    </template>
    <template v-else-if="mode==='check' && check">
     <p class="kc-muted">材料已收起。本题只检查这个目标；重复题族仅作练习。</p>
     <h4>{{ check.question }}</h4>
     <label v-for="(text,key) in check.options" :key="key" class="kc-option"><input v-model="answer" type="radio" :value="key" :disabled="!!verdict" />{{ key }}. {{ text }}</label>
     <label class="kc-option"><input v-model="answer" type="radio" value="?" :disabled="!!verdict" />暂时不确定</label>
     <label class="kc-muted"><input v-model="helped" type="checkbox" :disabled="!!verdict" />本次参考了材料或助手（仅作练习）</label>
     <button class="kc-primary" :disabled="!answer||sending||!!verdict" @click="act('check',{check_id:check.id,answer,helped})">查看检查结果</button>
     <div v-if="verdict" class="kc-example" role="status"><strong>{{ verdict.correct?'本题通过':'继续理解这个目标' }}</strong><p>{{ verdict.explanation }}</p><p>{{ verdict.eligible?'记录为该组件的一次独立原型检查。':'本次仅作练习，不增加独立证据。' }}</p></div>
     <button @click="mode='read'">回看解释和例子</button>
    </template>
    <template v-else-if="mode==='recall'">
     <p>请先用自己的话回忆：{{ active.material.goal }}</p>
     <button v-if="!recallRevealed" @click="recallRevealed=true">完成回忆，查看要点</button>
     <template v-else><p class="kc-example kc-prose">{{ active.material.explanation }}</p><p class="kc-muted">按刚才的回忆情况反馈，FSRS 会安排复习；自评不增加客观验证证据。</p><div class="kc-actions"><button v-for="(label,i) in ['未想起','很吃力','基本想起','轻松想起']" :key="label" :disabled="sending" @click="act('recall',{rating:i+1})">{{ label }}</button></div></template>
     <button @click="mode='read'">回到学习材料</button>
    </template>
   </template>
   <p v-if="actionError" class="kc-error" role="alert">{{ actionError }}<button v-if="failedRequest" @click="retryAction">重试保存</button></p>
   <details class="kc-sources"><summary>来源与上下文（{{ active.material.sources.length }} 处）</summary><div v-for="src in active.material.sources" :key="src.slug+src.quote"><p>{{ src.role }}</p><blockquote>{{ src.quote }}</blockquote><button @click="emit('source',src.slug)">查看完整 Wiki 与原始出处 ↗</button></div><p class="kc-muted">{{ active.state.legacy_touches }} 条旧来源接触记录未计入本目标的掌握估计。</p></details>
   <div class="kc-reader-foot"><button v-if="next" class="kc-primary" @click="open(next.id,next.action)">继续：{{ next.title }} →</button><button v-else @click="close">返回星图选择目标</button></div>
  </template>
 </t-drawer>
</template>
<script setup lang="ts">
import {computed,ref,watch,onMounted,onUnmounted,onActivated,onDeactivated} from 'vue'
import {getComponents,componentAction,type ComponentView,type ComponentAction,type ComponentActionResult} from '@/api/learning/components'
import {componentColors,componentLabels,componentActionLabels} from './componentPresentation'
import {useAssistantHostPublisher} from '../assistantHost'
import {useAuthStore} from '@/stores/auth'
import {completeComponentAction,createComponentReadingClock} from './componentLifecycle'
import {componentModules,componentLearningLinks} from './componentLearningGuide'
const props=defineProps<{kbId:string}>()
const emit=defineEmits<{(e:'view',v:ComponentView|null):void;(e:'source',slug:string):void;(e:'changed'):void}>()
const publish=useAssistantHostPublisher(['learning'])
const auth=useAuthStore()
const scopeKey=computed(()=>JSON.stringify([props.kbId,auth.user?.id,auth.currentTenantId]))
const view=ref<ComponentView|null>(null),loading=ref(false),error=ref(''),notice=ref(''),topic=ref('')
const activeId=ref(''),mode=ref('read'),sending=ref(false),actionError=ref(''),failedRequest=ref<ComponentAction|null>(null)
const active=computed(()=>view.value?.components.find(c=>c.id===activeId.value))
const focusGoal=computed(()=>view.value?.components.find(c=>c.id===view.value?.plan?.focus_goal_id))
const modules=computed(()=>componentModules(view.value?.components||[]))
const learningLinks=computed(()=>componentLearningLinks(view.value?.components||[],activeId.value))
const next=computed(()=>view.value?.steps.find(s=>s.id!==activeId.value))
const contacted=computed(()=>view.value?.components.filter(c=>c.state.level!=='unseen').length||0)
const unavailable=computed(()=>view.value?.components.filter(c=>!c.available).length||0)
const distribution=computed(()=>Object.keys(componentLabels).map(level=>({level,count:view.value?.components.filter(c=>c.state.level===level).length||0})))
const distributionText=computed(()=>distribution.value.map(p=>`${componentLabels[p.level]} ${p.count}`).join('，'))
const checkId=ref(''),answer=ref(''),helped=ref(false),verdict=ref<ComponentActionResult|null>(null),recallRevealed=ref(false)
const check=computed(()=>active.value?.material.checks.find(q=>q.id===checkId.value))
const readMessage=ref(''),sessionId=ref(''),readingPaused=ref(false)
const readingClock=createComponentReadingClock()
let generation=0,scopeGeneration=0,readerGeneration=0,timer:ReturnType<typeof setInterval>|undefined,activated=true,disposed=false
function stopTimer(){clearInterval(timer);timer=undefined;readingClock.pause()}
function publishContext(){
 const data=view.value;if(!data?.components.length)return
 publish({
  scene:'component-learning',
  learning_ui:'当前按独立知识组件学习，星图与学习导航是同一模型。阅读只更新接触进度，不增加检查证据，也不提高表现估计；自评熟悉用于减少重复学习，不提高表现估计；检查支持指至少两个不同题族独立通过且最近检查通过。检查和回忆自愿参加，不要引用旧 Wiki 页画像替代组件状态，不要宣称点亮等同能力认证或真实掌握概率。讲解检索文档时必须区分文档描述的方案和当前组件实现：文档中的 logit 固定权重、三次证据阈值或四档参数不代表当前组件规则，禁止用答案引用权重代替页面阅读权重来演算当前用户能力。用户问文档公式时可注明“资料中的方案”后解释；问当前页面时以这里的组件状态为准，未提供的实现参数应说明无法确认。',
  learning_scope:`${topic.value?`模块「${topic.value}」及必要前置`:'全部模块，自动聚焦目标链'}。界面不要求用户设置学习时长；每完成一步重新安排。样本：${data.components[0]?.material.provenance}`,
  learning_plan_status:error.value?'更新失败，下面是上次计划，不要宣称已同步。':loading.value?'正在更新计划。':'计划已更新，与屏幕导航一致。',
  kb_progress:`目标 ${data.components.length} 个；${distributionText.value}；来源待核对 ${unavailable.value} 个。`,
  next_recommended:data.steps.map((s,i)=>`${i+1}. ${s.title}：${componentActionLabels[s.action]}，约 ${s.minutes} 分钟；${s.reason}`).join('；')||'本轮没有合适的下一步',
  current_component:active.value?`${active.value.material.title}；${active.value.material.goal}；适用：${active.value.material.condition}；${active.value.state.basis}`:'当前未打开组件',
  current_page_excerpt:active.value?active.value.material.explanation+'\n'+active.value.material.example:'',
  component_model:data.model_version,
  learning_path_method:data.plan?`路径目标：${data.plan.focus_goal_title||'暂无'}；原因：${data.plan.focus_reason||''}；${data.plan.progression||''}。${(data.plan.limitations||[]).join(' ')} 策略 ${data.plan.policy_version}。比较的是原型优先级，不是已证实的学习收益。前置步骤实际完成后需重算。${data.plan.notice||''}`:'',
  personal_relevance:data.relevance_status==='available'?'本轮参考个人记忆主题与常用资料的候选联系；关联只改推荐顺序，不构成学习或掌握证据。':data.relevance_status==='unavailable'?'个人关联暂不可用，本轮未据此排序。':data.relevance_status==='disabled'?'个人学习采集已关闭，本轮不使用个人关联。':'尚无可用个人关联。',
 })
}
async function refresh(){const gen=++generation,kb=props.kbId,scope=scopeGeneration;loading.value=true;error.value='';publishContext();try{const data=await getComponents(kb,15,'',topic.value);if(disposed||scope!==scopeGeneration||gen!==generation)return;view.value=data;emit('view',data)}catch{if(!disposed&&scope===scopeGeneration&&gen===generation){error.value='目标与学习路径更新失败，请重试。';if(!view.value)emit('view',null)}}finally{if(!disposed&&scope===scopeGeneration&&gen===generation){loading.value=false;publishContext()}}}
function startCheck(){const c=active.value;if(!c)return;checkId.value=(c.material.checks.find(q=>!c.state.seen_check_ids.includes(q.id))||c.material.checks[0])?.id||'';answer.value='';helped.value=false;verdict.value=null;mode.value='check'}
async function open(id:string,action='read'){
 stopTimer();const c=view.value?.components.find(c=>c.id===id);if(!c)return
 const gen=++readerGeneration;activeId.value=id;sessionId.value=crypto.randomUUID();mode.value='read';actionError.value='';failedRequest.value=null;verdict.value=null;readingPaused.value=false;sending.value=false;readMessage.value='正在准备自动阅读记录…';recallRevealed.value=false
 if(action==='check')startCheck();if(action==='recall')mode.value='recall';publishContext()
 if(!c.available){readMessage.value='来源需要核对';return}
 const result=await act('open');if(gen!==readerGeneration||!result?.recorded)return
 startReadingTimer(result,gen)
}
function startReadingTimer(result:ComponentActionResult,gen:number){
 stopTimer();readingClock.start(result.read_after_seconds,Date.now());readMessage.value='阅读会自动更新，无需逐页确认。'
 timer=setInterval(()=>{if(readingClock.tick(Date.now(),gen===readerGeneration&&document.visibilityState==='visible'&&activated&&mode.value==='read'&&!sending.value&&!readingPaused.value)){act('read').then(r=>{if(gen===readerGeneration&&r?.recorded){readMessage.value='✓ 阅读进度已保存。';stopTimer()}})}},1000)
}
async function send(request:ComponentAction){
 const kb=props.kbId,gen=readerGeneration,scope=scopeGeneration
 const scopeCurrent=()=>!disposed&&scope===scopeGeneration&&kb===props.kbId
 const readerCurrent=()=>scopeCurrent()&&gen===readerGeneration
 sending.value=true;actionError.value='';failedRequest.value=null
 try{return await completeComponentAction(()=>componentAction(kb,request),{
  scopeCurrent,readerCurrent,refresh,changed:()=>emit('changed'),
  applyToReader(result){
   if(!result.recorded){readMessage.value='当前已停采，阅读不会写入个人画像。';stopTimer();return}
   if(request.action==='check')verdict.value=result
   if(request.action==='recall'){mode.value='read';notice.value='回忆已保存，下次复习由 FSRS 安排。'}
   if(request.action==='read')readMessage.value='✓ 阅读进度已保存。'
  },
 })}catch{if(readerCurrent()){failedRequest.value=request;actionError.value='尚未确认保存成功；来源可能已变化或服务暂不可用。可重试同一次操作。'}}finally{if(readerCurrent())sending.value=false}
}
function act(action:string,extra:Partial<ComponentAction>={}){const c=active.value;if(!c||sending.value)return Promise.resolve(undefined);if(action==='difficult'){stopTimer();readingPaused.value=true;readMessage.value='已暂停自动阅读记录。准备好后可继续阅读，重新积累本轮进度。'}return send({component_id:c.id,version:c.version,action,operation_id:crypto.randomUUID(),session_id:sessionId.value,...extra})}
async function retryAction(){const request=failedRequest.value,gen=readerGeneration;if(request&&!sending.value){const result=await send(request);if(gen===readerGeneration&&request.action==='open'&&result?.recorded)startReadingTimer(result,gen)}}
function close(){++readerGeneration;stopTimer();activeId.value='';sending.value=false;failedRequest.value=null;publishContext()}
watch(scopeKey,()=>{++scopeGeneration;++generation;close();view.value=null;emit('view',null);topic.value='';refresh()},{immediate:true,flush:'sync'})
onMounted(()=>{activated=true});onActivated(()=>{const resume=!activated;activated=true;if(resume)refresh()});onDeactivated(()=>{activated=false;close()});onUnmounted(()=>{disposed=true;++scopeGeneration;++generation;++readerGeneration;stopTimer()})
defineExpose({open,refresh,close})
</script>
<style scoped>
.kc-workspace{display:flex;flex-direction:column;gap:12px;padding:10px}.kc-workspace header,.kc-reader-top{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}h3,h4,p{margin:0}h3{font-size:17px}h4{font-size:15px;margin:14px 0 8px}p{font-size:13px;line-height:1.8}button,select{font:inherit;font-size:12px;color:var(--td-text-color-primary);border:1px solid var(--td-component-border,#ddd);background:var(--td-bg-color-container,#fff);padding:7px 10px;border-radius:6px;cursor:pointer}button:disabled{opacity:.5;cursor:default}button:hover{border-color:var(--td-brand-color,#07c05f)}.kc-muted,small{font-size:12px;color:var(--td-text-color-secondary,#666);line-height:1.7}.kc-count{display:flex;gap:8px;flex-wrap:wrap;align-items:baseline}.kc-count strong{font-size:24px}.kc-count span{font-size:12px}.kc-distribution{display:flex;height:9px;border-radius:6px;overflow:hidden}.kc-legend{display:flex;flex-wrap:wrap;gap:8px;font-size:11px}.kc-legend i,.kc-status i{display:inline-block;width:8px;height:8px;border-radius:50%;margin-right:5px}.kc-budget,.kc-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin:8px 0;font-size:12px}.chosen{color:var(--td-brand-color);border-color:var(--td-brand-color)}.kc-next{border:1px solid #c7e0d6;border-radius:10px;background:#eff9f53d;padding:15px}.kc-primary{background:var(--td-brand-color,#07c05f);color:white;border-color:var(--td-brand-color,#07c05f);margin-top:12px}.kc-next .kc-primary{width:100%;text-align:left}.kc-path{padding-left:22px;margin:0}.kc-path li{padding:5px}.kc-path button{width:100%;text-align:left;border:0}.kc-path small{display:block}.kc-scope{font-size:12px;display:flex;gap:8px;align-items:center}.kc-scope select{min-width:0;flex:1}details{font-size:12px;line-height:1.8}summary{cursor:pointer}.kc-error{color:var(--td-error-color,#b33333);font-size:12px;line-height:1.8}.kc-reader-top{font-size:12px;color:var(--td-text-color-secondary);margin-bottom:16px}.kc-condition{color:var(--td-text-color-secondary);margin:8px 0}.kc-status{padding:12px;margin:18px 0;background:var(--td-bg-color-secondarycontainer,#f4f6f7);border-radius:8px;font-size:12px}.kc-status span{margin-left:8px}.kc-prose{white-space:pre-wrap;font-size:14px;line-height:1.95;overflow-wrap:anywhere}.kc-example{padding:16px;background:#eff9f53d;border-left:3px solid #59aa9a;margin:10px 0 20px;border-radius:6px}.kc-option{display:flex;gap:10px;align-items:flex-start;padding:12px;border:1px solid var(--td-component-border);border-radius:6px;margin:8px 0;font-size:14px}.kc-option input{margin-top:4px}.kc-sources{margin-top:24px;border-top:1px solid var(--td-component-border);padding-top:14px}.kc-sources blockquote{margin:8px 0;padding:8px 12px;border-left:2px solid #ccd5d3;white-space:pre-wrap}.kc-reader-foot{margin-top:20px;border-top:1px solid var(--td-component-border);padding-top:10px}
.kc-focus{display:flex;flex-direction:column;gap:5px;padding:12px;border:1px solid var(--td-component-border,#ddd);border-radius:8px}.kc-focus strong{font-size:14px}.kc-learning-links{display:flex;flex-direction:column;gap:6px;margin-top:16px;padding:12px;border-left:3px solid #59aa9a;background:var(--td-bg-color-secondarycontainer,#f4f6f7)}.kc-learning-links button{text-align:left}.kc-learning-links button span{display:block;font-size:12px;color:var(--td-text-color-secondary);margin-top:3px}
</style>
