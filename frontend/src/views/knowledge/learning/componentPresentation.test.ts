import test from 'node:test'
import assert from 'node:assert/strict'
import {componentGraph,componentLabels} from './componentPresentation'
import type {ComponentView,LearningComponent} from '@/api/learning/components'
test('two skills from one Wiki page keep distinct graph identities and status',()=>{
 const base={available:true,version:'v1',material:{topic:'主题',condition:'测试条件',goal:'分别观察学习目标',explanation:'解释',example:'例子',minutes:2,provenance:'测试样本',checks:[],sources:[{slug:'same-page',quote:'来源',role:'依据'}],prerequisites:[],related:[]},state:{level:'unseen',familiarity:.2,lower:.1,upper:.35,reads:0,passes:0,practice:0,legacy_touches:0,checks:0,seen_check_ids:[],basis:'no observation'}}
 const one={...base,id:'one',material:{...base.material,key:'one',title:'记录事实'},state:{...base.state,level:'learning'}} as LearningComponent
 const two={...base,id:'two',material:{...base.material,key:'two',title:'重算状态',prerequisites:['one'],related:['absent']}} as LearningComponent
 const graph=componentGraph({components:[one,two],steps:[{id:'two'}]} as ComponentView)
 assert.deepEqual(graph.nodes.map(n=>n.slug),['kc:one','kc:two']);assert.equal(graph.nodes[0].learning_contacted,true);assert.equal(graph.nodes[1].learning_contacted,false)
 assert.equal(graph.nodes[0].p_eff,undefined,'reading progress must not publish an unsupported probability')
 assert.deepEqual(graph.edges,[{from:'kc:one',to:'kc:two',kind:'prerequisite'}]);assert.equal(graph.zones[0].next,'kc:two')
})
test('self-reported familiarity remains distinct from independent check support',()=>{
 const c={id:'self',available:true,material:{topic:'主题',key:'self',title:'目标',condition:'适用条件',prerequisites:[],related:[]},state:{level:'self_reported',familiarity:.2,checks:0,basis:'你反馈已经熟悉'}} as unknown as LearningComponent
 const node=componentGraph({components:[c],steps:[]} as unknown as ComponentView).nodes[0]
 assert.equal(node.learning_contacted,true)
 assert.notEqual(node.level,'familiar')
 assert.equal(node.p_eff,undefined)
 assert.match(node.verification_label||'',/自评熟悉/)
 assert.equal(componentLabels.familiar,'检查支持')
})
test('missing or changed-source components never display an available mastery color',()=>{
 assert.deepEqual(componentGraph(null),{nodes:[],edges:[],zones:[]})
 const c={id:'one',available:false,source_problem:'来源已变更',material:{topic:'主题',key:'one',title:'目标',prerequisites:[],related:[]},state:{level:'familiar',familiarity:.9,checks:2}} as unknown as LearningComponent
 const graph=componentGraph({components:[c],steps:[]} as unknown as ComponentView);assert.equal(graph.nodes[0].verification_color,'#a7afb8');assert.equal(graph.nodes[0].verification_label,'来源已变更')
})
