<template>
 <section class="path-panel" aria-label="下一步学习">
  <div class="path-heading"><h3>本轮学习路径</h3><span v-if="plan?.steps.length">{{ plan.steps.length }} 步 · 约 {{ totalMinutes }} 分钟</span></div>
  <template v-if="current">
   <article class="current-step">
    <div class="step-meta"><span class="now-dot"></span>建议现在 · {{ actionLabel(current.action) }}<span>约 {{ current.minutes }} 分钟</span></div>
    <h4>{{ current.title || current.slug }}</h4>
    <p class="reason">{{ current.reason?.detail || reasonLabel(current.reason?.code) }}</p>
    <p v-if="current.requires" class="reason">先完成：{{ requiresLabel(current.requires) }}</p>
    <button class="primary" :disabled="busy" @click="canChallenge ? emit('challenge',current.slug,current.objective!) : emit('open',current.slug)">{{ canChallenge ? '开始验证' : current.action==='recall'?'开始回忆':current.action==='confirm' ? '回看并确认理解' : '开始阅读' }} <span>→</span></button>
    <div class="secondary-actions"><button v-if="current.action!=='recall'" :disabled="busy" @click="emit('known',current.slug)">已经熟悉？减少重复推荐</button><span v-else>可在知识点中暂停复习</span><button :disabled="busy" @click="emit('skip',current.slug)">换一个</button></div>
    <p class="done-when">{{ current.done_when }}</p>
   </article>
   <ol v-if="upcoming.length" class="upcoming" aria-label="接下来学习什么">
    <li v-for="(step,index) in upcoming.slice(0,expanded?upcoming.length:2)" :key="step.id"><span class="step-number">{{ index+2 }}</span><button :disabled="busy" @click="emit('open',step.slug)"><strong>{{ step.title || step.slug }}</strong><small>{{ actionLabel(step.action) }} · {{ step.requires ? '先完成前置步骤 · ' : '' }}约 {{ step.minutes }} 分钟</small></button><span class="step-arrow">↗</span></li>
   </ol>
   <button v-if="upcoming.length>2" class="expand-path" @click="expanded=!expanded">{{ expanded?'收起后续步骤':`还有 ${upcoming.length-2} 步 · 展开完整路径` }}</button>
  </template>
  <p v-else-if="busy" class="reason" role="status">正在根据学习记录规划下一步…</p>
  <p v-else-if="!plan" class="reason">学习路径暂不可用，请重试；也可以从星图直接阅读。</p>
  <p v-if="plan?.degrade" class="path-note" role="status">{{ degradeLabel(plan.degrade) }}</p>
  <p v-if="plan?.personalization==='unavailable'" class="path-note" role="status">长期记忆暂不可用，本轮仍按学习记录与材料顺序规划。</p>
 </section>
</template>
<script setup lang="ts">
import {computed,ref} from 'vue'
import type {LearningPathPlan} from '@/api/learning/objectives'
import {pathActionLabel as actionLabel,pathReasonLabel as reasonLabel,pathDegradeLabel as degradeLabel} from './pathLabels'
const props=defineProps<{plan:LearningPathPlan|null;busy?:boolean}>()
const emit=defineEmits<{(e:'open',slug:string):void;(e:'challenge',slug:string,objective:string):void;(e:'known',slug:string):void;(e:'skip',slug:string):void}>()
const pending=computed(()=>props.plan?.steps.filter(s=>!s.completed)||[])
const current=computed(()=>pending.value[0])
const upcoming=computed(()=>pending.value.slice(1))
const expanded=ref(false)
const totalMinutes=computed(()=>pending.value.reduce((n,s)=>n+s.minutes,0))
const canChallenge=computed(()=>!!current.value?.objective&&!current.value.requires&&['verify','review','practice'].includes(current.value.action))
function requiresLabel(ids:string){return ids.split('; ').map(id=>props.plan?.steps.find(s=>s.id===id)).filter(Boolean).map(s=>s!.title||s!.slug).join('；')||'前置材料'}
</script>
<style scoped>
.expand-path{border:0;background:none;padding:0;color:var(--td-text-color-secondary);font-size:11px;text-align:left;cursor:pointer}
.path-panel{display:flex;flex-direction:column;gap:12px;color:var(--td-text-color-primary)}h3,h4,p{margin:0}.path-heading{display:flex;align-items:center;justify-content:space-between;gap:8px}.path-heading h3{font-size:14px;font-weight:600}.path-heading>span{font-size:11px;color:var(--td-text-color-secondary)}.current-step{display:flex;flex-direction:column;gap:11px;padding:14px;border:1px solid rgba(20,132,82,.25);border-radius:10px;background:linear-gradient(140deg,rgba(20,132,82,.07),transparent 80%)}.step-meta{display:flex;align-items:center;gap:5px;font-size:11px;color:var(--td-text-color-secondary)}.step-meta>span:last-child{margin-left:auto}.now-dot{width:6px;height:6px;border-radius:50%;background:#148452}.current-step h4{font-size:17px;line-height:1.4;font-weight:600;overflow-wrap:anywhere}.reason{font-size:12px;line-height:1.65;color:var(--td-text-color-secondary)}.primary{display:flex;justify-content:space-between;width:100%;padding:10px 14px;border:0;border-radius:6px;background:var(--td-brand-color,#07c05f);color:#fff;font:inherit;font-size:13px;cursor:pointer}.primary:disabled,button:disabled{opacity:.5;cursor:default}.secondary-actions{display:flex;justify-content:space-between;gap:8px}.secondary-actions button{border:0;background:none;padding:3px 0;color:var(--td-text-color-secondary);font-size:11px;cursor:pointer}.secondary-actions button:first-child{color:var(--td-brand-color)}.done-when{padding-top:9px;border-top:1px solid rgba(20,132,82,.12);font-size:10px;line-height:1.6;color:var(--td-text-color-secondary)}.upcoming{padding:0;margin:0;list-style:none;display:flex;flex-direction:column}.upcoming li{display:flex;align-items:center;gap:10px;padding:10px 2px;border-bottom:1px solid var(--td-component-border)}.step-number{display:grid;place-items:center;width:22px;height:22px;flex-shrink:0;border-radius:50%;background:var(--td-bg-color-component);color:var(--td-text-color-secondary);font-size:11px}.upcoming button{flex:1;min-width:0;display:flex;flex-direction:column;gap:4px;border:0;background:none;text-align:left;color:var(--td-text-color-primary);cursor:pointer;padding:0}.upcoming strong{font-size:12px;font-weight:500;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.upcoming small,.step-arrow{font-size:10px;color:var(--td-text-color-secondary)}.path-note{font-size:11px;line-height:1.6;color:var(--td-text-color-secondary);padding:10px;background:var(--td-bg-color-component);border-radius:6px}button:focus-visible{outline:2px solid var(--td-brand-color);outline-offset:3px}
@media(max-height:800px){.current-step{gap:8px;padding:11px}.path-panel{gap:9px}.done-when{padding-top:7px}}
</style>
