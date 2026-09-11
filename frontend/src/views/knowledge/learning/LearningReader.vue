<template>
 <t-drawer :visible="visible" :header="page?.title || '学习知识点'" :close-btn="true" size="min(700px, 96vw)" :footer="false" @close="emit('close')">
  <p v-if="loading" class="reader-hint" role="status">正在加载知识内容…</p>
  <div v-else-if="error" class="reader-error" role="alert">{{ error }} <button @click="load">重新加载</button></div>
  <template v-else-if="page">
   <div class="reader-top"><button @click="emit('close')">← 返回学习导航</button><button @click="showLearningControls">学习记录与反馈</button><span>{{ page.page_type==='entity'?'实体':'概念' }} · 约 {{ minutes }} 分钟</span><button @click="emit('full',page.slug)">在 Wiki 中查看 ↗</button></div>
   <NodeLearningControls :kb-id="kbId" :slug="page.slug" />
   <p v-if="page.summary" class="reader-summary">{{ page.summary }}</p>
   <div ref="body" class="learning-reader-body" v-html="html" @click="followLink" />
   <div class="reader-end"><span>阅读进度会自动记录，可以直接继续。熟悉或困难反馈可帮助调整推荐。</span><button @click="showLearningControls">查看记录或反馈困难</button><button v-if="nextSlug" class="next" @click="emit('next',nextSlug)">继续：{{ nextTitle || '下一知识点' }} →</button><button v-else @click="emit('close')">返回学习导航</button></div>
  </template>
 </t-drawer>
