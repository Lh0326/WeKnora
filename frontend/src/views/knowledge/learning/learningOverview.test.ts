import test from 'node:test'
import assert from 'node:assert/strict'
import type {LearningNodeView,ObjectiveViewResponse} from '@/api/learning/objectives'
import {summarizeNodes,groupLearningNodes,learningStates,learningFocus,nodeEvidenceSummary} from './learningOverview'
import {projectObjectiveNodes} from './objectivePresentation'
const nodes:LearningNodeView[]=learningStates.map((state,i)=>({slug:`p${i}`,title:`知识 ${i}`,folder_id:i<3?'a':'b',folder_name:i<3?'基础':'应用',state,reads:state==='unseen'?0:1,cites:0,updated:false,objective_total:state==='verified'?1:0,objective_verified:state==='verified'?1:0}))
test('all page states have disjoint counts; confirmed known and verified remain distinct',()=>{const s=summarizeNodes(nodes);assert.equal(s.total,5);assert.equal(s.known,2);assert.equal(s.covered,4);assert.equal(s.pending,3);assert.equal(Object.values(s.counts).reduce((a,b)=>a+b),5);assert.equal(s.counts.verified,1)})
test('module totals reconcile with the whole library and review modules rank first',()=>{const g=groupLearningNodes(nodes);assert.equal(g[0]!.id,'b');assert.equal(g.reduce((n,g)=>n+g.total,0),5);assert.equal(g.reduce((n,g)=>n+g.known,0),2)})
test('graph and dashboard agree for pages without an objective bank',()=>{const v:ObjectiveViewResponse={nodes,entries:[],projection_version:'v2'};const graph=projectObjectiveNodes(nodes.map(n=>({...n,level:'unseen'})),v);assert.equal(graph.filter(n=>n.learning_contacted).length,4);assert.equal(graph.filter(n=>n.verification_complete).length,1);assert.equal(graph.find(n=>n.slug==='p2')!.level,'familiar');assert.equal(graph.find(n=>n.slug==='p2')!.verification_complete,false)})
test('an empty library has finite totals and no artificial module',()=>{assert.deepEqual(groupLearningNodes([]),[]);assert.equal(summarizeNodes([]).total,0)})

test('guidance prioritizes explicit review, then unseen gaps, then unresolved understanding',()=>{
 assert.equal(learningFocus(nodes)?.state,'review')
 assert.equal(learningFocus(nodes.filter(n=>n.state!=='review'))?.state,'unseen')
 assert.equal(learningFocus(nodes.filter(n=>['learning','self_known','verified'].includes(n.state)))?.state,'learning')
 assert.equal(learningFocus([]),null)
})
test('citation-only contact never claims the user read or understood the page',()=>{
 const n={...nodes[1]!,reads:0,cites:3}
 assert.match(nodeEvidenceSummary(n),/尚无阅读记录/)
 assert.match(nodeEvidenceSummary({...n,state:'self_known'}),/主动确认/)
 assert.equal(summarizeNodes(nodes).verifiable,1)
})
