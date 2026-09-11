<template>
  <div class="wa-widget" :class="{ open }">
    <!-- chat card (Zask-style chrome, WeKnora white-green palette) -->
    <transition name="wa-card">
      <div v-if="open" class="wa-card" role="dialog" :aria-label="t('knowledgeEditor.wikiBrowser.assistantName')">
        <div class="wa-card-head">
          <div class="wa-card-title-group">
            <span class="wa-card-title">{{ t('knowledgeEditor.wikiBrowser.assistantTitle') }}</span>
            <span class="wa-card-scene" :title="sceneLabel">
              <svg class="wa-card-scene-icon" viewBox="0 0 16 16" aria-hidden="true">
                <path d="M3 3.5A1.5 1.5 0 0 1 4.5 2h7A1.5 1.5 0 0 1 13 3.5v9A1.5 1.5 0 0 1 11.5 14h-7A1.5 1.5 0 0 1 3 12.5v-9Z" fill="none" stroke="currentColor" stroke-width="1.2" />
                <path d="M5.5 6h5M5.5 8.5h5M5.5 11h3" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
              </svg>
              {{ sceneLabel }}
            </span>
          </div>
          <button class="wa-card-icon-btn" type="button"
            :title="t('knowledgeEditor.wikiBrowser.assistantNewChat')"
            :aria-label="t('knowledgeEditor.wikiBrowser.assistantNewChat')"
            @click="restartSession">
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
              <path d="M13.7 1.8v2.9h-2.9" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <button class="wa-card-icon-btn" type="button" aria-label="close" @click="open = false">
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            </svg>
          </button>
        </div>
        <div class="wa-card-body">
          <div v-if="creating" class="wa-card-loading"><t-loading size="small" /></div>
          <ChatView v-else-if="sessionId" :key="sessionId" :session_id="sessionId"
            agent-id="builtin-wiki-page-assistant"
            :kb-ids="[kbId]" :embedded-mode="true" :host-context="mergedHostContext" />
          <div v-else class="wa-card-error" role="alert"><p>{{ sessionError || '正在连接知识助手…' }}</p><button @click="createSession">重新连接</button></div>
        </div>
      </div>
    </transition>

    <!-- floating ball -->
    <button
      class="wa-ball"
      type="button"
      :class="{ 'is-blinking': blinking }"
      :title="t('knowledgeEditor.wikiBrowser.assistantTooltip')"
      :aria-label="t('knowledgeEditor.wikiBrowser.assistantTooltip')"
      :aria-expanded="open"
      ref="ballRef"
      @click="toggle"
    >
      <WikiAssistantMascot :pupil-dx="pupil.x" :pupil-dy="pupil.y" />
      <span v-if="!open" class="wa-ball-badge">?</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { Loading as TLoading } from 'tdesign-vue-next'
import ChatView from '@/views/chat/index.vue'
import WikiAssistantMascot from './WikiAssistantMascot.vue'
import { pupilOffset } from './assistantGaze'
import {
  createAssistantLearningContext, snapshotFields,
} from '../assistantLearningContext'
import { createSessions, getSession } from '@/api/chat/index'
import {useAuthStore} from '@/stores/auth'
import {LEARNING_UPDATED} from '../learning/learningEvents'
import {learningSessionKey} from '../learning/learningSession'

const props = defineProps<{
  kbId: string
  /** active-interface metadata published by whichever tab is showing */
  hostContext: Record<string, string> | null
}>()

const { t } = useI18n()

// A conversation persists within the same user, tenant and knowledge base.
// Changing that scope must discard in-flight context and restore its own session.
const open = ref(false)
const creating = ref(false)
const sessionError=ref('')
const auth=useAuthStore()
const scopeKey=computed(()=>`${auth.user?.id||''}:${auth.effectiveTenantId||''}:${props.kbId}`)
const learningContext=createAssistantLearningContext()
const learningSession=inject(learningSessionKey,null)
const activeLearningSession=computed(()=>learningSession?.snapshot.value?.kbId===props.kbId?learningSession.snapshot.value:null)
let sessionGeneration=0
const sessionKey=()=>`${SESSION_KEY}:${scopeKey.value}`
const refreshLearning=(force=false)=>learningContext.ensure(props.kbId,scopeKey.value,force,{trajectoryOnly:!!activeLearningSession.value})
const sessionId = ref<string>('')
let sharedSessionId = ''
const SESSION_KEY = 'weknora_kb_assistant_session'

