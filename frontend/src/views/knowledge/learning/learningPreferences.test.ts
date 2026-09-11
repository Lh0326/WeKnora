import test from 'node:test'
import assert from 'node:assert/strict'
import {createLearningPreferences,learningScopeSlugs,learningPreferenceError} from './learningPreferences'
import type {LearningPlanSettings,LearningNodeView} from '@/api/learning/objectives'
const defaults=():LearningPlanSettings=>({revision:'',limit_to_folder:false,folder_id:'',depth:'aware',time_budget_minutes:15,use_memory:true,goal_objectives:[]})
function deferred<T>(){let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>resolve=r);return {resolve,promise}}

test('saved five-minute ungrouped scope remains different from the whole library',async()=>{
 let persisted=defaults()
 const api={load:async()=>persisted,save:async(_kb:string,value:LearningPlanSettings)=>(persisted={...value,revision:'rev-1'})}
 const first=createLearningPreferences(api);first.setScope('kb');await first.load()
 await first.save({...defaults(),limit_to_folder:true,time_budget_minutes:5,use_memory:false})
 const reopened=createLearningPreferences(api);reopened.setScope('kb');const restored=await reopened.load()
 assert.equal(restored?.limit_to_folder,true);assert.equal(restored?.folder_id,'');assert.equal(restored?.time_budget_minutes,5);assert.equal(restored?.use_memory,false)
 const nodes=[{slug:'a',folder_id:''},{slug:'b'},{slug:'c',folder_id:'module'}] as LearningNodeView[]
 assert.deepEqual(learningScopeSlugs(nodes,''),['a','b']);assert.deepEqual(learningScopeSlugs(nodes,null),[]);assert.deepEqual(learningScopeSlugs(nodes,'module'),['c'])
})

test('late reads and writes cannot carry a revision into a different KB',async()=>{
 const oldRead=deferred<LearningPlanSettings>(),oldSave=deferred<LearningPlanSettings>()
 const store=createLearningPreferences({load:async kb=>kb==='old'?oldRead.promise:defaults(),save:async()=>oldSave.promise})
 store.setScope('old');const reading=store.load();store.setScope('new');oldRead.resolve({...defaults(),revision:'old'});assert.equal(await reading,null)
 await store.load();const saving=store.save({...defaults(),time_budget_minutes:5});store.setScope('third');oldSave.resolve({...defaults(),revision:'late-write'});assert.equal(await saving,null)
 await assert.rejects(store.save(defaults()),/saved_scope_not_loaded/)
})

test('failed reads never overwrite unknown saved defaults; conflicts preserve the input',async()=>{
 let saves=0
 const missing=createLearningPreferences({load:async()=>{throw Error('offline')},save:async()=>{saves++;return defaults()}})
 missing.setScope('kb');await assert.rejects(missing.load());await assert.rejects(missing.save(defaults()));assert.equal(saves,0)
 const conflict={response:{status:409}}
 const store=createLearningPreferences({load:async()=>({...defaults(),revision:'first'}),save:async()=>{throw conflict}})
 store.setScope('kb');await store.load();const input={...defaults(),time_budget_minutes:5,goal_objectives:['a']};await assert.rejects(store.save(input));assert.equal(input.time_budget_minutes,5);assert.deepEqual(input.goal_objectives,['a']);assert.match(learningPreferenceError(conflict),/其他页面/)
})

test('malformed defaults cannot be presented as a restored learning scope',async()=>{
 const store=createLearningPreferences({load:async()=>({...defaults(),time_budget_minutes:NaN}),save:async()=>defaults()})
 store.setScope('kb');await assert.rejects(store.load(),/invalid_saved_scope/);await assert.rejects(store.save(defaults()),/saved_scope_not_loaded/)
})
