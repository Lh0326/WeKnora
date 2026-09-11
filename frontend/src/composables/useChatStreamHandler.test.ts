import test from 'node:test'
import assert from 'node:assert/strict'
import {computed,createSSRApp,isReactive,nextTick,reactive,ref} from 'vue'
import {renderToString} from '@vue/server-renderer'
import {createI18n} from 'vue-i18n'
import {useChatStreamHandler,type ChatMessage} from './useChatStreamHandler'

async function fixture(agentMode=false){
 const messages=reactive<ChatMessage[]>([])
 let handler!:ReturnType<typeof useChatStreamHandler>
 const app=createSSRApp({setup(){handler=useChatStreamHandler({messagesList:messages,loading:ref(false),isReplying:ref(false),currentAssistantMessageId:ref(''),fullContent:ref(''),isAgentStreamSession:()=>agentMode,scrollToBottom:()=>{},preserveIncompleteStreamReactive:true});return()=>null}})
 app.use(createI18n({legacy:false,locale:'en',messages:{en:{}}}))
 await renderToString(app)
 return {messages,handler}
}

test('resumed RAG answers remain reactive after incomplete history hydration',async()=>{
 const {messages,handler}=await fixture()
 await handler.handleMsgList([{id:'assistant',role:'assistant',content:'',is_completed:false}])
 handler.processStreamChunk({response_type:'agent_query',id:'assistant',assistant_message_id:'assistant'})
 const row=messages[0]!
 const rendered=computed(()=>((row.agentEventStream||[]) as ChatMessage[]).filter(e=>e.type==='answer').map(e=>e.content).join(''))
 assert.equal(rendered.value,'')
 handler.processStreamChunk({response_type:'answer',id:'assistant',content:'第一句',data:{event_id:'answer'}})
 await nextTick()
 assert.equal(isReactive(row.agentEventStream),true)
 assert.equal(rendered.value,'第一句')
 handler.processStreamChunk({response_type:'answer',id:'assistant',content:'。第二句',data:{event_id:'answer'},done:true})
 handler.processStreamChunk({response_type:'complete',id:'assistant',done:true})
 await nextTick()
 assert.equal(rendered.value,'第一句。第二句')
 assert.equal(row.is_completed,true)
})

test('fresh RAG answer follows retrieval events without requiring reload',async()=>{
 const {messages,handler}=await fixture()
 handler.processStreamChunk({response_type:'agent_query',id:'request',assistant_message_id:'assistant'})
 const row=messages[0]!
 const rendered=computed(()=>((row.agentEventStream||[]) as ChatMessage[]).filter(e=>e.type==='answer').map(e=>e.content).join(''))
 handler.processStreamChunk({response_type:'tool_call',id:'request',data:{tool_call_id:'search',tool_name:'knowledge_search'}})
 assert.equal(rendered.value,'')
 handler.processStreamChunk({response_type:'answer',id:'request',content:'18 个知识点',data:{event_id:'answer'}})
 handler.processStreamChunk({response_type:'answer',id:'request',content:'，已会 0 个。',data:{event_id:'answer'},done:true})
 handler.processStreamChunk({response_type:'complete',id:'request',done:true})
 await nextTick()
 assert.equal(rendered.value,'18 个知识点，已会 0 个。')
 assert.equal(row.content,rendered.value)
})

test('initial history binds the pending user bubble without duplicating it',async()=>{
 const {messages,handler}=await fixture()
 messages.push({role:'user',content:'问题'})
 await handler.handleMsgList([{id:'user',role:'user',content:'[Host context]\nscene: learning\n[/Host context]\n\n问题',is_completed:true}])
 assert.equal(messages.length,1)
 assert.equal(messages[0]!.id,'user')
})

test('late RAG retrieval progress must not retract the final answer',async()=>{
 const {messages,handler}=await fixture()
 handler.processStreamChunk({response_type:'agent_query',id:'request',assistant_message_id:'assistant'})
 handler.processStreamChunk({response_type:'answer',id:'request',content:'度量机制，5 分钟，事件溯源度量。',data:{event_id:'answer'},done:true})
 // Pipeline progress is independently delivered and can arrive after tokens.
 handler.processStreamChunk({response_type:'tool_call',id:'request',data:{tool_call_id:'search',tool_name:'knowledge_search'}})
 handler.processStreamChunk({response_type:'tool_result',id:'request',data:{tool_call_id:'search',success:true}})
 handler.processStreamChunk({response_type:'complete',id:'request',done:true})
 const row=messages[0]!
 const answers=(row.agentEventStream as ChatMessage[]).filter(e=>e.type==='answer'&&!e.superseded)
 assert.equal(answers.length,1)
 assert.equal(answers[0]!.content,'度量机制，5 分钟，事件溯源度量。')
 assert.equal(row.content,answers[0]!.content)
 assert.equal(row.is_completed,true)
})

test('agent tool rounds still retract their optimistic preamble',async()=>{
 const {messages,handler}=await fixture(true)
 handler.processStreamChunk({response_type:'agent_query',id:'request',assistant_message_id:'assistant'})
 handler.processStreamChunk({response_type:'answer',id:'request',content:'先检查资料。',data:{event_id:'preamble'}})
 handler.processStreamChunk({response_type:'tool_call',id:'request',data:{tool_call_id:'search',tool_name:'knowledge_search'}})
 handler.processStreamChunk({response_type:'answer',id:'request',content:'最终回答。',data:{event_id:'final'},done:true})
 const row=messages[0]!
 assert.equal((row.agentEventStream as ChatMessage[]).find(e=>e.event_id==='preamble')!.superseded,true)
 assert.equal(row.content,'最终回答。')
})

test('resume from offset zero replaces the saved partial answer without duplication',async()=>{
 const {messages,handler}=await fixture()
 await handler.handleMsgList([{id:'assistant',request_id:'request',role:'assistant',content:'度量机制',is_completed:false,agent_steps:[{iteration:1,tool_calls:[{id:'search',name:'knowledge_search',result:{success:true}}]}]}])
 assert.equal(messages[0]!.content,'度量机制')
 handler.processStreamChunk({response_type:'tool_call',id:'request',data:{tool_call_id:'search',tool_name:'knowledge_search'}})
 handler.processStreamChunk({response_type:'answer',id:'request',content:'度量机制',data:{event_id:'answer'}})
 handler.processStreamChunk({response_type:'answer',id:'request',content:'，5 分钟。',data:{event_id:'answer'},done:true})
 handler.processStreamChunk({response_type:'complete',id:'request',done:true})
 assert.equal(messages[0]!.content,'度量机制，5 分钟。')
 const answers=(messages[0]!.agentEventStream as ChatMessage[]).filter(e=>e.type==='answer')
 assert.equal(answers.length,1)
 assert.equal(answers[0]!.content,messages[0]!.content)
})
