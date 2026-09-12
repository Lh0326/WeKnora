import test from 'node:test'
import assert from 'node:assert/strict'
import {completeComponentAction,createComponentReadingClock} from './componentLifecycle'

function deferred<T>() {
  let resolve!: (value:T)=>void
  let reject!: (reason:Error)=>void
  const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no})
  return {promise,resolve,reject}
}

function actionHarness() {
  const request=deferred<{recorded:boolean;level:string}>()
  let scope=0,reader=0
  const initialScope=scope,initialReader=reader
  const state={graph:'unseen',history:0,readerResult:'',refreshes:0}
  const hooks={
    scopeCurrent:()=>scope===initialScope,
    readerCurrent:()=>scope===initialScope&&reader===initialReader,
    applyToReader:(result:{recorded:boolean;level:string})=>{state.readerResult=result.level},
    refresh:async()=>{state.refreshes++;state.graph='learning'},
    changed:()=>{state.history++},
  }
  return {request,state,hooks,close:()=>{reader++},switchScope:()=>{scope++;reader++}}
}

test('a committed read arriving after close updates graph and history without reopening its reader',async()=>{
  const h=actionHarness()
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.close()
  h.request.resolve({recorded:true,level:'learning'})
  assert.equal(await pending,undefined)
  assert.deepEqual(h.state,{graph:'learning',history:1,readerResult:'',refreshes:1})
})

test('an old check arriving after another target opens cannot populate the new target verdict',async()=>{
  const h=actionHarness()
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.close()
  h.state.readerResult='new target: unanswered'
  h.request.resolve({recorded:true,level:'familiar'})
  await pending
  assert.equal(h.state.readerResult,'new target: unanswered')
  assert.equal(h.state.history,1)
  assert.equal(h.state.refreshes,1)
})

for(const change of ['knowledge base','account','tenant']) {
  test(`a ${change} scope switch fences old response, refresh and history writes`,async()=>{
    const h=actionHarness()
    const pending=completeComponentAction(()=>h.request.promise,h.hooks)
    h.switchScope()
    h.request.resolve({recorded:true,level:'learning'})
    assert.equal(await pending,undefined)
    assert.deepEqual(h.state,{graph:'unseen',history:0,readerResult:'',refreshes:0})
  })
}

test('switching away and back does not revive a request from an earlier scope epoch',async()=>{
  const h=actionHarness()
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.switchScope();h.switchScope()
  h.request.resolve({recorded:true,level:'learning'})
  await pending
  assert.equal(h.state.refreshes,0)
  assert.equal(h.state.history,0)
})

test('closing during the follow-up fetch still notifies the parent when that fetch finishes',async()=>{
  const h=actionHarness(),fetched=deferred<void>(),started=deferred<void>()
  h.hooks.refresh=async()=>{started.resolve();await fetched.promise;h.state.refreshes++;h.state.graph='learning'}
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.request.resolve({recorded:true,level:'learning'})
  await started.promise
  h.close()
  fetched.resolve()
  assert.equal(await pending,undefined)
  assert.equal(h.state.history,1)
  assert.equal(h.state.graph,'learning')
})

test('a scope change during follow-up refresh cannot notify the new scope',async()=>{
  const h=actionHarness(),fetched=deferred<void>(),started=deferred<void>()
  h.hooks.refresh=async()=>{started.resolve();await fetched.promise}
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.request.resolve({recorded:true,level:'learning'})
  await started.promise
  h.switchScope()
  fetched.resolve()
  assert.equal(await pending,undefined)
  assert.equal(h.state.history,0)
})

test('a recorded open also invalidates parent history, while opt-out does not pretend to record it',async()=>{
  const h=actionHarness()
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.request.resolve({recorded:true,level:'touched'})
  assert.equal((await pending)?.level,'touched')
  assert.equal(h.state.history,1)
  const stopped=actionHarness()
  const unrecorded=completeComponentAction(()=>stopped.request.promise,stopped.hooks)
  stopped.request.resolve({recorded:false,level:'collection stopped'})
  assert.equal((await unrecorded)?.recorded,false)
  assert.equal(stopped.state.readerResult,'collection stopped')
  assert.equal(stopped.state.refreshes,0)
  assert.equal(stopped.state.history,0)
})

test('failed submission cannot announce saved progress',async()=>{
  const h=actionHarness()
  const pending=completeComponentAction(()=>h.request.promise,h.hooks)
  h.request.reject(new Error('network failed'))
  await assert.rejects(pending,/network failed/)
  assert.deepEqual(h.state,{graph:'unseen',history:0,readerResult:'',refreshes:0})
})

test('difficulty pauses the previous reading clock; a late tick cannot record a reread',()=>{
  const clock=createComponentReadingClock()
  clock.start(3,0)
  assert.equal(clock.tick(1000,true),false)
  assert.equal(clock.tick(2000,true),false)
  clock.pause()
  assert.equal(clock.tick(3000,true),false)
  assert.equal(clock.tick(4000,true),false)
})

test('explicitly continuing after difficulty requires a new full visible reading interval',()=>{
  const clock=createComponentReadingClock()
  clock.start(3,0)
  clock.tick(1000,true);clock.tick(2000,true)
  clock.pause()
  clock.start(3,5000)
  assert.equal(clock.tick(6000,true),false)
  assert.equal(clock.tick(7000,true),false)
  assert.equal(clock.tick(8000,true),true)
  assert.equal(clock.tick(9000,true),false,'only one reading event per interval')
})

test('hidden, submitting and non-reader time does not count toward reading',()=>{
  const clock=createComponentReadingClock()
  clock.start(3,0)
  assert.equal(clock.tick(1000,true),false)
  assert.equal(clock.tick(2000,false),false)
  assert.equal(clock.tick(10000,false),false)
  assert.equal(clock.tick(11000,true),false)
  assert.equal(clock.tick(12000,true),true)
})
