import test from 'node:test'
import assert from 'node:assert/strict'
import {recallDue,recallSummary,createRecallFeedback} from './recallFeedback'
import type {LearningReviewStatus,LearningReviewInput} from '@/api/learning/objectives'
const state=():LearningReviewStatus=>({revision:'r1',content_version:'v1',content_changed:false,policy_version:'sm2-recall-v1',active:true,due:false,due_at:'2030-01-02T00:00:00Z',interval_days:1,repetitions:1,ease:2.5,early_practice:false})

test('only opted-in schedules become due; source changes require recall without changing verified state',()=>{
 assert.equal(recallDue(),false)
 assert.equal(recallDue(state(),Date.parse('2030-01-01')),false)
 assert.equal(recallDue(state(),Date.parse('2030-01-02')),true)
 assert.equal(recallDue({...state(),content_changed:true}),true)
 assert.equal(recallDue({...state(),active:false,content_changed:true,due:true}),false)
 assert.match(recallSummary({...state(),content_changed:true}),/材料已更新/)
 assert.match(recallSummary({...state(),active:false}),/已暂停/)
})

test('lost response retries the identical request even after status changes',async()=>{
 const inputs:LearningReviewInput[]=[];let count=0
 const client=createRecallFeedback(async(_kb,input)=>{inputs.push(input);if(++count===1)throw Error('lost response');return {...state(),revision:'saved'}},()=> 'request-1')
 client.setTarget('kb','a');const before=state()
 await assert.rejects(client.send('good',before))
 await assert.rejects(client.send('again',before),/unresolved/)
 const after=await client.send('good',{...before,revision:'other-tab'})
 assert.equal(after?.revision,'saved');assert.deepEqual(inputs[0],inputs[1]);assert.equal(before.revision,'r1')
})

test('late responses are discarded after a knowledge node or account owner changes',async()=>{
 let resolve!:(value:LearningReviewStatus)=>void
 const client=createRecallFeedback(async()=>new Promise(r=>resolve=r),()=> 'request')
 client.setTarget('old-kb','a');const old=client.send('good',state())
 assert.equal(await client.send('again',state()),null)
 client.setTarget('new-kb','b');resolve(state());assert.equal(await old,null)
 const next=client.send('enroll');client.discard();resolve(state());assert.equal(await next,null)
})

test('after a conflict only reloading/discarding starts a fresh submission',async()=>{
 const inputs:LearningReviewInput[]=[];let n=0
 const client=createRecallFeedback(async(_kb,input)=>{inputs.push(input);if(++n===1)throw {response:{status:409}};return state()},()=> `request-${n}`)
 client.setTarget('kb','a');await assert.rejects(client.send('good',state()));client.discard()
 await client.send('again',{...state(),revision:'latest'})
 assert.equal(inputs[1].revision,'latest');assert.notEqual(inputs[0].request_id,inputs[1].request_id)
})
