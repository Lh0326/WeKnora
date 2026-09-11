<template>
 <section class="recall" aria-label="个人间隔复习" :aria-busy="busy">
  <div class="recall-heading"><strong>间隔复习 <span>自愿加入</span></strong><button v-if="status?.active" :disabled="disabled||busy||!!error" @click="submit('pause')">暂停复习</button><button v-else :disabled="disabled||busy||!!error" @click="submit('enroll')">{{ status?'继续间隔复习':'加入间隔复习' }}</button></div>
  <p v-if="status">{{ recallSummary(status) }}</p>
  <template v-if="!status?.active"><small>日常阅读会自动更新学习画像；需要长期记牢的内容可以加入复习。</small></template>
  <template v-else-if="stage==='idle'"><button class="recall-primary" :disabled="disabled||busy||!!error" @click="stage='recall'">{{ recallDue(status)?'开始回忆':'提前练习一次' }}</button></template>
  <div v-else class="recall-practice">
   <strong>用自己的话回忆「{{ title }}」</strong>
   <p>它是什么？什么时候会用到？尝试说出一个关键步骤或限制，再回看下方材料核对。</p>
   <button v-if="stage==='recall'" :disabled="disabled||busy||!!error" @click="stage='rate'">已尝试回忆并核对材料</button>
   <template v-else><p>核对前，你回忆起来有多轻松？</p><div class="recall-ratings"><button v-for="choice in choices" :key="choice.action" :disabled="disabled||busy||!!error" @click="submit(choice.action)"><strong>{{ choice.label }}</strong><span>{{ choice.hint }}</span></button></div></template>
   <button class="recall-cancel" :disabled="busy||!!error" @click="stage='idle'">稍后再练</button>
  </div>
  <p v-if="notice" role="status">{{ notice }}</p>
  <div v-if="error" role="alert"><p>{{ error }}</p><button v-if="!conflict" :disabled="busy" @click="submit(lastAction)">重试保存本次反馈</button> <button :disabled="busy" @click="reload">重新读取状态</button></div>
  <details><summary>复习如何安排</summary><p>FSRS 根据回忆成败、间隔和难度估计记忆稳定性，自动安排下一次复习，目标保持率为 90%，最长间隔 365 天。刚开始可能先安排短时复习；材料更新后重新起算。旧安排在下一次反馈时切换到 FSRS。阅读和“已经熟悉”不会冒充回忆成功，验证记录单独保留。</p></details>
 </section>
</template>
<script setup lang="ts">
import {ref,watch,onUnmounted,onDeactivated} from 'vue'
import {updateReviewSchedule,type LearningReviewStatus,type RecallAction} from '@/api/learning/objectives'
import {recallDue,recallSummary,createRecallFeedback} from './recallFeedback'
const props=defineProps<{kbId:string;slug:string;title:string;review?:LearningReviewStatus;disabled?:boolean}>()
const emit=defineEmits<{(e:'updated'):void;(e:'reload'):void}>()
const status=ref(props.review),busy=ref(false),error=ref(''),notice=ref(''),conflict=ref(false),stage=ref<'idle'|'recall'|'rate'>('idle')
const choices:Array<{action:RecallAction;label:string;hint:string}>=[{action:'again',label:'忘记了',hint:'需要重学'},{action:'hard',label:'费力想起',hint:'基本正确'},{action:'good',label:'正常想起',hint:'略有犹豫'},{action:'easy',label:'轻松想起',hint:'完整准确'}]
const feedback=createRecallFeedback(updateReviewSchedule)
let lastAction:RecallAction='enroll',generation=0
watch(()=>[props.kbId,props.slug],()=>{generation++;feedback.setTarget(props.kbId,props.slug);status.value=props.review;error.value='';notice.value='';busy.value=false;stage.value='idle'},{immediate:true})
watch(()=>props.review,value=>{status.value=value})
async function submit(action:RecallAction){
 if(busy.value||props.disabled)return
 const gen=generation;lastAction=action;busy.value=true;error.value='';notice.value=''
 try{const result=await feedback.send(action,status.value);if(!result||gen!==generation)return;status.value=result;stage.value='idle';notice.value=action==='pause'?'已暂停复习推荐。':action==='enroll'?'已加入复习，可以先回忆一次作为起点。':result.early_practice?'反馈已保存；本次提前练习保留原定复习时间。':action==='again'?'已记录需要巩固，约 10 分钟后再试一次。':'回忆反馈已保存，下次复习时间已更新。';emit('updated')}
 catch(e){if(gen!==generation)return;conflict.value=(e as {response?:{status?:number}})?.response?.status===409;error.value=conflict.value?'材料或复习安排已在其他页面变化，请重新读取后再反馈。':'暂未确认保存结果。可以重试同一条反馈，或重新读取状态核对。'}
 finally{if(gen===generation)busy.value=false}
}
function reload(){generation++;feedback.discard();error.value='';notice.value='';stage.value='idle';emit('reload')}
function deactivate(){generation++;feedback.discard();busy.value=false;stage.value='idle';error.value=''}
onUnmounted(deactivate);onDeactivated(deactivate)
</script>
<style scoped>
.recall{border-top:1px solid var(--td-component-border);margin-top:12px;padding-top:12px;display:flex;flex-direction:column;gap:9px;font-size:12px}.recall-heading{display:flex;align-items:center;justify-content:space-between;gap:10px}.recall-heading strong>span{font-size:10px;font-weight:400;margin-left:5px;color:var(--td-text-color-secondary)}.recall p{margin:0;line-height:1.65;color:var(--td-text-color-secondary)}.recall button{font:inherit;padding:7px 10px;border:1px solid var(--td-component-border);border-radius:6px;background:var(--td-bg-color-container);color:var(--td-text-color-primary);cursor:pointer;align-self:flex-start}.recall button:disabled{opacity:.5;cursor:default}.recall button:focus-visible{outline:2px solid var(--td-brand-color);outline-offset:2px}.recall .recall-primary{color:var(--td-brand-color);border-color:var(--td-brand-color)}.recall small,.recall summary{font-size:11px;color:var(--td-text-color-secondary)}.recall summary{cursor:pointer}.recall details p{margin-top:8px}.recall-practice{padding:12px;background:var(--td-bg-color-component);border-radius:7px;display:flex;flex-direction:column;gap:10px}.recall-ratings{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:6px}.recall-ratings button{display:flex;flex-direction:column;gap:5px;align-items:center;padding:9px 5px}.recall-ratings strong{font-weight:500}.recall-ratings span{font-size:10px;color:var(--td-text-color-secondary)}.recall .recall-cancel{border:0;background:none;padding:0;font-size:11px;color:var(--td-text-color-secondary)}@media(max-width:540px){.recall-ratings{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
