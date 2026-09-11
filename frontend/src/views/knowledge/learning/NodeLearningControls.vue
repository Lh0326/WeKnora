<template>
 <section class="node-learning" aria-label="此知识点的学习状态">
  <div class="node-status"><span class="dot" :style="{background:node?.estimate?estimateColors[node.estimate.level]:nodeStateColors[node?.state || 'unseen']}"></span><strong>{{ loading ? '读取学习记录…' : node?.estimate ? estimateLabels[node.estimate.level] : node ? nodeStateLabels[node.state] : '学习状态暂不可用' }}</strong><span v-if="node?.updated">内容已变化，请重新核对</span></div>
  <p v-if="node" class="evidence-summary">{{ node.estimate ? estimateExplanation(node) : nodeEvidenceSummary(node) }}</p>
  <details v-if="node?.estimate"><summary>查看证据依据</summary><p v-if="!node.estimate.performance_observed">尚无独立检查，不显示能力数值。阅读与熟悉反馈已保留，可直接继续学习。</p><p v-else>已作答目标的实验性平均指数 {{ Math.round(node.estimate.familiarity*100) }}/100；不同参数假设下为 {{ Math.round(node.estimate.lower*100) }}–{{ Math.round(node.estimate.upper*100) }}。参数尚未本地校准；该范围不是统计置信区间，也不代表整页的掌握概率。</p></details>
  <div class="node-actions">
   <button :disabled="busy||loading||node?.state==='self_known'||node?.state==='verified'" @click="mark('read')">标记已读</button>
   <button :disabled="busy||loading||node?.state==='verified'" :class="{selected:node?.state==='self_known'}" @click="mark('known')">{{ node?.state==='verified'?'已验证通过':'纠正：已经熟悉' }}</button>
   <button :disabled="busy||loading" :class="{selected:node?.state==='review'}" @click="mark('review')">纠正：仍有困难</button>
  </div>
  <p v-if="error" role="alert">{{ error }} <button @click="load">重试读取</button></p>
  <p v-else-if="notice" role="status">{{ notice }}</p>
  <p v-else>阅读会自动更新进度与建议；熟悉或困难反馈可帮助调整顺序。</p>
  <p v-if="readStatus" role="status" :class="{'read-error':readFailed}">{{ readStatus }}</p>
  <RecallReview v-if="node" :key="`${kbId}:${slug}`" :kb-id="kbId" :slug="slug" :title="node.title||slug" :review="node.review" :disabled="busy||loading||!!error" @updated="notifyLearningUpdated(kbId,slug)" @reload="reloadReview" />
 </section>
</template>
<script setup lang="ts">
import {ref,watch,onMounted,onUnmounted,onActivated,onDeactivated} from 'vue'
import {getObjectiveView,setNodeState,type LearningNodeView} from '@/api/learning/objectives'
import {LEARNING_UPDATED,LEARNING_READ_STATUS,notifyLearningUpdated,nodeStateLabels,nodeStateColors} from './learningEvents'
import {nodeEvidenceSummary} from './learningOverview'
import {estimateLabels,estimateColors,estimateExplanation} from './learningEstimates'
import RecallReview from './RecallReview.vue'
const props=defineProps<{kbId:string;slug:string}>()
const node=ref<LearningNodeView|null>(null),loading=ref(false),busy=ref(false),error=ref(''),notice=ref('')
const readStatus=ref(''),readFailed=ref(false)
let generation=0,active=true
async function load(){const gen=++generation;loading.value=true;error.value='';try{const view=await getObjectiveView(props.kbId);if(gen!==generation)return;if(!view.nodes)throw Error('学习服务版本未同步，请更新后端');node.value=view.nodes.find(n=>n.slug===props.slug)||null}catch(e){if(gen===generation)error.value=e instanceof Error?e.message:'学习记录读取失败'}finally{if(gen===generation)loading.value=false}}
async function mark(state:'read'|'known'|'review'){
 if(busy.value)return;const kb=props.kbId,slug=props.slug;busy.value=true;error.value=''
 try{await setNodeState(kb,slug,state);notifyLearningUpdated(kb,slug,state);if(kb===props.kbId&&slug===props.slug){notice.value=state==='known'?'已记录自认熟悉，将减少重复阅读推荐；这不会增加检查证据。':state==='review'?'已记录困难，优先回看这处内容；重读后可以继续下一项。':'已读已记录，返回学习页可查看下一步。';await load()}}
 catch{if(kb===props.kbId&&slug===props.slug)error.value='未确认保存结果，请先重试读取状态。'}finally{busy.value=false}
}
async function reloadReview(){const kb=props.kbId,slug=props.slug;await load();if(active&&kb===props.kbId&&slug===props.slug&&node.value&&!error.value)notifyLearningUpdated(kb,slug)}
function updated(e:Event){const detail=(e as CustomEvent).detail;if(active&&detail?.kbId===props.kbId&&detail?.slug===props.slug)load()}
function reading(e:Event){const d=(e as CustomEvent).detail;if(!active||d?.kbId!==props.kbId||d?.slug!==props.slug)return;readFailed.value=d.status==='error';readStatus.value=d.status==='saving'?'正在保存阅读进度…':d.status==='disabled'?'自动采集已关闭，本次阅读未自动记录。仍可主动标记已读或反馈估计偏差。':d.status==='error'?'自动保存失败，可点击「标记已读」重试；当前尚未确认保存。':d.tier==='deep'?'✓ 已记录持续阅读，画像和下一步建议已更新。':'✓ 已保存阅读进度，星图和学习导航已同步。'}
watch(()=>[props.kbId,props.slug],()=>{node.value=null;notice.value='';readStatus.value='';readFailed.value=false;if(props.kbId&&props.slug)load()},{immediate:true})
onMounted(()=>{window.addEventListener(LEARNING_UPDATED,updated);window.addEventListener(LEARNING_READ_STATUS,reading)});onUnmounted(()=>{generation++;window.removeEventListener(LEARNING_UPDATED,updated);window.removeEventListener(LEARNING_READ_STATUS,reading)})
onActivated(()=>{active=true;load()});onDeactivated(()=>{active=false;generation++})
</script>
<style scoped>
.known{background:var(--td-brand-color,#07c05f)!important;color:#fff!important;border-color:var(--td-brand-color,#07c05f)!important}.read-error{color:var(--td-error-color)!important}
.node-learning{margin:12px 0;padding:12px 14px;border:1px solid var(--td-component-border,#e5e8eb);border-radius:8px;background:var(--td-bg-color-container,#fff);font-size:12px}.node-status{display:flex;align-items:center;gap:7px;flex-wrap:wrap}.dot{width:8px;height:8px;border-radius:50%}.node-status span:last-child{color:var(--td-text-color-secondary)}.node-actions{display:flex;gap:8px;margin-top:10px;flex-wrap:wrap}button{font:inherit;color:var(--td-text-color-primary);background:var(--td-bg-color-container,#fff);border:1px solid var(--td-component-border,#ddd);border-radius:5px;padding:5px 9px;cursor:pointer}button.selected{color:var(--td-brand-color);border-color:var(--td-brand-color)}button:disabled{opacity:.5;cursor:default}p{margin:8px 0 0;color:var(--td-text-color-secondary);line-height:1.6}
</style>
