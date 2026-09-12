<template>
 <section class="model-overview" aria-label="全库学习记录">
  <div class="model-heading"><div><span>全库学习记录</span><p><strong>{{ summary.covered }}</strong> / {{ summary.total }} 已接触</p></div><div class="model-key"><strong>{{ summary.counts.familiar }}</strong><span>检查支持</span></div></div>
  <div class="model-bar" role="img" :aria-label="estimateLevels.map(s=>`${estimateLabels[s]} ${summary.counts[s]}`).join('，')"><i v-for="state in estimateLevels" :key="state" :style="{width:`${summary.total?summary.counts[state]/summary.total*100:0}%`,background:estimateColors[state]}" /></div>
  <div class="model-states"><button v-for="state in estimateLevels" :key="state" @click="selected=state;open=true"><span><i :style="{background:estimateColors[state]}" />{{ estimateLabels[state] }}</span><strong>{{ summary.counts[state] }}</strong></button></div>
  <p class="model-hint">有效阅读会更新画像和下一步，无需逐页确认；可随时反馈“已经熟悉”或“仍有困难”。</p>
  <details><summary>状态依据与验证</summary><p>阅读只更新进度，自评只调整推荐；{{ summary.uncertain }} 个节点尚缺少独立作答依据。回忆记录用于 FSRS 遗忘预测。另有 {{ summary.verified }} 个节点的目标验证通过。</p></details>
  <t-drawer v-model:visible="open" :header="`${estimateLabels[selected]} · ${filtered.length} 个知识点`" size="480px" :footer="false">
   <button v-for="node in filtered.slice(0,visibleCount)" :key="node.slug" class="model-node" @click="open=false;emit('open',node.slug)"><strong>{{ node.title||node.slug }}</strong><span>{{ node.folder_name||'未分组知识' }}</span><small>{{ estimateExplanation(node) }}</small></button>
   <button v-if="filtered.length>visibleCount" class="model-node" @click="visibleCount+=100">再显示 100 个知识点</button>
  </t-drawer>
 </section>
</template>
<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import type {LearningNodeView,EstimateLevel} from '@/api/learning/objectives'
import {estimateLevels,estimateLabels,estimateColors,estimateLevel,estimateExplanation,summarizeEstimates} from './learningEstimates'
const props=defineProps<{nodes:LearningNodeView[]}>()
const emit=defineEmits<{(e:'open',slug:string):void}>()
const summary=computed(()=>summarizeEstimates(props.nodes))
const open=ref(false),selected=ref<EstimateLevel>('unseen')
const filtered=computed(()=>props.nodes.filter(n=>estimateLevel(n)===selected.value))
const visibleCount=ref(100)
watch([selected,open],()=>visibleCount.value=100)
</script>
<style scoped>
.model-overview{display:flex;flex-direction:column;gap:10px}.model-heading{display:flex;justify-content:space-between;align-items:center}.model-heading span{font-size:11px;color:var(--td-text-color-secondary)}.model-heading p{margin:5px 0 0;font-size:12px}.model-heading strong{font-size:25px}.model-key{display:flex;flex-direction:column;align-items:flex-end;gap:3px}.model-key strong{color:var(--td-brand-color)}.model-bar{height:8px;display:flex;overflow:hidden;border-radius:4px;background:var(--td-bg-color-component)}.model-bar i{transition:width .3s}.model-states{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:6px}.model-states button{display:flex;justify-content:space-between;align-items:center;gap:4px;padding:8px 6px;border:1px solid transparent;border-radius:6px;background:var(--td-bg-color-component);color:var(--td-text-color-primary);cursor:pointer}.model-states button:hover{border-color:var(--td-brand-color)}.model-states span{font-size:11px;white-space:nowrap}.model-states i{display:inline-block;width:6px;height:6px;border-radius:50%;margin-right:4px}.model-hint,details p{margin:0;font-size:11px;line-height:1.7;color:var(--td-text-color-secondary)}details{font-size:11px;color:var(--td-text-color-secondary)}summary{cursor:pointer}details p{margin-top:8px}.model-node{display:flex;flex-direction:column;gap:6px;width:100%;padding:14px 0;text-align:left;border:0;border-bottom:1px solid var(--td-component-border);background:none;color:var(--td-text-color-primary);cursor:pointer}.model-node span,.model-node small{color:var(--td-text-color-secondary);line-height:1.6}button:focus-visible{outline:2px solid var(--td-brand-color);outline-offset:2px}
</style>