const ballRef = ref<HTMLElement | null>(null)
const pupil = ref({ x: 0, y: 0 })
const blinking = ref(false)

/** 学习快照合并进 host-context：助手回答学习情况/建议类问题时引用真实数据 */
const mergedHostContext = computed<Record<string, string> | null>(() => {
  const base = props.hostContext || {}
  const learning = snapshotFields(learningContext.snapshot.value,base.current_page_slug || null,activeLearningSession.value)
  const merged = { ...base, ...(learning || {}) }
  return Object.keys(merged).length ? merged : null
})
// 快照刷新：卡片打开 / 换库 / 换页（换页只重算 current_node，无请求）；
// 卡片开着每 2 分钟静默续期，新问答落账后的建议即随之更新。
watch(open, (v) => { if (v) refreshLearning() })
watch(()=>!!activeLearningSession.value,()=>refreshLearning(true))
watch(scopeKey,()=>{sessionGeneration++;creating.value=false;sessionId.value='';sharedSessionId='';learningContext.reset(scopeKey.value);refreshLearning();if(open.value)restoreOrCreate()})
let learningTimer = 0
watch(open, (v) => {
  if (v && !learningTimer) {
    learningTimer = window.setInterval(() => refreshLearning(), 120_000)
  } else if (!v && learningTimer) {
    clearInterval(learningTimer)
    learningTimer = 0
  }
})
onBeforeUnmount(() => { if (learningTimer) clearInterval(learningTimer) })

/** 顶栏第二行：告诉用户"助手看得见你所在的界面"，问题可以基于当前页提出 */
const sceneLabel = computed(() => {
  const scene = props.hostContext?.scene || ''
  const title = props.hostContext?.current_page_title || ''
  if (scene === 'wiki-page' && title) return t('knowledgeEditor.wikiBrowser.assistantSceneWikiPage', { title })
  if (scene === 'wiki' || scene === 'graph') return t('knowledgeEditor.wikiBrowser.assistantSceneWiki')
  if (scene === 'learning') return t('knowledgeEditor.wikiBrowser.assistantSceneLearning')
  if (scene === 'documents') return t('knowledgeEditor.wikiBrowser.assistantSceneDocuments')
  return t('knowledgeEditor.wikiBrowser.assistantSceneKb')
})

let rafId = 0
let pointerX = 0
let pointerY = 0
let blinkTimer: ReturnType<typeof setTimeout> | null = null

const prefersReducedMotion =
  typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

function onPointerMove(e: MouseEvent) {
  pointerX = e.clientX
  pointerY = e.clientY
  if (rafId) return
  rafId = requestAnimationFrame(() => {
    rafId = 0
    if (!ballRef.value) return
    const rect = ballRef.value.getBoundingClientRect()
    pupil.value = pupilOffset(
      { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 },
      { x: pointerX, y: pointerY },
      2.6,
      14,
    )
  })
}

function scheduleBlink() {
  const delay = 2600 + Math.random() * 3200
  blinkTimer = setTimeout(() => {
    blinking.value = true
    setTimeout(() => { blinking.value = false }, 220)
    scheduleBlink()
  }, delay)
}

