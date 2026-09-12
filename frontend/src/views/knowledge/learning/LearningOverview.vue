<template>
  <ModelOverview v-if="nodes.some(n=>n.estimate)" :nodes="nodes" @open="emit('open',$event)" />
  <section v-else class="overview" aria-label="个人知识掌握概览">
    <div class="overview-top">
      <div class="coverage-ring" :style="{background:ringGradient}" role="img" :aria-label="`已会覆盖：自认已会 ${summary.counts.self_known}，验证通过 ${summary.counts.verified}，共 ${summary.total} 个知识点`">
        <div><strong>{{ summary.total ? Math.round(summary.known / summary.total * 100) : 0 }}<small>%</small></strong><span>已会覆盖</span></div>
      </div>
      <div class="overview-copy"><span class="eyebrow">我的知识版图</span><p><strong>{{ summary.known }}</strong> / {{ summary.total }} <span>已会（含自认）</span></p><small>自认已会 {{ summary.counts.self_known }} · 验证通过 {{ summary.counts.verified }}</small><small>已接触 {{ summary.covered }} · 待学习与巩固 {{ summary.pending }}</small></div>
    </div>
    <div class="state-grid" aria-label="点击按学习状态查看知识点">
      <button v-for="state in learningStates" :key="state" @click="selectedState=state; search=''; listOpen=true">
        <span><i :style="{background:nodeStateColors[state]}"></i>{{ nodeStateLabels[state] }}</span><strong>{{ summary.counts[state] }}</strong>
      </button>
      <button @click="selectedState='recall';search='';listOpen=true"><span><i style="background:#8b70bd"></i>到期复习</span><strong>{{ dueNodes.length }}</strong></button>
    </div>
    <details v-if="summary.total" class="overview-explanation"><summary>状态依据与学习提示</summary>
    <p class="overview-hint">{{ summary.verifiable ? `${summary.verifiable}/${summary.total} 个知识点已有审核目标，可进一步验证理解。` : '本库暂无已审核验证目标；可以自主确认学会，客观验证单独统计。' }}</p>
    <div v-if="focus" class="learning-focus"><p>{{ focus.text }}</p><button @click="selectedState=focus.state;search='';listOpen=true">{{ focus.action }} →</button></div>
    </details>
    <p v-else class="overview-hint">本库还没有可学习的实体或概念，生成 Wiki 后即可开始。</p>
    <t-drawer v-model:visible="listOpen" :header="`${selectedState==='recall'?'到期复习':nodeStateLabels[selectedState]} · ${filtered.length} 个知识点`" size="480px" :footer="false">
      <input v-model="search" class="node-search" placeholder="搜索知识点或模块" aria-label="搜索知识点或模块" />
      <p class="overview-hint">{{ selectedState==='recall'?'你主动加入的间隔复习，与上方五种学习状态可重叠；到期不代表验证失效。点击知识点后开始回忆。':'点击知识点可阅读、确认已会或加入待巩固。' }}</p>
      <button v-for="node in filtered.slice(0,limit)" :key="node.slug" class="node-row" @click="listOpen=false; emit('open',node.slug)"><span><strong>{{ node.title || node.slug }}</strong><small>{{ node.folder_name || '未分组知识' }}</small></span><span>阅读 →</span></button>
      <p v-if="!filtered.length" class="overview-hint">没有符合条件的知识点。</p>
      <button v-if="filtered.length>limit" class="more" @click="limit+=100">加载更多</button>
    </t-drawer>
  </section>
