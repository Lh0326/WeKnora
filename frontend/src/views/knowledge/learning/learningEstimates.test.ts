import test from 'node:test'
import assert from 'node:assert/strict'
import type {LearningNodeView} from '@/api/learning/objectives'
import {estimateExplanation,summarizeEstimates} from './learningEstimates'
import {projectObjectiveNodes} from './objectivePresentation'
import {snapshotFields} from '../assistantLearningContext'
import {readingThresholdMs} from '../wiki/readTracker'

const node:LearningNodeView={slug:'a',title:'A',folder_id:'',folder_name:'',state:'learning',reads:1,cites:0,updated:false,objective_total:0,objective_verified:0,estimate:{model_version:'v',level:'developing',familiarity:.32,lower:.16,upper:.48,coverage:1,opportunities:1,expected_gain:.02,answers:0,corrections:0,basis:'reading'}}
test('automatic reading is visible without declaring mastery or verification',()=>{
 const summary=summarizeEstimates([node]);assert.equal(summary.covered,1);assert.equal(summary.counts.developing,1);assert.equal(summary.counts.familiar,0);assert.equal(summary.verified,0)
 const view={projection_version:'v',entries:[],nodes:[node]}
 const projected=projectObjectiveNodes([{slug:'a',title:'A',level:'unseen'} as any],view)[0]!
 assert.match(projected.verification_label!,/已阅读/);assert.equal(projected.verification_complete,false);assert.equal(projected.p_eff,undefined)
 const fields=snapshotFields({view,plan:{policy_version:'v',steps:[]},trajectory:''},'a')!
 assert.match(fields.current_node!,/已阅读/);assert.match(fields.model_progress!,/不要求用户逐页确认/)
 assert.match(estimateExplanation(node),/尚无独立检查/)
})
test('short material has a shorter opportunity threshold, not instant mastery',()=>{
 assert.equal(readingThresholdMs('很短的说明'),8000)
 assert.ok(readingThresholdMs('学'.repeat(350))>readingThresholdMs('很短的说明'))
 assert.equal(readingThresholdMs('学'.repeat(10000)),180000)
})

test('assistant does not confuse a historical difficulty declaration with the current model',()=>{
 const revisited:LearningNodeView={...node,state:'review',estimate:{...node.estimate!,corrections:1}}
 const fields=snapshotFields({view:{projection_version:'v',entries:[],nodes:[revisited]},plan:{policy_version:'v',steps:[]},trajectory:''},'a')!
 assert.match(fields.model_progress!,/建议巩固 0/)
 assert.match(fields.current_node!,/已阅读/)
 assert.doesNotMatch(fields.kb_progress!,/待巩固 1/)
 assert.match(fields.kb_progress!,/历史声明不能覆盖/)
})

test('explanations do not mislabel citations or self-report as reading evidence',()=>{
 const cited:LearningNodeView={...node,reads:0,cites:2,estimate:{...node.estimate!,coverage:0,opportunities:0}}
 assert.match(estimateExplanation(cited),/仅有问答触及/)
 assert.doesNotMatch(estimateExplanation(cited),/主要依据阅读/)
 const corrected={...cited,estimate:{...cited.estimate!,corrections:1}}
 assert.match(estimateExplanation(corrected),/已保存你的熟悉或困难反馈/)
 assert.doesNotMatch(estimateExplanation(corrected),/主要依据阅读/)
})


test('self-reported familiarity has its own color category and assistant count',()=>{
 const self:LearningNodeView={...node,state:'self_known',estimate:{...node.estimate!,level:'self_reported',self_report:'known',performance_observed:false,corrections:1}}
 const summary=summarizeEstimates([self]);assert.equal(summary.counts.self_reported,1);assert.equal(summary.counts.familiar,0);assert.equal(summary.verified,0)
 const fields=snapshotFields({view:{projection_version:'v',entries:[],nodes:[self]},plan:null,trajectory:''},'a')!
 assert.match(fields.current_node!,/自认熟悉/);assert.match(fields.model_progress!,/检查支持 0；自认熟悉 1/);assert.match(fields.model_progress!,/没有独立检查时不显示能力数值/)
})