async function createSession() {
  if (creating.value) return
  creating.value = true
  sessionError.value=''
  const gen=++sessionGeneration
  try {
    const res = await createSessions({})
    if(gen!==sessionGeneration)return
    const id = (res as any)?.data?.id ?? (res as any)?.id
    if (!id) throw new Error('no session id')
    sessionId.value = id
    sharedSessionId = id
    // 助手会话自治：全局仅此一条（侧栏同步自服务端会话列表，会显示为
    // 单独的一条"新会话"），仅"新对话"按钮会更换——上下文长期保留。
    try { localStorage.setItem(sessionKey(), id) } catch { /* private mode */ }
  } catch (e) {
    if(gen!==sessionGeneration)return
    sessionError.value='知识助手连接失败，请检查服务后重试。'
    console.error('[WikiAssistant] session create failed:', e)
    sessionId.value = '' // shows the retry affordance
  } finally {
    if(gen===sessionGeneration)creating.value = false
  }
}

async function restoreSession(): Promise<boolean> {
  const gen=sessionGeneration
  try { sharedSessionId=localStorage.getItem(sessionKey())||'' } catch { return false }
  if (!sharedSessionId) return false
  // 自愈：缓存指向的会话可能已被删除（侧栏清理/数据重置），失效即
  // 丢弃缓存，让调用方自动新建——绝不把用户卡在 404 上。
  try {
    const res: any = await getSession(sharedSessionId)
    const id = res?.data?.id ?? res?.id
    if (id !== sharedSessionId) throw new Error('session gone')
    if(gen!==sessionGeneration)return false
    sessionId.value = sharedSessionId
    return true
  } catch {
    if(gen!==sessionGeneration)return false
    sharedSessionId = ''
    sessionId.value = ''
    try { localStorage.removeItem(sessionKey()) } catch { /* noop */ }
    return false
  }
}

/** 新对话（用户主动清除上下文）：换一条全新全局会话，旧会话留在历史 */
function restartSession() {
  refreshLearning(true)
  sessionId.value = ''
  sharedSessionId = ''
  try { localStorage.removeItem(sessionKey()) } catch { /* noop */ }
  createSession()
}

async function restoreOrCreate(){const gen=sessionGeneration;const restored=await restoreSession();if(!restored&&gen===sessionGeneration&&!sessionId.value)await createSession()}
function onLearningUpdate(e:Event){if((e as CustomEvent).detail?.kbId===props.kbId)refreshLearning(true)}
async function toggle() {
  open.value = !open.value
  if (open.value && !sessionId.value) {
    await restoreOrCreate()
  }
}

onMounted(() => {
  window.addEventListener(LEARNING_UPDATED,onLearningUpdate)
  restoreSession()
  // 预热学习快照：不等卡片打开——首条提问发生在打开后的几秒内，
  // 按需拉取会输给打字速度（实测 0.2s 竞态），挂载即取则必然就绪。
  refreshLearning()
  if (!prefersReducedMotion) {
    window.addEventListener('mousemove', onPointerMove, { passive: true })
    scheduleBlink()
  }
})

onBeforeUnmount(() => {
  sessionGeneration++;learningContext.reset('')
  window.removeEventListener(LEARNING_UPDATED,onLearningUpdate)
  window.removeEventListener('mousemove', onPointerMove)
  if (rafId) cancelAnimationFrame(rafId)
  if (blinkTimer) clearTimeout(blinkTimer)
})
</script>

<style scoped>
.wa-widget { position: fixed; right: 20px; bottom: 20px; z-index: 2000; }