</template>
<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import ModelOverview from './ModelOverview.vue'
import type {LearningNodeView} from '@/api/learning/objectives'
import {learningStates,summarizeNodes,learningFocus,type LearningState} from './learningOverview'
import {nodeStateColors,nodeStateLabels} from './learningEvents'
import {recallDue} from './recallFeedback'
const props=defineProps<{nodes:LearningNodeView[]}>()
const emit=defineEmits<{(e:'open',slug:string):void}>()
const summary=computed(()=>summarizeNodes(props.nodes))
const focus=computed(()=>learningFocus(props.nodes))
const ringGradient=computed(()=>{let offset=0;return `conic-gradient(${(['verified','self_known','learning','review','unseen'] as const).map(s=>{const start=offset;offset+=summary.value.total?summary.value.counts[s]/summary.value.total*100:100;return `${nodeStateColors[s]} ${start}% ${offset}%`}).join(',')})`})
const dueNodes=computed(()=>props.nodes.filter(n=>recallDue(n.review)))
const listOpen=ref(false),selectedState=ref<LearningState|'recall'>('unseen'),search=ref(''),limit=ref(100)
const filtered=computed(()=>(selectedState.value==='recall'?dueNodes.value:props.nodes.filter(n=>n.state===selectedState.value)).filter(n=>`${n.title} ${n.folder_name} ${n.slug}`.toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase())))
watch([search,selectedState],()=>limit.value=100)
</script>
<style scoped>
.overview-explanation{font-size:11px;color:var(--td-text-color-secondary)}.overview-explanation summary{cursor:pointer}.overview-explanation[open] summary{margin-bottom:8px}.overview-explanation .learning-focus{margin-top:8px}
.learning-focus{border-left:2px solid var(--td-brand-color);padding:2px 0 2px 10px;font-size:11px;line-height:1.6;color:var(--td-text-color-secondary)}.learning-focus p{margin:0 0 4px}.learning-focus button{border:0;background:none;color:var(--td-brand-color);padding:0;font:inherit;cursor:pointer}
.overview{display:flex;flex-direction:column;gap:12px}.overview-top{display:flex;align-items:center;gap:16px}.coverage-ring{width:88px;height:88px;border-radius:50%;padding:7px;box-sizing:border-box;flex-shrink:0}.coverage-ring>div{height:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;border-radius:50%;background:var(--td-bg-color-container,#fff)}.coverage-ring strong{font-size:23px;line-height:1.2}.coverage-ring small{font-size:12px}.coverage-ring span{font-size:10px;color:var(--td-text-color-secondary)}.overview-copy{min-width:0;display:flex;flex-direction:column;gap:6px}.eyebrow{font-size:12px;color:var(--td-text-color-secondary)}.overview-copy p{margin:0;font-size:15px}.overview-copy p strong{font-size:25px}.overview-copy small{font-size:11px;line-height:1.5;color:var(--td-text-color-secondary)}.state-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:6px}.state-grid button{display:flex;justify-content:space-between;align-items:center;gap:4px;padding:9px 7px;background:var(--td-bg-color-component,#f5f7f8);border:1px solid transparent;border-radius:7px;color:var(--td-text-color-primary);cursor:pointer}.state-grid button:hover{border-color:var(--td-brand-color)}.state-grid span{font-size:11px;white-space:nowrap}.state-grid i{display:inline-block;width:6px;height:6px;border-radius:50%;margin-right:4px}.state-grid strong{font-size:15px}.overview-hint{margin:0;font-size:11px;line-height:1.6;color:var(--td-text-color-secondary)}.node-search{width:100%;box-sizing:border-box;padding:10px;margin-bottom:12px;border:1px solid var(--td-component-border);border-radius:6px;background:var(--td-bg-color-container);color:var(--td-text-color-primary)}.node-row{display:flex;width:100%;align-items:center;justify-content:space-between;gap:16px;padding:14px 0;border:0;border-bottom:1px solid var(--td-component-border);background:none;text-align:left;color:var(--td-text-color-primary);cursor:pointer}.node-row>span:first-child{display:flex;flex-direction:column;gap:5px;min-width:0}.node-row strong{font-size:14px;overflow-wrap:anywhere}.node-row small,.node-row>span:last-child{font-size:12px;color:var(--td-text-color-secondary)}.node-row>span:last-child{white-space:nowrap}.more{margin-top:16px}button:focus-visible{outline:2px solid var(--td-brand-color);outline-offset:2px}
@media(max-height:800px){.overview{gap:8px}.overview-top{gap:12px}.coverage-ring{width:70px;height:70px;padding:6px}.coverage-ring strong{font-size:20px}.overview-copy{gap:3px}.overview-copy p strong{font-size:21px}.state-grid button{padding:6px 7px}}
</style>
