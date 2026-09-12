import test from 'node:test'
import assert from 'node:assert/strict'
import {createAssistantLearningContext,snapshotFields,type AssistantLearningSnapshot} from './assistantLearningContext'
const snap=(title:string):AssistantLearningSnapshot=>({view:{projection_version:'v',entries:[],nodes:[{slug:'a',title,folder_id:'',folder_name:'',state:'self_known',reads:1,cites:0,updated:false,objective_total:0,objective_verified:0}]},plan:{policy_version:'v',steps:[]},trajectory:title})
test('late response cannot leak a prior scope into a new knowledge base',async()=>{const pending=new Map<string,(v:AssistantLearningSnapshot)=>void>();const c=createAssistantLearningContext(kb=>new Promise(resolve=>pending.set(kb,resolve)));const readSnapshot=()=>c.snapshot.value;const a=c.ensure('a','alice:1:a');const b=c.ensure('b','alice:1:b');pending.get('b')!(snap('B'));await b;pending.get('a')!(snap('A'));await a;assert.equal(readSnapshot()?.trajectory,'B');const n=c.ensure('b','bob:1:b');assert.equal(readSnapshot(),null);pending.get('b')!(snap('Bob'));await n;assert.equal(readSnapshot()?.trajectory,'Bob')})
test('forced refresh rejects stale flights and reset clears cached records',async()=>{const resolvers:Array<(v:AssistantLearningSnapshot)=>void>=[];const c=createAssistantLearningContext(()=>new Promise(resolve=>resolvers.push(resolve)));const a=c.ensure('a','scope');const b=c.ensure('a','scope',true);resolvers[1]!(snap('new'));await b;resolvers[0]!(snap('old'));await a;assert.equal(c.snapshot.value?.trajectory,'new');c.reset('logged-out');assert.equal(c.snapshot.value,null)})
test('self report cannot be described as objective validation or a probability',()=>{const fields=snapshotFields(snap('A'),'a')!;assert.match(fields.kb_progress!,/自认已会 1/);assert.match(fields.kb_progress!,/验证通过 0/);assert.match(fields.current_node!,/自认已会/);assert.match(fields.current_node!,/0\/0/);assert.doesNotMatch(fields.current_node!,/%/)})
test('unavailable snapshots are not represented as zero learning',()=>{const fields=snapshotFields({view:null,plan:null,trajectory:''},null)!;assert.match(fields.kb_progress!,/暂不可用/);assert.match(fields.next_recommended!,/暂不可用/);assert.equal(snapshotFields(null,null),null)})

test('switching to the shared workspace cancels the default-plan flight and requests only history',async()=>{
 const calls:Array<{history:boolean;resolve:(s:AssistantLearningSnapshot)=>void}>=[]
 const c=createAssistantLearningContext((_kb,options)=>new Promise(resolve=>calls.push({history:!!options?.trajectoryOnly,resolve})))
 const fallback=c.ensure('a','scope')
 const shared=c.ensure('a','scope',false,{trajectoryOnly:true})
 assert.deepEqual(calls.map(c=>c.history),[false,true])
 calls[1]!.resolve({view:null,plan:null,trajectory:'最新历史'});await shared
 calls[0]!.resolve(snap('旧默认计划'));await fallback
 assert.equal(c.snapshot.value?.plan,null)
 assert.equal(c.snapshot.value?.trajectory,'最新历史')
})