</template>
<script setup lang="ts">
import {computed,ref,watch,onMounted,onUnmounted,onActivated,onDeactivated,nextTick} from 'vue'
import {marked} from 'marked'
import {getWikiPage,type WikiPage} from '@/api/wiki'
import {recordWikiRead} from '@/api/learning'
import {sanitizeMarkdownHTML,hydrateProtectedFileImages} from '@/utils/security'
import {createWikiReadTracker,readingThresholdMs} from '../wiki/readTracker'
import {useAssistantHostPublisher} from '../assistantHost'
const setAssistantHost=useAssistantHostPublisher(['learning'])
import {notifyReadStatus,notifyLearningUpdated,LEARNING_UPDATED} from './learningEvents'
import NodeLearningControls from './NodeLearningControls.vue'
const props=defineProps<{kbId:string;slug:string;visible:boolean;nextSlug?:string;nextTitle?:string}>()
const emit=defineEmits<{(e:'close'):void;(e:'next',slug:string):void;(e:'full',slug:string):void}>()
const page=ref<WikiPage|null>(null),loading=ref(false),error=ref(''),body=ref<HTMLElement|null>(null)
let generation=0,readingKb=props.kbId
const tracker=createWikiReadTracker((slug,tier)=>{const kb=readingKb;notifyReadStatus(kb,slug,'saving',tier);recordWikiRead(kb,slug,tier).then(result=>{if(result.data?.recorded===false){notifyReadStatus(kb,slug,'disabled',tier);return}notifyReadStatus(kb,slug,'saved',tier);notifyLearningUpdated(kb,slug)}).catch(()=>notifyReadStatus(kb,slug,'error',tier))},()=>readingThresholdMs(page.value?.content||''))
const minutes=computed(()=>Math.min(8,1+Math.floor([...(page.value?.content||'')].length/350)))
const escape=(s:string)=>s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;')
const html=computed(()=>{const content=(page.value?.content||'').replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n/,'').replace(/\[\[([^\]]+)\]\]/g,(_,inner:string)=>{const [slug,...label]=inner.split('|');return `<a href="#" data-slug="${escape(slug!.trim())}">${escape(label.join('|')||slug!.split('/').pop()||slug!)}</a>`});return sanitizeMarkdownHTML(marked.parse(content,{async:false,breaks:true}) as string)})
async function load(){const gen=++generation;tracker.settle();page.value=null;error.value='';if(!props.visible||!props.slug)return;loading.value=true;const kb=props.kbId,slug=props.slug;try{const result:any=await getWikiPage(kb,slug);if(gen!==generation)return;const loaded:WikiPage=result.data??result;if(!loaded?.slug||!['entity','concept'].includes(loaded.page_type))throw Error('not a learning node');page.value=loaded;readingKb=kb;tracker.enter(loaded.slug);publishPage()}catch{if(gen===generation)error.value='知识内容加载失败，请重试或在 Wiki 中查看。'}finally{if(gen===generation)loading.value=false}}
function publishPage(){const p=page.value;if(props.visible&&p)setAssistantHost({scene:'wiki-page',current_page_slug:p.slug,current_page_title:p.title,current_page_type:p.page_type,current_page_excerpt:p.content.slice(0,6000)})}
function followLink(e:MouseEvent){const link=(e.target as HTMLElement).closest<HTMLAnchorElement>('a[data-slug]');if(link?.dataset.slug){e.preventDefault();emit('next',link.dataset.slug)}}
function showLearningControls(){body.value?.closest('.t-drawer__body')?.scrollTo({top:0,behavior:'smooth'})}
function feedbackUpdated(event:Event){const d=(event as CustomEvent).detail;if(props.visible&&d?.kbId===props.kbId&&d?.slug===props.slug&&d?.action==='review')tracker.restart()}
watch(()=>[props.kbId,props.slug,props.visible],()=>{if(!props.visible)setAssistantHost({scene:'learning'});load()},{immediate:true})
watch(html,async()=>{await nextTick();if(body.value){body.value.closest('.t-drawer__body')?.scrollTo({top:0});await hydrateProtectedFileImages(body.value,{mode:'knowledgeBase',kbId:props.kbId})}})
onMounted(()=>{tracker.start();window.addEventListener(LEARNING_UPDATED,feedbackUpdated)});onUnmounted(()=>{generation++;tracker.stop();window.removeEventListener(LEARNING_UPDATED,feedbackUpdated)});onActivated(()=>{tracker.start();if(props.visible&&page.value){tracker.enter(page.value.slug);publishPage()}});onDeactivated(()=>{generation++;tracker.stop()})
</script>
<style scoped>
.reader-top{flex-wrap:wrap;position:sticky;top:-16px;z-index:2;background:var(--td-bg-color-container);padding:8px 0;box-shadow:0 2px 8px #00000008}
.reader-top{display:flex;justify-content:space-between;align-items:center;gap:12px;font-size:12px;color:var(--td-text-color-secondary)}button{border:1px solid var(--td-component-border);border-radius:6px;background:var(--td-bg-color-container);color:var(--td-text-color-primary);padding:7px 10px;cursor:pointer;font-size:12px}.reader-summary{padding:12px 14px;background:var(--td-bg-color-component);border-radius:8px;color:var(--td-text-color-secondary);font-size:13px;line-height:1.8}.learning-reader-body{font-size:14px;line-height:1.85;overflow-wrap:anywhere}.learning-reader-body :deep(h1),.learning-reader-body :deep(h2){font-size:19px;margin:24px 0 12px}.learning-reader-body :deep(h3){font-size:16px;margin:20px 0 10px}.learning-reader-body :deep(a){color:var(--td-brand-color)}.learning-reader-body :deep(img){max-width:100%;height:auto}.learning-reader-body :deep(pre){overflow:auto;padding:12px;border-radius:6px;background:var(--td-bg-color-component);font-size:12px;white-space:pre}.learning-reader-body :deep(table){display:block;max-width:100%;overflow:auto;border-collapse:collapse}.learning-reader-body :deep(td),.learning-reader-body :deep(th){padding:6px 10px;border:1px solid var(--td-component-border)}.reader-end{display:flex;flex-direction:column;gap:12px;padding:18px 0;border-top:1px solid var(--td-component-border);margin-top:24px;font-size:12px;color:var(--td-text-color-secondary)}.next{background:var(--td-brand-color);border-color:var(--td-brand-color);color:white;text-align:left}.reader-hint,.reader-error{font-size:13px;line-height:1.7}.reader-error{color:var(--td-error-color)}
</style>
