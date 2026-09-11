import {createApp,h,ref,provide,computed} from 'vue'
import TDesign from 'tdesign-vue-next'
import 'tdesign-vue-next/es/style/index.css'
import {createI18n} from 'vue-i18n'
import zhCN from '../../src/i18n/locales/zh-CN'
import Workspace from '../../src/views/knowledge/learning/ObjectiveWorkspace.vue'
import Constellation from '../../src/views/knowledge/learning/LearningConstellation.vue'
import NodeControls from '../../src/views/knowledge/learning/NodeLearningControls.vue'
import {projectObjectiveNodes} from '../../src/views/knowledge/learning/objectivePresentation'
import {createLearningSession,learningSessionKey} from '../../src/views/knowledge/learning/learningSession'
import {snapshotFields} from '../../src/views/knowledge/assistantLearningContext'
import {readNode,verifyConcept,fixtureNodes,failNextPath,failNextRecall} from './mockApi'
const app=createApp({setup(){
 const panel=ref(),view=ref<any>(null),plan=ref<any>(null),selected=ref('')
 const session=createLearningSession();session.setScope('fixture-user','fixture-kb');provide(learningSessionKey,session)
 const assistant=computed(()=>snapshotFields(null,selected.value,session.snapshot.value))
 const open=(slug:string)=>{selected.value=slug;readNode(slug);panel.value.refresh()}
 return()=>h('main',{style:'max-width:1120px;margin:20px auto;padding:16px;font-family:sans-serif;color:#222'},[
  h('p',{style:'padding:10px;background:#fff3cd;margin-bottom:14px'},'工程交互夹具：实际 Vue 组件 + 模拟 API；不调用模型，无真实用户或学习效果数据。'),
  h('div',{class:'fixture-grid'},[
   h('div',[h(Constellation,{verification:true,nodes:projectObjectiveNodes(fixtureNodes.map(n=>({...n,level:'unseen'})),view.value),edges:[{from:'concept/rag',to:'concept/config',kind:'prereq'}],zones:[{id:'rag',name:'检索增强生成',next:plan.value?.steps.find((s:any)=>s.slug.startsWith('concept/r'))?.slug},{id:'basics',name:'知识库基础'}],onOpen:open}),selected.value?h(NodeControls,{kbId:'fixture-kb',slug:selected.value}):null]),
   h('div',{class:'fixture-sidebar'},[h(Workspace,{ref:panel,onView:(v:any)=>view.value=v,onPlan:(p:any)=>plan.value=p,kbId:'fixture-kb',onOpen:open,onQuiz:()=>{verifyConcept();panel.value.refresh()}})])
  ]),
  h('details',{open:true,style:'margin-top:24px;padding:14px;border:1px solid #ddd'},[h('summary','助手实际接收的学习上下文（工程检查）'),h('p',assistant.value?.learning_scope||'尚无计划'),h('p',assistant.value?.learning_plan_status||''),h('p',assistant.value?.next_recommended||''),h('p',assistant.value?.recall_schedule||''),h('button',{onClick:()=>{failNextPath();panel.value.refresh()}},'模拟下一次规划失败'),h('button',{onClick:failNextRecall},'模拟复习保存后响应丢失')])
 ])
}})
app.use(TDesign).use(createI18n({legacy:false,locale:'zh-CN',messages:{'zh-CN':zhCN}})).mount('#app')