/* ---- floating ball ---- */
.wa-ball {
  position: relative;
  width: 54px; height: 54px;
  border-radius: 50%;
  border: none;
  padding: 3px;
  background: var(--td-bg-color-container, #fff);
  box-shadow: var(--td-shadow-2);
  cursor: pointer;
  transition: transform 0.18s ease, box-shadow 0.18s ease;
}
.wa-ball > .mascot { animation: wa-breathe 4.6s ease-in-out infinite; }
.wa-widget.open .wa-ball > .mascot { animation: none; }
.wa-ball:hover { transform: scale(1.08); box-shadow: var(--td-shadow-3); }
.wa-ball:focus-visible { outline: 2px solid var(--td-brand-color, #07c05f); outline-offset: 2px; }
.wa-widget.open .wa-ball { animation: none; transform: scale(1.04); }
@keyframes wa-breathe {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.045); }
}
.wa-ball-badge {
  position: absolute; top: -3px; right: -3px;
  min-width: 16px; height: 16px; line-height: 16px;
  border-radius: 999px;
  background: var(--td-brand-color, #07c05f); color: #fff;
  font-size: 11px; font-weight: 700; text-align: center;
  box-shadow: 0 0 0 2px var(--td-bg-color-container, #fff);
}

/* ---- chat card: Zask-style chrome in WeKnora white-green ---- */
.wa-card {
  position: absolute; right: 0; bottom: 64px;
  width: min(420px, calc(100vw - 48px));
  height: min(560px, 68vh);
  display: flex; flex-direction: column;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-stroke, rgba(0, 0, 0, 0.06));
  border-radius: 16px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.14), 0 2px 8px rgba(0, 0, 0, 0.06);
  overflow: hidden;
}
.wa-card-head {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 12px 10px 14px;
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
  background: linear-gradient(180deg, rgba(7, 192, 95, 0.05), transparent 70%);
  flex-shrink: 0;
}
.wa-card-title-group { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
.wa-card-title { font-size: 13px; font-weight: 700; color: var(--td-text-color-primary, #333); line-height: 1.3; }
.wa-card-scene {
  font-size: 11px; color: var(--td-brand-color, #07c05f);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  display: flex; align-items: center; gap: 3px;
}
.wa-card-scene-icon { width: 11px; height: 11px; flex-shrink: 0; }
.wa-card-icon-btn {
  border: none; background: none; cursor: pointer;
  width: 28px; height: 28px; flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
  border-radius: 8px; color: var(--td-text-color-secondary, #666);
  transition: background 0.15s ease, color 0.15s ease;
}
.wa-card-icon-btn svg { width: 15px; height: 15px; }
.wa-card-icon-btn:hover { background: var(--td-brand-color-light, rgba(7, 192, 95, 0.1)); color: var(--td-brand-color, #07c05f); }
.wa-card-icon-btn:focus-visible { outline: 2px solid var(--td-brand-color, #07c05f); outline-offset: 1px; }
.wa-card-body { flex: 1; min-height: 0; position: relative; }
.wa-card-loading, .wa-card-error {
  position: absolute; inset: 0;
  display: flex; align-items: center; justify-content: center;
  font-size: 12px; color: var(--td-text-color-placeholder, #999);
}
.wa-card-error { cursor: pointer; }
.wa-card-error:hover { color: var(--td-brand-color, #07c05f); }

/* ---- embedded ChatView: template-grade message design (WeKnora 白绿).
     用户=右侧绿色实心气泡（右下圆缺口）；助手=左侧形象头像+无气泡正文；
     markdown 层级化 + 深色代码块；长行换行（宽度自适应，无横向滚动）。 ---- */
.wa-card :deep(.chat.is-embedded) { height: 100%; }
.wa-card :deep(.chat_scroll_box) { overflow-x: hidden; }
/* msg_list 的嵌入样式自带 margin-right:-32px 补偿 hack（为修复助手抽屉设计），
   在本卡片里会把内容推出右边界——归零后 16px 左右对称留白才成立 */
.wa-card :deep(.msg_list.is-embedded) {
  padding: 14px 16px 10px;
  margin-right: 0 !important;
  width: auto;
  overflow-x: hidden;
  gap: 0;
}
/* 卡内任何元素都不允许出现横向滚动：内容一律换行/收缩 */
.wa-card :deep(*) { max-width: 100%; }
.wa-card :deep([class*='code']), .wa-card :deep(pre), .wa-card :deep(code) { overflow-x: hidden !important; }
.wa-card :deep(.msg-item-wrapper) { margin-bottom: 16px; }

/* 用户消息：绿色气泡、白字、右下角小圆缺口 */
.wa-card :deep(.user_msg) {
  max-width: 86%;
  padding: 8px 12px;
  border-radius: 12px 12px 4px 12px;
  background: var(--td-brand-color, #07c05f);
  color: #fff;
  font-size: 13px;
  line-height: 1.6;
}

/* 助手消息：无头像、无气泡，整宽正文（左右留白由消息列表统一给出） */
.wa-card :deep(.bot_msg.is-embedded) {
  font-size: 13px;
  line-height: 1.7;
  color: var(--td-text-color-primary, #333);
}

/* markdown 排版层级（模板风格） */
.wa-card :deep(.markdown-content),
.wa-card :deep(.markdown-content *) { max-width: 100%; }
.wa-card :deep(.markdown-content) { font-size: 13px; line-height: 1.7; }
.wa-card :deep(.markdown-content h1),
.wa-card :deep(.markdown-content h2),
.wa-card :deep(.markdown-content h3),
.wa-card :deep(.markdown-content h4) {
  font-weight: 700;
  color: var(--td-text-color-primary, #333);
  margin: 0.7em 0 0.35em;
  line-height: 1.4;
}
.wa-card :deep(.markdown-content h1) { font-size: 15px; }
.wa-card :deep(.markdown-content h2) { font-size: 14px; }
.wa-card :deep(.markdown-content h3),
.wa-card :deep(.markdown-content h4) { font-size: 13px; }
.wa-card :deep(.markdown-content p) { margin: 0.4em 0; }
.wa-card :deep(.markdown-content ul),
.wa-card :deep(.markdown-content ol) { margin: 0.4em 0; padding-left: 1.35em; }
.wa-card :deep(.markdown-content li) { margin: 0.22em 0; }
.wa-card :deep(.markdown-content ul) { list-style: disc; }
.wa-card :deep(.markdown-content ol) { list-style: decimal; }
.wa-card :deep(.markdown-content strong) { font-weight: 700; }
.wa-card :deep(.markdown-content a) { color: var(--td-brand-color, #07c05f); }
.wa-card :deep(.markdown-content blockquote) {
  margin: 0.5em 0;
  padding: 4px 10px;
  border-left: 3px solid var(--td-brand-color, #07c05f);
  background: rgba(7, 192, 95, 0.05);
  border-radius: 0 6px 6px 0;
  color: var(--td-text-color-secondary, #555);
}

/* 深色代码块（模板风） */
.wa-card :deep(.markdown-content pre),
.wa-card :deep(.markdown-content code) {
  white-space: pre-wrap;
  word-break: break-word;
  overflow-wrap: anywhere;
}
.wa-card :deep(.markdown-content pre) {
  overflow-x: hidden;
  background: #1e2622 !important;
  color: #d9e5df;
  border-radius: 8px !important;
  padding: 12px !important;
  margin: 0.55em 0;
  font-size: 12.5px;
  line-height: 1.6;
}
.wa-card :deep(.markdown-content pre code) {
  background: transparent !important;
  color: inherit;
  padding: 0;
  font-size: inherit;
}
.wa-card :deep(.markdown-content :not(pre) > code) {
  background: rgba(7, 192, 95, 0.09);
  color: #04744a;
  border-radius: 4px;
  padding: 1px 5px;
  font-size: 12px;
}
.wa-card :deep(.markdown-content table) { display: block; max-width: 100%; }
.wa-card :deep(.markdown-content img) { border-radius: 8px; max-width: 100%; }

.wa-card :deep(.input-container.is-embedded) {
  padding: 8px 12px 12px;
  border-top: 1px solid var(--td-component-stroke, #f0f0f0);
}

/* card enter/leave: rise from the ball with a slight spring */
.wa-card-enter-active { transition: opacity 0.22s cubic-bezier(0.22, 0.61, 0.36, 1), transform 0.22s cubic-bezier(0.22, 0.61, 0.36, 1); }
.wa-card-leave-active { transition: opacity 0.16s ease, transform 0.16s ease; }
.wa-card-enter-from, .wa-card-leave-to {
  opacity: 0;
  transform: translateY(14px) scale(0.96);
  transform-origin: bottom right;
}

@media (prefers-reduced-motion: reduce) {
  .wa-ball { animation: none; }
  .wa-card-enter-active, .wa-card-leave-active { transition: none; }
}
</style>
