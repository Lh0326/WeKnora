<template>
  <div class="learning-tab">
    <!-- 停采全局横幅：一进页签就能看到，避免"答了题却不亮"的困惑 -->
    <div v-if="collectDisabled && !initialLoading" class="collect-banner">
      {{ $t('knowledgeEditor.learningTab.collectOnNote') }}
    </div>
    <!-- 加载态 / 错误态：整幅占位（错误态给重试入口，绝不伪装成"没有数据"） -->
    <div v-if="initialLoading" class="sky-state">
      <t-loading size="large" />
    </div>
    <div v-else-if="loadFailed" class="sky-state">
      <div class="error-text">{{ $t('knowledgeEditor.learningTab.loadError') }}</div>
      <t-button size="small" variant="outline" @click="refresh()">{{ $t('knowledgeEditor.learningTab.retry') }}</t-button>
    </div>

    <!-- 星图与学习导航同时可见，共用个人状态与推荐路径。 -->
    <div v-else class="sky">
      <section class="sky-chart" aria-label="个人知识星图">
        <div class="sky-head">
          <div class="ts-title">知识星图</div>
          <div class="ts-line">从知识关联与学习状态中，找到下一步。</div>
        </div>
        <div class="sky-map">
          <LearningConstellation v-if="hasNodes" :nodes="constellationNodes" :edges="zoneEdges" :zones="zoneAxes"
            :highlight-zone="hasComponents ? null : highlightZone" verification :components="hasComponents" :estimated="hasComponents || objView?.nodes?.some(n=>n.estimate)" @open="openPage" />
          <div v-else class="empty-nodes"><div class="empty-nodes-orb"></div><div class="empty-nodes-text">{{ $t('knowledgeEditor.learningTab.noNodes') }}</div></div>
        </div>
      </section>
      <div class="sky-side">
        <div class="side-scroll">
          <ComponentWorkspace ref="componentWorkspace" :kb-id="knowledgeBaseId" @view="componentView=$event" @source="openWikiPage" @changed="refresh()" />
          <ObjectiveWorkspace v-if="!hasComponents" ref="objectiveWorkspace" :kb-id="knowledgeBaseId"
            @open="openPage" @quiz="(slug, objective) => startQuiz({slug,title:slug,reason:'',has_quiz:true}, objective)"
            @view="objView=$event" @plan="currentPath=$event" @highlight="highlightZone=$event" />
        </div>
        <div class="side-foot">
          <t-button size="small" variant="outline" @click="timelineDrawer=true">{{ $t('knowledgeEditor.learningTab.timelineTitle') }}<span v-if="todayTimelineCount>0" class="foot-badge">{{ todayTimelineCount }}</span></t-button>
          <t-button size="small" variant="outline" @click="profileDrawer=true">{{ $t('knowledgeEditor.learningTab.profileTitle') }}</t-button>
        </div>
      </div>
    </div>
    <LearningReader :kb-id="knowledgeBaseId" :slug="readerSlug" :visible="readerOpen" :next-slug="readerNext?.slug" :next-title="readerNext?.title" @close="readerOpen=false" @next="openPage" @full="openWikiPage" />
    <!-- 测验答题抽屉：放大作答，不打断单屏星空布局 -->
    <t-drawer :visible="quiz.active" size="600px" :footer="false"
      :header="$t('knowledgeEditor.learningTab.quizTitle', { slug: quiz.title })" @close="closeQuiz()">
      <div v-if="collectDisabled" class="collect-note">
        {{ $t('knowledgeEditor.learningTab.optedOutNote') }}
      </div>
      <div v-if="quiz.loading" class="section-empty">{{ $t('common.loading') }}</div>
      <template v-else-if="quizCurrent">
        <p class="collect-note">{{ quizCurrent.mode === "verification" ? "独立验证候选：按题目规定条件完成" : "练习题：不增加独立验证证据" }} · {{ quizCurrent.assistance_mode === "open_book" ? "开卷" : "闭卷" }}</p>
        <label><input v-model="quizHelped" type="checkbox" :disabled="quiz.result!==null" />本次使用了助手或额外帮助（仅作练习）</label>
        <div class="quiz-question">{{ quizCurrent.question }}</div>
        <div class="quiz-options">
          <div v-for="(text, key) in quizCurrent.options" :key="key" class="quiz-option"
            :class="{ selected: quiz.chosen === key, correct: quiz.result !== null && key === quiz.result.correct_key, wrong: quiz.result !== null && quiz.chosen === key && !quiz.result.correct }"
            @click="chooseOption(key)">
            <span class="option-key">{{ key }}</span>
            <span class="option-text">{{ text }}</span>
          </div>
          <!-- 元认知出口：「不确定」按申报处理——零权重、不罚分，看解析后再来 -->
          <div class="quiz-option unsure" :class="{ selected: quiz.chosen === 'E' }" @click="chooseOption('E')">
            <span class="option-key">E</span>
            <span class="option-text">{{ $t('knowledgeEditor.learningTab.unsureOption') }}</span>
          </div>
        </div>
        <div class="quiz-footer">
          <t-button size="small" :disabled="!quiz.chosen || quiz.result !== null" @click="submitAnswer">{{ $t('knowledgeEditor.learningTab.submitAnswer') }}</t-button>
          <span v-if="quiz.items.length > 1" class="quiz-progress">{{ quiz.index + 1 }} / {{ quiz.items.length }}</span>
          <t-button v-if="quiz.result !== null && quiz.index < quiz.items.length - 1" size="small" variant="outline" @click="nextQuestion">{{ $t('knowledgeEditor.learningTab.nextQuestion') }}</t-button>
          <span class="quiz-kbd-hint">{{ $t('knowledgeEditor.learningTab.kbdHint') }}</span>
        </div>
        <div v-if="quiz.result !== null" class="quiz-result" :class="{ ok: quiz.result.correct && !quiz.result.unsure, unsure: quiz.result.unsure }">
          <div class="result-line">
            {{ quiz.result.unsure
              ? $t('knowledgeEditor.learningTab.answerUnsure')
              : quiz.result.correct ? $t('knowledgeEditor.learningTab.answerCorrect') : $t('knowledgeEditor.learningTab.answerWrong') }}
          </div>
          <div v-if="quiz.result.unsure" class="result-schedule">{{ $t('knowledgeEditor.learningTab.unsureNote') }}</div>
          <p>{{ quiz.result.eligible ? "本次计入验证证据，目标状态以目标面板为准。" : "本次未计入独立验证证据，可查看解析继续练习。" }}</p>
          <div v-if="!quizCurrent.objective_id && pEffMoveText" class="result-level">{{ pEffMoveText }}</div>
          <div v-if="!quizCurrent.objective_id && levelChangeText" class="result-level">{{ levelChangeText }}</div>
          <div v-if="!quizCurrent.objective_id && reviewScheduleText" class="result-schedule">{{ reviewScheduleText }}</div>
          <div class="result-explanation">{{ quiz.result.explanation }}</div>
          <div v-if="currentSourceDocs.length" class="result-refs">
            <span class="result-refs-label">{{ $t('knowledgeEditor.learningTab.evidenceSource') }}</span>
            <a v-for="doc in currentSourceDocs" :key="doc.knowledge_id" href="#"
              class="result-ref-link" :title="$t('knowledgeEditor.learningTab.evidenceSourceTip')"
              @click.prevent="emit('open-source-doc', doc.knowledge_id)">
              {{ doc.title || doc.knowledge_id }}<span v-if="doc.chunk_count > 1" class="result-ref-count">×{{ doc.chunk_count }}</span>
            </a>
          </div>
          <div v-else-if="quiz.result.chunk_refs && quiz.result.chunk_refs.length" class="result-refs">
            {{ $t('knowledgeEditor.learningTab.evidenceRefs', { n: quiz.result.chunk_refs.length }) }}
          </div>
        </div>
      </template>
      <div v-else class="section-empty">{{ $t('knowledgeEditor.learningTab.quizEmpty') }}</div>
    </t-drawer>

    <!-- 点亮时间线：右栏底部入口点击后放大查看 -->
    <t-drawer v-model:visible="timelineDrawer" size="820px" :footer="false"
      :header="$t('knowledgeEditor.learningTab.timelineTitle')">
      <div class="tl-filters">
        <button v-for="f in timelineFilters" :key="f.key" class="tl-filter" :class="{ active: timelineFilter === f.key }"
          :aria-pressed="timelineFilter === f.key"
          @click="timelineFilter = f.key">{{ f.label }}</button>
      </div>
      <div v-if="initialLoading" class="section-empty"><t-loading size="small" /></div>
      <div v-else-if="loadFailed" class="section-empty">
        <span>{{ $t('knowledgeEditor.learningTab.loadError') }}</span>
        <t-button size="small" variant="outline" @click="refresh()">{{ $t('knowledgeEditor.learningTab.retry') }}</t-button>
      </div>
      <!-- 筛选×分页的组合假空态：目标事件可能在未加载的后续页里——如实提示
       「已加载页内无匹配」并给加载更多入口，而不是断言"没有记录" -->
      <div v-else-if="!filteredTimeline.length" class="section-empty">
        <template v-if="hasActiveFilters && timeline.length < timelineTotal">
          <span>{{ $t('knowledgeEditor.learningTab.tlFilteredEmpty') }}</span>
          <t-button size="small" variant="text" :loading="timelineLoading" @click="loadMoreTimeline()">
            {{ $t('knowledgeEditor.learningTab.tlLoadMore') }}
          </t-button>
        </template>
        <template v-else>{{ $t('knowledgeEditor.learningTab.timelineEmpty') }}</template>
      </div>
      <template v-else>
        <div class="tl-node-filters">
          <button v-for="c in timelineNodesVisible" :key="c.slug" class="tl-node-chip"
            :class="{ active: selectedNodeSlugs.has(c.slug) }"
            :aria-pressed="selectedNodeSlugs.has(c.slug)"
            :title="$t('knowledgeEditor.learningTab.tlNodeChipTip', { n: c.count })"
            @click="toggleNodeFilter(c.slug)">{{ c.title }}<span class="tl-node-count">{{ c.count }}</span></button>
          <button v-if="timelineNodeChips.length > 8" class="tl-node-chip more"
            @click="timelineNodesCollapsed = !timelineNodesCollapsed">
            {{ timelineNodesCollapsed ? $t('knowledgeEditor.learningTab.tlMoreNodes', { n: timelineNodeChips.length }) : $t('knowledgeEditor.learningTab.tlFewerNodes') }}
          </button>
        </div>
        <div class="timeline-box">
          <div class="timeline-list">
            <template v-for="group in groupedTimeline" :key="group.label">
              <div class="tl-group-header">{{ group.label }}</div>
              <div v-for="item in group.items" :key="item.key" class="timeline-item"
                :title="item.event_type === 're_ask' ? $t('knowledgeEditor.learningTab.reAskHint') : formatTime(item.occurred_at)">
                <span class="tl-dot" :class="`ev-${item.event_type}`"></span>
                <span class="tl-type">{{ eventText(item.event_type) }}</span>
                <span v-if="item.page_type" class="tl-badge" :class="`tl-badge-${item.page_type}`">{{ pageTypeText(item.page_type) }}</span>
                <span class="tl-slug">{{ item.title || item.slug }}</span>
                <span class="tl-time">{{ formatTime(item.occurred_at) }}</span>
              </div>
            </template>
            <!-- 触底自动翻页哨兵：只在真正请求下一页时才显示转圈，避免"永远在加载"的歧义 -->
            <div v-show="timeline.length < timelineTotal" ref="timelineEndRef" class="timeline-end">
              <t-loading v-if="timelineLoading" size="small" />
            </div>
            <!-- 明确的到底信号：用户不再需要猜是否还有下一页 -->
            <div v-if="timeline.length > 0 && timeline.length >= timelineTotal" class="timeline-all-loaded">
              {{ $t('knowledgeEditor.learningTab.timelineEnd', { n: timelineTotal }) }}
            </div>
          </div>
        </div>
      </template>
    </t-drawer>

    <!-- 画像管理：右栏底部入口点击后放大查看。主区加载失败时如实报错，
         不拿 0 节点/0 事件的空摘要伪装"没有数据" -->
    <t-drawer v-model:visible="profileDrawer" size="560px" :footer="false"
      :header="$t('knowledgeEditor.learningTab.profileTitle')">
      <div v-if="loadFailed" class="section-empty">
        <span>{{ $t('knowledgeEditor.learningTab.loadError') }}</span>
        <t-button size="small" variant="outline" @click="refresh()">{{ $t('knowledgeEditor.learningTab.retry') }}</t-button>
      </div>
      <template v-else>
        <div class="profile-scope-note">{{ $t('knowledgeEditor.learningTab.profileScopeNote') }}</div>
        <div class="profile-summary">{{ profileSummary }}</div>
        <div v-if="collectDisabled" class="collect-note">{{ $t('knowledgeEditor.learningTab.collectOnNote') }}</div>
        <!-- 停采的数据去向说明：Google Activity Controls 模式——pause 不删历史 -->
        <div v-if="collectDisabled && timelineTotal > 0" class="kept-note">
          {{ $t('knowledgeEditor.learningTab.collectKeptNote', { n: timelineTotal }) }}
        </div>
      </template>
      <div class="profile-row">
        <t-switch v-model="collectDisabled" size="small" @change="onToggleCollect" />
        <span class="profile-label">{{ $t('knowledgeEditor.learningTab.stopCollect') }}</span>
        <span class="spacer"></span>
        <t-button size="small" variant="outline" :loading="exporting" @click="exportProfile">{{ $t('knowledgeEditor.learningTab.export') }}</t-button>
        <t-button size="small" theme="danger" variant="outline" @click="openDeleteDialog">{{ $t('knowledgeEditor.learningTab.delete') }}</t-button>
      </div>
    </t-drawer>

    <!-- 自评「更生」原因弹窗：技能矩阵面谈问题——哪方面不熟？四原因各自映射降档力度与内容反馈标签 -->
    <t-dialog v-model:visible="selfAssessDownOpen" :header="$t('knowledgeEditor.learningTab.selfAssessDialogTitle')"
      :confirm-btn="{ content: $t('knowledgeEditor.learningTab.selfAssessSubmit'), disabled: !selfAssessReason }"
      @confirm="submitSelfAssessDown">
      <div class="sa-dialog">
        <div class="sa-node">{{ selfAssessTargetTitle }}</div>
        <div v-for="opt in selfAssessReasonOptions" :key="opt.value" class="sa-option"
          :class="{ selected: selfAssessReason === opt.value }" role="radio"
          :aria-checked="selfAssessReason === opt.value" tabindex="0"
          @click="selfAssessReason = opt.value" @keydown.enter.prevent="selfAssessReason = opt.value">
          <span class="sa-radio"></span>
          <div>
            <div class="sa-option-label">{{ opt.label }}</div>
            <div class="sa-option-hint">{{ opt.hint }}</div>
          </div>
        </div>
      </div>
    </t-dialog>

    <t-dialog v-model:visible="confirmDelete" :header="$t('knowledgeEditor.learningTab.deleteConfirmTitle')"
      :confirm-btn="{ content: $t('knowledgeEditor.learningTab.delete'), theme: 'danger', disabled: !deleteAcknowledge }" @confirm="doDelete">
      <template #body>
        <div class="delete-body">
          <div>{{ $t('knowledgeEditor.learningTab.deleteConfirmBody') }}</div>
          <label class="delete-optout">
            <input type="checkbox" v-model="deleteOptOut" />
            {{ $t('knowledgeEditor.learningTab.deleteAlsoOptOut') }}
          </label>
          <!-- 显式不可撤销确认：勾选前"删除"按钮保持禁用（Google/Meta 删除流程标配） -->
          <label class="delete-optout">
            <input type="checkbox" v-model="deleteAcknowledge" />
            {{ $t('knowledgeEditor.learningTab.deleteIrreversible') }}
          </label>
        </div>
      </template>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Button as TButton, Dialog as TDialog, Drawer as TDrawer, MessagePlugin, Popup as TPopup, Switch as TSwitch } from 'tdesign-vue-next'
import { ChevronRightIcon, HelpCircleIcon } from 'tdesign-icons-vue-next'
import LearningConstellation from './LearningConstellation.vue'
import ComponentWorkspace from './ComponentWorkspace.vue'
import {componentGraph} from './componentPresentation'
import type {ComponentView} from '@/api/learning/components'
import { buildZonePaths, circledNum, pathOrderOf, type ZonePathEntry } from './zonePath'
import ObjectiveWorkspace from './ObjectiveWorkspace.vue'
import { projectObjectiveNodes } from './objectivePresentation'
import LearningReader from './LearningReader.vue'
import type { LearningPathPlan, ObjectiveViewResponse } from '@/api/learning/objectives'
import {
  getLearningProgress, getLearningZoneMap, getLearningQuiz, submitLearningAnswer,
  getLearningTimeline, exportLearningProfile, deleteLearningProfile, selfAssess, skipNode,
  type SelfAssessDownReason,
  getLearningSettings, updateLearningSettings, getLearningMastery, getLearningChanges,
  type LearningProgress, type Recommendation, type QuizQuestion, type AnswerResult, type TimelineItem, type MasteryView, type PassiveChangesSummary,
  type ZoneMapResponse, type ZoneSummary,
} from '@/api/learning'

const props = defineProps<{ knowledgeBaseId: string }>()
const componentWorkspace=ref<InstanceType<typeof ComponentWorkspace>|null>(null)
const componentView=ref<ComponentView|null>(null)
const hasComponents=computed(()=>!!componentView.value?.components.length)
const componentNetwork=computed(()=>componentGraph(componentView.value))
// 出处文档跳转复用知识库页既有的源文档打开通道（WikiBrowser 同款事件）。
const emit = defineEmits<{ (e: 'open-source-doc', knowledgeId: string): void }>()
const router = useRouter()
const { t } = useI18n()

const progress = ref<LearningProgress | null>(null)
const recommendations = ref<Recommendation[]>([])
// 模块分区视图：每个 wiki 目录一个知识区，各区自己的"1/2"+全区节点+先修边。
const zoneMap = ref<ZoneMapResponse | null>(null)
// Stage-4 objective layer: separated progress + objective view + path plan.
const objectiveWorkspace=ref<InstanceType<typeof ObjectiveWorkspace>|null>(null)
const objView = ref<ObjectiveViewResponse | null>(null)
const currentPath = ref<LearningPathPlan | null>(null)
// Per-node objective summary from the objective view: slug → list of
// {state, title, evidenceCount, lastVerifiedAt} for badge rendering.
const objectivesBySlug = computed(() => {
  const map: Record<string, Array<{ state: string; title: string; evidence: number; lastVerified?: string; conflicting: boolean; stale: boolean }>> = {}
  if (!objView.value?.entries) return map
  for (const e of objView.value.entries) {
    if (!map[e.slug]) map[e.slug] = []
    map[e.slug].push({
      state: e.state,
      title: e.title,
      evidence: e.evidence.eligible_passes + e.evidence.eligible_failures,
      lastVerified: e.recency.last_verified_at,
      conflicting: e.state === 'conflicting',
      stale: e.state === 'stale_content',
    })
  }
  return map
})

// Per-node detail from the objective view: contact / self-report / last
// verified date / next step suggestion — the four textual dimensions each
// zone-path-row displays alongside the objective badges.
const nodeDetailBySlug = computed(() => {
  const map: Record<string, { contacted: boolean; reads: number; cites: number; selfReport: string; selfReportAt?: string; lastVerified?: string; nextStep: string }> = {}
  if (!objView.value?.entries) return map
  for (const e of objView.value.entries) {
    if (!map[e.slug]) {
      map[e.slug] = { contacted: false, reads: 0, cites: 0, selfReport: '', selfReportAt: undefined, lastVerified: undefined, nextStep: '' }
    }
    const d = map[e.slug]
    d.reads += e.exposure.reads
    d.cites += e.exposure.cites
    if (e.exposure.reads > 0 || e.exposure.cites > 0) d.contacted = true
    if (e.self_report.direction && !d.selfReport) {
      d.selfReport = e.self_report.direction
      d.selfReportAt = e.self_report.at
    }
    if (e.recency.last_verified_at && (!d.lastVerified || e.recency.last_verified_at > d.lastVerified)) {
      d.lastVerified = e.recency.last_verified_at
    }
  }
  // Derive next-step text from the freshest objective's path_status.
  for (const e of objView.value.entries) {
    if (map[e.slug] && !map[e.slug].nextStep) {
      map[e.slug].nextStep = nextStepText(e.state, e.path_status)
    }
  }
  return map
})

function nextStepText(state: string, pathStatus: string): string {
  if (pathStatus === 'user_retired') return t('knowledgeEditor.learningTab.nodeNextRetired')
  if (pathStatus === 'challenge_pending') return t('knowledgeEditor.learningTab.nodeNextChallenge')
  switch (state) {
    case 'verified': return t('knowledgeEditor.learningTab.nodeNextVerified')
    case 'conflicting': return t('knowledgeEditor.learningTab.nodeNextConflicting')
    case 'stale_content': return t('knowledgeEditor.learningTab.nodeNextStale')
    case 'partial': return t('knowledgeEditor.learningTab.nodeNextPartial')
    default: return t('knowledgeEditor.learningTab.nodeNextUnverified')
  }
}

// Objective badge label helper: text-first (not color-only).
function objBadge(entry: { state: string; evidence: number; conflicting: boolean; stale: boolean }): string {
  const { t: tl } = { t: (k: string, p?: any) => k } // placeholder; real t used below
  switch (entry.state) {
    case 'verified': return `✓ ${entry.evidence}证据`
    case 'conflicting': return `⚠ 冲突`
    case 'stale_content': return `⟳ 待复核`
    case 'partial': return `◐ ${entry.evidence}证据`
    default: return `— 未验证`
  }
}

function formatDate(iso: string): string {
  try { return new Date(iso).toLocaleDateString() } catch { return iso }
}

function objBadgeClass(entry: { state: string }): string {
  switch (entry.state) {
    case 'verified': return 'obj-badge-verified'
    case 'conflicting': return 'obj-badge-conflicting'
    case 'stale_content': return 'obj-badge-stale'
    case 'partial': return 'obj-badge-partial'
    default: return 'obj-badge-unverified'
  }
}
// 悬停右侧栏分区卡片时，星图仅亮该分区并标记其"1"。
const highlightZone = ref<string | null>(null)
// 展开某分区"1"的自评/跳过操作面板。
const expandedZoneAssess = ref<string | null>(null)
const recommendLoading = ref(false)
// 遗忘动态（被动变化通道）：与时间线（主动事件）分栏显示，互不挤占。
const passiveChanges = ref<PassiveChangesSummary | null>(null)
const initialLoading = ref(true)
const loadFailed = ref(false)
const timeline = ref<TimelineItem[]>([])
const timelinePage = ref(1)
const timelineTotal = ref(0)
const collectDisabled = ref(false)
const confirmDelete = ref(false)
const deleteOptOut = ref(false)
const deleteAcknowledge = ref(false)
const exporting = ref(false)
const quiz = ref<{
  active: boolean; slug: string; title: string; loading: boolean
  items: QuizQuestion[]; index: number; chosen: string; result: AnswerResult | null
  levelBefore: string; levelAfter: string; levelAfterP: number
  levelBeforeFaded: boolean; levelAfterFaded: boolean
}>({ active: false, slug: '', title: '', loading: false, items: [], index: 0, chosen: '', result: null, levelBefore: '', levelAfter: '', levelAfterP: 0, levelBeforeFaded: false, levelAfterFaded: false })

function fadedOf(m: MasteryView | null): boolean {
  return !!m && m.level === 'unseen' && m.evidence_count > 0
}

// 四档色对齐 WeKnora 绿色品牌阶（与图谱 graphMasteryColors 同一序列）。
const TIER_COLORS: Record<string, string> = {
  unseen: '#d0d0d0',
  touched: '#8ce0af',
  familiar: '#07c05f',
  mastered: '#038626',
}
const tierOrder = computed(() => [
  { key: 'mastered', label: t('knowledgeEditor.learningTab.tierMastered'), color: TIER_COLORS.mastered, hint: t('knowledgeEditor.learningTab.tierHintMastered') },
  { key: 'familiar', label: t('knowledgeEditor.learningTab.tierFamiliar'), color: TIER_COLORS.familiar, hint: t('knowledgeEditor.learningTab.tierHintFamiliar') },
  { key: 'touched', label: t('knowledgeEditor.learningTab.tierTouched'), color: TIER_COLORS.touched, hint: t('knowledgeEditor.learningTab.tierHintTouched') },
  { key: 'unseen', label: t('knowledgeEditor.learningTab.tierUnseen'), color: TIER_COLORS.unseen, hint: t('knowledgeEditor.learningTab.tierHintUnseen') },
])
const masteredCount = computed(() => progress.value?.levels?.mastered ?? 0)
// ---- 入场动画时间轴（对齐文档/图谱页签的动态语言）----
// progress 每次更新（首载/答题后刷新）都从当前比例平滑重扫：进度环扫过、
// 中心数字滚动、四档计数与单元进度条同步生长；reduced-motion 用户跳过。
const animT = ref(0)
let animRAF = 0
const prefersReducedMotion =
  typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
watch(progress, (p) => {
  if (!p) return
  if (prefersReducedMotion) {
    animT.value = 1
    return
  }
  cancelAnimationFrame(animRAF)
  const from = animT.value
  const start = performance.now()
  const dur = 900
  const step = (now: number) => {
    const k = Math.min(1, (now - start) / dur)
    animT.value = from + (1 - from) * (1 - Math.pow(1 - k, 3))
    if (k < 1) animRAF = requestAnimationFrame(step)
  }
  animRAF = requestAnimationFrame(step)
})
const displayLit = computed(() => Math.round((progress.value?.lit_nodes ?? 0) * animT.value))
function tierDisplay(key: string): number {
  return Math.round((progress.value?.levels?.[key] ?? 0) * animT.value)
}
const units = computed(() => progress.value?.units || [])
// 分组进度默认展开：恰好填满驾驶舱右栏与星图等高的剩余空间（替代被合并的旧分区）
const unitsCollapsed = ref(false)
// 底部两块低频分区的折叠状态：时间线默认展开（课题核心演示物），画像默认收起。
// 时间线/画像改为放大抽屉：默认关闭，右栏底部按钮进入
const timelineDrawer = ref(false)
const profileDrawer = ref(false)
const quizCurrent = computed(() => quiz.value.items[quiz.value.index] || null)
function unitPercent(u: { lit: number; total: number }) {
  return u.total ? Math.round((u.lit / u.total) * 100) : 0
}
function tierColor(level: string): string {
  return TIER_COLORS[level] || '#d0d0d0'
}
// 已淡化：有学习历史（答错/追问/衰减）但有效掌握度跌出点亮线的节点——
// 配色离开警示暖色系（红/琥珀/橙），用低饱和灰蓝表达"洗淡"，与"未接触"
// 的浅灰、三类警示暖色都不撞色。
const FADED_COLOR = '#7b8ba1'
function fadedColor(rec: Recommendation): string {
  return rec.faded ? FADED_COLOR : tierColor(rec.level || 'unseen')
}
function levelLabel(level: string, faded?: boolean): string {
  if (faded) return t('knowledgeEditor.learningTab.tierFaded')
  const map: Record<string, string> = {
    unseen: t('knowledgeEditor.learningTab.tierUnseen'),
    touched: t('knowledgeEditor.learningTab.tierTouched'),
    familiar: t('knowledgeEditor.learningTab.tierFamiliar'),
    mastered: t('knowledgeEditor.learningTab.tierMastered'),
  }
  return map[level] || level
}

// ① 的进度提示：把"同一节点会连续推荐直到巩固"这一事实说清楚——读过了
// 卡片会显示档位变化，而不是让用户以为推荐坏了（"一直卡在①"）。
function zoneStepHint(rec: Recommendation): string {
  const quiz = rec.has_quiz
    ? t('knowledgeEditor.learningTab.zoneQuizAvail', { n: rec.quiz_count || 1 })
    : ''
  let hint: string
  if (rec.faded) hint = t('knowledgeEditor.learningTab.zoneHintFaded')
  else if (rec.level === 'touched') hint = t('knowledgeEditor.learningTab.zoneHintTouched')
  else if (rec.level === 'familiar') hint = t('knowledgeEditor.learningTab.zoneHintFamiliar')
  else hint = t('knowledgeEditor.learningTab.zoneHintUnseen')
  return quiz ? `${hint} · ${quiz}` : hint
}
function levelStyle(level: string, faded?: boolean): { background: string; color: string } {
  const c = faded ? FADED_COLOR : tierColor(level)
  return { background: `${c}1f`, color: c === '#d0d0d0' ? '#8b8b8b' : c }
}

// 四档卡片展开的节点清单：懒加载一次掌握度列表并缓存。
const masteryList = ref<MasteryView[] | null>(null)
// 掌握度列表拉取失败标记：失败被缓存成空数组会伪装成"没有节点"，
// 且 toggleTier 的真值判断会让这份假空态永远不再重试——失败必须可辨、可重试。
const masteryLoadFailed = ref(false)
// 知识星图的数据源：与档位清单共用一份缓存，refresh 时后台补拉一次。
// 星图数据源：zone-map 的全量节点（带分区/章节/位次），自评悬停徽标从
// 掌握度列表补齐（两份缓存来自同一份后端事实）。
const constellationNodes = computed(() => hasComponents.value ? componentNetwork.value.nodes : projectObjectiveNodes(zoneMap.value?.nodes ?? [], objView.value))
const zoneEdges = computed(() => hasComponents.value ? componentNetwork.value.edges : zoneMap.value?.edges ?? [])
const zoneAxes = computed(() =>
  hasComponents.value ? componentNetwork.value.zones : (zoneMap.value?.zones ?? []).map((z) => ({
    id: z.folder_id,
    name: z.folder_name || t('knowledgeEditor.learningTab.rootUnit'),
    next: currentPath.value?.steps.find(step => zoneMap.value?.nodes.some(n => n.slug === step.slug && n.folder_id === z.folder_id))?.slug ?? null,
  })))
const zoneList = computed<ZoneSummary[]>(() => zoneMap.value?.zones ?? [])

// 「从这开始」= 第一张还有 ① 的卡：模块卡按推荐优先级排序，多模块时用户
// 只需跟着这个标记走，不再"每张卡都像起点"。
const firstPickIndex = computed(() => zoneList.value.findIndex((z) => !!z.next))

// 展开的完整学习路径：与档位清单/星图共用同一份 zoneMap 事实，按模块分组
// 并以资料顺序（从浅入深）排序；序号在卡片展开后连续 ①..N，每行档位批注。
const zonePaths = computed<Record<string, ZonePathEntry[]>>(() => buildZonePaths(zoneMap.value?.nodes ?? []))
// 每张模块卡独立的展开状态（重命名为整体替换以保持响应性）。
const expandedZones = ref(new Set<string>())
// 游标的路径序号：推荐目标落在学习路径的第几站（0＝不在路径里，容错为
// 不显示序号）。折叠态游标行与展开态高亮行由此共享同一套编号。
function cursorOrderOf(zone: ZoneSummary): number {
  return pathOrderOf(zonePaths.value, zone.folder_id || 'root', zone.next?.slug || '')
}
function toggleZonePath(key: string) {
  const next = new Set(expandedZones.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedZones.value = next
}
async function ensureMasteryList() {
  if (masteryList.value) return
  try {
    const res = await getLearningMastery(props.knowledgeBaseId)
    masteryList.value = (((res as any).data ?? res) as MasteryView[])
    masteryLoadFailed.value = false
  } catch {
    masteryList.value = []
    masteryLoadFailed.value = true
  }
}
// 档位清单失败重试：清掉假空态缓存并重拉。
async function retryMasteryList() {
  masteryList.value = null
  masteryLoadFailed.value = false
  if (tierExpanded.value) {
    tierListLoading.value = true
    try {
      const res = await getLearningMastery(props.knowledgeBaseId)
      masteryList.value = (((res as any).data ?? res) as MasteryView[])
    } catch {
      masteryList.value = []
      masteryLoadFailed.value = true
    } finally {
      tierListLoading.value = false
    }
  } else {
    ensureMasteryList()
  }
}
// slug→中文标题映射兜底：时间线接口自带 Title，聚合后供任何缺标题的
// 显示点（档位清单等）回退，消灭"显示 slug 路径"的整类问题。
const slugTitleMap = computed(() => {
  const map = new Map<string, string>()
  for (const it of timeline.value) {
    if (it.title && !map.has(it.slug)) map.set(it.slug, it.title)
  }
  return map
})
function titleOf(slug: string, title?: string): string {
  return title || slugTitleMap.value.get(slug) || slug
}
const tierListLoading = ref(false)
const tierExpanded = ref<string | null>(null)
const tierList = computed(() =>
  masteryList.value ? masteryList.value.filter((m) => m.level === tierExpanded.value) : [],
)
async function toggleTier(key: string) {
  if (tierExpanded.value === key) {
    tierExpanded.value = null
    return
  }
  tierExpanded.value = key
  if (!masteryList.value || masteryLoadFailed.value) {
    masteryLoadFailed.value = false
    masteryList.value = null
    tierListLoading.value = true
    try {
      const res = await getLearningMastery(props.knowledgeBaseId)
      masteryList.value = (((res as any).data ?? res) as MasteryView[])
    } catch {
      masteryList.value = []
    } finally {
      tierListLoading.value = false
    }
  }
}

// 承上启下叙述：一行讲清"为什么下一个是它"。键由后端 buildWhy/顺承通道给出，
// 参数（引用节点/章节/文档/位次）来自 WhyRef 与 Section/DocTitle/DocRank。
function whyText(rec: Recommendation): string {
  if (!rec.why) return ''
  const key = 'knowledgeEditor.learningTab.why' + rec.why
    .split('_').map((s, i) => i === 0 ? s : s.charAt(0).toUpperCase() + s.slice(1)).join('')
  switch (rec.why) {
    case 'continues_prereq':
    case 'continues_prev':
      return t(key, { ref: rec.why_ref || '' })
    case 'same_section':
      return t(key, { ref: rec.why_ref || '', section: rec.section || '' })
    case 'material_start':
      return t(key, { rank: rec.doc_rank ?? 0, section: rec.section || rec.doc_title || '' })
    case 'chapter_of':
    case 'in_doc':
      return t(key, { label: rec.why_ref || '' })
    default:
      return t(key)
  }
}

function reasonText(reason: string): string {
  const map: Record<string, string> = {
    affinity: t('knowledgeEditor.learningTab.reasonAffinity'),
    frontier: t('knowledgeEditor.learningTab.reasonFrontier'),
    foundation: t('knowledgeEditor.learningTab.reasonFoundation'),
    continue: t('knowledgeEditor.learningTab.reasonContinue'),
    consolidate: t('knowledgeEditor.learningTab.reasonConsolidate'),
    review: t('knowledgeEditor.learningTab.reasonReview'),
    remedial: t('knowledgeEditor.learningTab.reasonRemedial'),
    struggling: t('knowledgeEditor.learningTab.reasonStruggling'),
    'prerequisite-stuck': t('knowledgeEditor.learningTab.reasonStuck'),
    bypass: t('knowledgeEditor.learningTab.reasonBypass'),
    explore: t('knowledgeEditor.learningTab.reasonExplore'),
    'self-verify': t('knowledgeEditor.learningTab.reasonSelfVerify'),
  }
  return map[reason] || reason
}
// 游标行短理由（≤6 字）：紧贴提示行前缀，完整语义放悬停（reasonText）。
function reasonShort(reason: string): string {
  const map: Record<string, string> = {
    affinity: t('knowledgeEditor.learningTab.rsAffinity'),
    frontier: t('knowledgeEditor.learningTab.rsFrontier'),
    foundation: t('knowledgeEditor.learningTab.rsFoundation'),
    continue: t('knowledgeEditor.learningTab.rsContinue'),
    consolidate: t('knowledgeEditor.learningTab.rsConsolidate'),
    review: t('knowledgeEditor.learningTab.rsReview'),
    remedial: t('knowledgeEditor.learningTab.rsRemedial'),
    struggling: t('knowledgeEditor.learningTab.rsStruggling'),
    'prerequisite-stuck': t('knowledgeEditor.learningTab.rsStuck'),
    bypass: t('knowledgeEditor.learningTab.rsBypass'),
    explore: t('knowledgeEditor.learningTab.rsExplore'),
    'self-verify': t('knowledgeEditor.learningTab.rsSelfVerify'),
  }
  return map[reason] || ''
}
function pageTypeOf(slug: string): string {
  const i = slug.indexOf('/')
  return i > 0 ? slug.slice(0, i) : ''
}

// 页面类型徽标：时间线/推荐卡用"实体/概念"中文徽标替代 slug 前缀。
function pageTypeText(pageType: string): string {
  const map: Record<string, string> = {
    entity: t('knowledgeEditor.learningTab.badgeEntity'),
    concept: t('knowledgeEditor.learningTab.badgeConcept'),
  }
  return map[pageType] || pageType
}
function eventText(type: string): string {
  if(type==='component_learning')return '目标学习'
  if(type==='source_read')return '原文已读'
  const recallLabels:Record<string,string>={review_enroll:'开启间隔复习',review_pause:'暂停间隔复习',review_again:'回忆反馈：忘记了',review_hard:'回忆反馈：费力想起',review_good:'回忆反馈：正常想起',review_easy:'回忆反馈：轻松想起'}
  if(recallLabels[type])return recallLabels[type]
  const map: Record<string, string> = {
    answer_cite: t('knowledgeEditor.learningTab.evCite'),
    cross_ref: t('knowledgeEditor.learningTab.evCross'),
    re_ask: t('knowledgeEditor.learningTab.evReAsk'),
    topic_signal: t('knowledgeEditor.learningTab.evTopic'),
    wiki_tool_read: t('knowledgeEditor.learningTab.evRead'),
    agent_read: t('knowledgeEditor.learningTab.evAgentRead'),
    wiki_deep_read: t('knowledgeEditor.learningTab.evDeepRead'),
    quiz_correct: t('knowledgeEditor.learningTab.evQuizOk'),
    quiz_wrong: t('knowledgeEditor.learningTab.evQuizBad'),
    quiz_unsure: t('knowledgeEditor.learningTab.evUnsure'),
    backfill_cite: t('knowledgeEditor.learningTab.evBackfill'),
    self_assess_up: t('knowledgeEditor.learningTab.evSelfAssessUp'),
    self_assess_down_all: t('knowledgeEditor.learningTab.evSelfAssessDownAll'),
    self_assess_down_doc_gap: t('knowledgeEditor.learningTab.evSelfAssessDownDocGap'),
    self_assess_down_doc_updated: t('knowledgeEditor.learningTab.evSelfAssessDownDocUpdated'),
    self_assess_down_quiz_easy: t('knowledgeEditor.learningTab.evSelfAssessDownQuizEasy'),
  }
  return map[type] || type
}

// 下档路径提示：把直接证据门控的解锁条件写成可达目标（快变量的"下一步"）。
function hintText(hint: string): string {
  const map: Record<string, string> = {
    first_touch: t('knowledgeEditor.learningTab.hintFirstTouch'),
    quiz_unlock_familiar: t('knowledgeEditor.learningTab.hintQuizUnlockFamiliar'),
    quiz_unlock_mastered: t('knowledgeEditor.learningTab.hintQuizUnlockMastered'),
    grow_mastery: t('knowledgeEditor.learningTab.hintGrowMastery'),
    keep_reviewing: t('knowledgeEditor.learningTab.hintKeepReviewing'),
  }
  return map[hint] || ''
}

// 今日答题摘要：企业产品语境——始终给事实性数字，不做"还没开始学习"式的提醒。
const todayAnswersText = computed(() => {
  const td = progress.value?.today
  return t('knowledgeEditor.learningTab.todayAnswers', { n: td?.answers ?? 0, m: td?.correct_count ?? 0 })
})

// 今日待巩固：被动遗忘（已降档/临近降档）+ 主动推荐中的错题重练/反复追问/
// 前置阻塞，合并去重为一张待办清单——"今天该做什么"一眼可答。
type TodoKind = 'demoted' | 'due' | 'remedial' | 'struggling' | 'stuck'
interface TodoRow { slug: string; title: string; kind: TodoKind; detail: string; hasQuiz: boolean }
const todayTodo = computed<TodoRow[]>(() => {
  const bySlug = new Map<string, TodoRow>()
  const rank: Record<TodoKind, number> = { demoted: 0, due: 1, remedial: 2, struggling: 3, stuck: 4 }
  const put = (row: TodoRow) => {
    const prev = bySlug.get(row.slug)
    if (!prev || rank[row.kind] < rank[prev.kind]) bySlug.set(row.slug, row)
  }
  for (const ch of passiveChanges.value?.items ?? []) {
    put({
      slug: ch.slug, title: ch.title || ch.slug,
      kind: ch.demoted ? 'demoted' : 'due',
      detail: ch.demoted
        ? t('knowledgeEditor.learningTab.changesIdle', { d: Math.round(ch.days_idle) })
        : t('knowledgeEditor.learningTab.changesNext', { n: Math.ceil(ch.next_review_days ?? 0) }),
      hasQuiz: false, // 被动通道不带题库信息，直达阅读即可
    })
  }
  // 分区视图下的错题/阻塞来源：各区的"1/2"携带同一套多通道理由。
  for (const z of zoneMap.value?.zones ?? []) {
    for (const r of [z.next, z.second]) {
      if (!r) continue
      const kind: TodoKind | null =
        r.reason === 'remedial' ? 'remedial'
          : r.reason === 'struggling' ? 'struggling'
            : r.reason === 'prerequisite-stuck' ? 'stuck'
              : null
      if (!kind) continue
      put({ slug: r.slug, title: r.title || r.slug, kind, detail: '', hasQuiz: r.has_quiz })
    }
  }
  return [...bySlug.values()].sort((a, b) => rank[a.kind] - rank[b.kind] || a.title.localeCompare(b.title)).slice(0, 8)
})
function todoTagText(kind: TodoKind): string {
  const map: Record<TodoKind, string> = {
    demoted: t('knowledgeEditor.learningTab.todoDemoted'),
    due: t('knowledgeEditor.learningTab.todoDue'),
    remedial: t('knowledgeEditor.learningTab.reasonRemedialShort'),
    struggling: t('knowledgeEditor.learningTab.reasonStrugglingShort'),
    stuck: t('knowledgeEditor.learningTab.reasonStuckShort'),
  }
  return map[kind]
}
function todoTagStyle(kind: TodoKind): { background: string; color: string } {
  const map: Record<TodoKind, { background: string; color: string }> = {
    demoted: { background: 'rgba(213, 73, 65, 0.10)', color: '#c8383f' },
    due: { background: 'rgba(212, 160, 23, 0.12)', color: '#a87b10' },
    remedial: { background: 'rgba(213, 73, 65, 0.10)', color: '#c8383f' },
    struggling: { background: 'rgba(212, 160, 23, 0.12)', color: '#a87b10' },
    stuck: { background: 'rgba(227, 115, 24, 0.12)', color: '#e37318' },
  }
  return map[kind]
}
const { locale } = useI18n()
function formatTime(iso: string): string {
  try {
    return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(iso))
  } catch {
    return iso
  }
}

// ---- 时间线筛选 + 日期分组 ----
type TlFilter = 'all' | 'cite' | 'cross' | 'reask' | 'quiz' | 'other'
const timelineFilter = ref<TlFilter>('all')
const timelineFilters = computed(() => [
  { key: 'all' as TlFilter, label: t('knowledgeEditor.learningTab.tlFilterAll') },
  { key: 'cite' as TlFilter, label: t('knowledgeEditor.learningTab.tlFilterCite') },
  { key: 'cross' as TlFilter, label: t('knowledgeEditor.learningTab.tlFilterCross') },
  { key: 'reask' as TlFilter, label: t('knowledgeEditor.learningTab.tlFilterReAsk') },
  { key: 'quiz' as TlFilter, label: t('knowledgeEditor.learningTab.tlFilterQuiz') },
])
function filterOf(type: string): TlFilter {
  if (type === 'answer_cite' || type === 'backfill_cite' || type === 'topic_signal' || type === 'wiki_tool_read' || type === 'agent_read') return 'cite'
  if (type === 'cross_ref') return 'cross'
  if (type === 're_ask') return 'reask'
  if (type === 'quiz_correct' || type === 'quiz_wrong' || type === 'quiz_unsure') return 'quiz'
  return 'other'
}
const filteredTimeline = computed(() => {
  let items = timeline.value
  if (timelineFilter.value !== 'all') {
    items = items.filter((it) => filterOf(it.event_type) === timelineFilter.value)
  }
  if (selectedNodeSlugs.value.size > 0) {
    items = items.filter((it) => selectedNodeSlugs.value.has(it.slug))
  }
  return items
})
// 筛选在生效吗（类型或节点任一）——假空态判定用：筛选中且还有未加载页时，
// "无匹配"只能对已加载页成立。
const hasActiveFilters = computed(() => timelineFilter.value !== 'all' || selectedNodeSlugs.value.size > 0)

// 按节点筛选：从已加载事件聚合节点 chips（中文标题优先），多选与类型筛选正交。
const selectedNodeSlugs = ref<Set<string>>(new Set())
const timelineNodesCollapsed = ref(true)
const timelineNodeChips = computed(() => {
  const agg = new Map<string, { slug: string; title: string; count: number }>()
  for (const it of timeline.value) {
    const cur = agg.get(it.slug) || { slug: it.slug, title: it.title || it.slug, count: 0 }
    cur.count++
    agg.set(it.slug, cur)
  }
  return [...agg.values()].sort((a, b) => b.count - a.count || a.slug.localeCompare(b.slug))
})
const timelineNodesVisible = computed(() =>
  timelineNodesCollapsed.value ? timelineNodeChips.value.slice(0, 8) : timelineNodeChips.value,
)
function toggleNodeFilter(slug: string) {
  const next = new Set(selectedNodeSlugs.value)
  if (next.has(slug)) {
    next.delete(slug)
  } else {
    next.add(slug)
  }
  selectedNodeSlugs.value = next
}
const groupedTimeline = computed(() => {
  const groups: { label: string; items: (TimelineItem & { key: string })[] }[] = []
  const dayLabel = (iso: string): string => {
    const d = new Date(iso)
    const today = new Date()
    const yesterday = new Date(today)
    yesterday.setDate(today.getDate() - 1)
    const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString()
    if (sameDay(d, today)) return t('knowledgeEditor.learningTab.tlToday')
    if (sameDay(d, yesterday)) return t('knowledgeEditor.learningTab.tlYesterday')
    return new Intl.DateTimeFormat(locale.value, { month: 'numeric', day: 'numeric' }).format(d)
  }
  for (let i = 0; i < filteredTimeline.value.length; i++) {
    const item = filteredTimeline.value[i]
    const label = dayLabel(item.occurred_at)
    if (!groups.length || groups[groups.length - 1].label !== label) {
      groups.push({ label, items: [] })
    }
    groups[groups.length - 1].items.push({ ...item, key: `${item.event_type}-${i}` })
  }
  return groups
})

const profileSummary = computed(() => {
  const nodes = hasComponents.value ? componentView.value!.components.length : progress.value?.total_nodes ?? 0
  const last = timeline.value[0]?.occurred_at
	if (hasComponents.value) return `当前库：${nodes} 个学习目标；${timelineTotal.value} 条历史事件（含旧页面记录）；最近活动 ${last ? formatTime(last) : '—'}。`
  return '当前库：' + t('knowledgeEditor.learningTab.profileSummary', {
    nodes,
    events: timelineTotal.value,
    last: last ? formatTime(last) : '—',
  })
})

const readerSlug=ref(''),readerOpen=ref(false)
watch(()=>props.knowledgeBaseId,()=>{readerOpen.value=false;readerSlug.value=''})
const readerNext=computed(()=>currentPath.value?.steps.find(step=>!step.completed&&step.slug!==readerSlug.value))
function openPage(slug:string){if(slug.startsWith('kc:')){componentWorkspace.value?.open(slug.slice(3));return}readerSlug.value=slug;readerOpen.value=true}
function openWikiPage(slug: string) {
  readerOpen.value=false
  router.push({ path: `/platform/knowledge-bases/${props.knowledgeBaseId}`, query: { tab: 'wiki', slug } })
}

// 零节点空态：Wiki 库尚无 entity/concept 页面——给出去向（图谱页签），
// 而不是让 0/0 空环自己解释自己。
const hasNodes = computed(() => hasComponents.value || (progress.value?.total_nodes ?? 0) > 0)
function goGraph() {
  router.push({ path: `/platform/knowledge-bases/${props.knowledgeBaseId}`, query: { tab: 'graph' } })
}

// 后端单页上限 100：首屏一次拉满，原型规模下根本不出现"翻页"这回事。
const TIMELINE_PAGE_SIZE = 100

async function refresh() {
  recommendLoading.value = true
  try {
    const [p, r, tl, s, ch] = await Promise.all([
      getLearningProgress(props.knowledgeBaseId),
      getLearningZoneMap(props.knowledgeBaseId),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
      getLearningSettings(),
      getLearningChanges(props.knowledgeBaseId, 20),
    ])
    progress.value = (p as any).data ?? p
    zoneMap.value = (r as any).data ?? null
    recommendations.value = ((r as any).data?.zones ?? [])
      .map((z: ZoneSummary) => z.next)
      .filter((x: Recommendation | null): x is Recommendation => !!x)
    timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
    timelineTotal.value = ((tl as any).total ?? 0) as number
    passiveChanges.value = ((ch as any).data ?? ch) as PassiveChangesSummary
    // 刷新即回到第 1 页：否则触底续拉会带着旧页码跳页（关闭答题卡/
    // 删除画像后的刷新都走这里）。
    timelinePage.value = 1
    collectDisabled.value = (s as any).data?.collect_disabled ?? false
    masteryList.value = null // 画像可能已变化，档位清单缓存失效
    ensureMasteryList() // 星图随刷新重建
    await objectiveWorkspace.value?.refresh()
    await componentWorkspace.value?.refresh()
    loadFailed.value = false
  } catch (err) {
    console.error('[LearningTab] refresh failed:', err)
    loadFailed.value = true
    MessagePlugin.error(t('knowledgeEditor.learningTab.loadFailed'))
  } finally {
    recommendLoading.value = false
    initialLoading.value = false
  }
}

const timelineEndRef = ref<HTMLElement | null>(null)
const timelineLoading = ref(false)
// 哨兵当前是否在视口内：IntersectionObserver 只在进出视口时回调，加载完
// 一页后若哨兵仍在视口里，必须据此主动续拉，否则会卡在"还差一页"空转。
const sentinelVisible = ref(false)
let timelineObserver: IntersectionObserver | null = null

async function loadMoreTimeline() {
  if (timelineLoading.value || timeline.value.length >= timelineTotal.value) return
  timelineLoading.value = true
  timelinePage.value++
  let items: TimelineItem[] = []
  try {
    const res = await getLearningTimeline(props.knowledgeBaseId, timelinePage.value, TIMELINE_PAGE_SIZE)
    items = ((res as any).data ?? res) as TimelineItem[]
    timeline.value.push(...items)
  } catch {
    timelinePage.value--
    return // 网络失败：停止本轮，哨兵再次进出视口时会重试
  } finally {
    timelineLoading.value = false
  }
  // 链式续拉：一页拉完哨兵仍在视口内就继续（observer 不会重复回调）；
  // 某页返回 0 条但 total 未满足属数据不一致，按到底处理防止死循环。
  if (items.length > 0 && sentinelVisible.value && timeline.value.length < timelineTotal.value) {
    loadMoreTimeline()
  }
}

// 时间线"开卷即新"：助手问答的学习事件在回答流结束后才落账，用户在流式期间
// 切回学习页的那次 onActivated 刷新会与之擦肩而过，抽屉若不重取将永远陈旧
// （"我问了却没有记录"的根因）。打开抽屉的检视时刻静默回到第 1 页。
async function refreshTimelineSilently() {
  try {
    const res = await getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE)
    timeline.value = ((res as any).data ?? res) as TimelineItem[]
    timelineTotal.value = (((res as any).total ?? 0) as number) || timeline.value.length
    timelinePage.value = 1
  } catch {
    // 拉取失败保留现有列表：抽屉内本就有重试按钮
  }
}

// 作答前后档位对比：掌握度变化透明可见（推演 2.3 的"实时看到行为如何改变掌握度"）。
// 停采时不显示（本就不记录）；档位未变时也如实显示当前档位与 p_eff，让"分数在动"可见。
// 刻意不展示 ±权重数值：形成性练习的研究共识是负分框架损害动机，档位与概率已足够透明。
const levelChangeText = computed(() => {
  if (!quiz.value.result || !quiz.value.levelBefore || collectDisabled.value) return ''
  const from = quiz.value.levelBefore
  const to = quiz.value.levelAfter || from
  if (from !== to) {
    return t('knowledgeEditor.learningTab.levelUpdate', {
      from: levelLabel(from, quiz.value.levelBeforeFaded),
      to: levelLabel(to, quiz.value.levelAfterFaded),
    })
  }
  return t('knowledgeEditor.learningTab.levelKeep', {
    level: levelLabel(to, quiz.value.levelAfterFaded),
    p: Math.round((quiz.value.levelAfterP ?? 0) * 100),
  })
})
// 确定性复习计划外显（Anki 按钮标间隔的等价物）："该节点预计保持 N 天"。
const reviewScheduleText = computed(() => {
  if (!quiz.value.result || collectDisabled.value) return ''
  const days = quiz.value.result.next_review_days
  if (days == null) return t('knowledgeEditor.learningTab.reviewNow')
  return t('knowledgeEditor.learningTab.nextReviewDays', { n: Math.max(1, Math.round(days)) })
})
// 快变量正反馈：作答前后的有效掌握度对比（服务端折叠前后各取一次）。
// 首次作答无 before（节点此前无状态），只显示"→ 72%"式单值。
const pEffMoveText = computed(() => {
  const r = quiz.value.result
  if (!r || collectDisabled.value) return ''
  const after = r.p_eff_after
  if (after == null) return ''
  const a = Math.round(after * 100)
  if (r.p_eff_before == null) return t('knowledgeEditor.learningTab.pEffMove', { from: '—', to: a })
  return t('knowledgeEditor.learningTab.pEffMove', { from: Math.round(r.p_eff_before * 100), to: a })
})
// 当前题的出处文档：作答后的溯源链接数据源。
const currentSourceDocs = computed(() => quizCurrent.value?.source_docs ?? [])
// 找单个节点的掌握度视图：复用档位清单缓存（首次调用填充）——开题的
// before 读档不再为找一个节点全量拉 map。调用方在作答后必须先置空缓存
// 再调用（见 submitAnswer），否则拿到的是作答前的旧档。
async function currentMasteryOf(slug: string): Promise<MasteryView | null> {
  if (!masteryList.value) {
    try {
      const res = await getLearningMastery(props.knowledgeBaseId)
      masteryList.value = (((res as any).data ?? res) as MasteryView[])
    } catch {
      return null
    }
  }
  return masteryList.value.find((m) => m.slug === slug) || null
}

// Bug fix: generation counter — when quiz A is closed and quiz B opened
// quickly, A's late-arriving response would overwrite B's items/levelBefore
// (wrong questions displayed under B's slug, wrong mastery comparison).
const quizHelped=ref(false)
let quizGeneration = 0
async function startQuiz(rec: Recommendation, objectiveID?: string) {
  quizHelped.value=false
  const gen = ++quizGeneration
  quiz.value = { active: true, slug: rec.slug, title: rec.title || rec.slug, loading: true, items: [], index: 0, chosen: '', result: null, levelBefore: '', levelAfter: '', levelAfterP: 0, levelBeforeFaded: false, levelAfterFaded: false }
  const [before] = await Promise.all([currentMasteryOf(rec.slug), (async () => {
    try {
      const res = await getLearningQuiz(props.knowledgeBaseId, rec.slug)
      if (gen !== quizGeneration) return // a newer quiz session superseded this one
      quiz.value.items = (((res as any).data ?? res) as QuizQuestion[]).filter(q=>!objectiveID||q.objective_id===objectiveID)
    } catch (err) {
      // 取题失败给出明确反馈，而不是静默渲染"暂无题目"的误导性空态。
      console.error('[LearningTab] quiz fetch failed:', err)
      quiz.value.items = []
      MessagePlugin.error(t('knowledgeEditor.learningTab.quizLoadFailed'))
    } finally {
      quiz.value.loading = false
    }
  })()])
  if (gen !== quizGeneration) return // stale session: don't overwrite a newer quiz's state
  quiz.value.levelBefore = before?.level || ''
  quiz.value.levelBeforeFaded = fadedOf(before)
  // URL 直达（图谱抽屉"练一练"）传入的 title 是 slug：用掌握度列表里的
  // 中文标题回填，标题栏永远显示中文而不是概念路径。
  if ((!quiz.value.title || quiz.value.title === rec.slug) && before?.title) {
    quiz.value.title = before.title
  }
  // 答题卡以抽屉呈现：quiz.active 即可见，无需滚动定位。
  await nextTick()
}
function chooseOption(key: string) {
  if (!quiz.value.result) quiz.value.chosen = key
}
// Bug fix: in-flight guard — without this, holding Enter (auto-repeat) or
// double-clicking during network RTT fires multiple POSTs; the backend has
// no idempotency key, so each lands as a duplicate attempt+event.
const quizSubmitting = ref(false)
async function submitAnswer() {
  if (quizSubmitting.value) return
  const current = quizCurrent.value
  if (!current || !quiz.value.chosen) return
  quizSubmitting.value = true
  try {
    const res = await submitLearningAnswer(props.knowledgeBaseId, current.id, quiz.value.chosen, quizHelped.value ? "assistant_helped" : current.assistance_mode)
    quiz.value.result = ((res as any).data ?? res) as AnswerResult
    // "看→练→掌握度更新→下一轮推荐"闭环在一屏内完成：作答后即时重拉
    // 进度、该节点掌握度、推荐列表与时间线首屏，不等关闭答题卡。
    // 缓存必须在取 after 档之前失效——否则 currentMasteryOf 会命中
    // 作答前的旧档，levelAfter 永远等于 levelBefore。
    masteryList.value = null
    const [p, after, r, tl] = await Promise.all([
      getLearningProgress(props.knowledgeBaseId),
      currentMasteryOf(quiz.value.slug),
      getLearningZoneMap(props.knowledgeBaseId),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
    ])
    progress.value = (p as any).data ?? p
    quiz.value.levelAfter = after?.level || quiz.value.levelBefore
    quiz.value.levelAfterFaded = fadedOf(after)
    quiz.value.levelAfterP = after?.p_eff ?? 0
    zoneMap.value = (r as any).data ?? null
    recommendations.value = ((r as any).data?.zones ?? [])
      .map((z: ZoneSummary) => z.next)
      .filter((x: Recommendation | null): x is Recommendation => !!x)
    timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
    timelineTotal.value = ((tl as any).total ?? 0) as number
    timelinePage.value = 1
    await objectiveWorkspace.value?.refresh()
    // currentMasteryOf 已回填作答后的档位缓存（星图/档位清单直接复用）。
  } catch (err) {
    console.error('[LearningTab] submit failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.submitFailed'))
  } finally {
    quizSubmitting.value = false
  }
}
function nextQuestion() {
  quizHelped.value=false
  quiz.value.index++
  quiz.value.chosen = ''
  quiz.value.result = null
  quiz.value.levelBefore = quiz.value.levelAfter || quiz.value.levelBefore
  quiz.value.levelBeforeFaded = quiz.value.levelAfterFaded
}

function closeQuiz() {
  quiz.value.active = false
  refresh()
}

async function onToggleCollect(value: unknown) {
  try {
    await updateLearningSettings(Boolean(value))
  } catch (err) {
    console.error('[LearningTab] toggle collect failed:', err)
    collectDisabled.value = !Boolean(value) // rollback
    MessagePlugin.error(t('knowledgeEditor.learningTab.toggleFailed'))
  }
}

// 打开删除确认即重置勾选：上一次会话留下的"已了解不可撤销"不能预授权本次删除。
function openDeleteDialog() {
  deleteOptOut.value = false
  deleteAcknowledge.value = false
  confirmDelete.value = true
}

async function exportProfile() {
  exporting.value = true
  try {
    const text = await (await import('@/api/learning')).downloadLearningProfile()
    const blob = new Blob([text], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    // 文件名带日期：多次导出不互相覆盖。
    const day = new Date().toISOString().slice(0, 10)
    a.download = `weknora-learning-profile-${day}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    console.error('[LearningTab] export failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.exportFailed'))
  } finally {
    exporting.value = false
  }
}

async function doDelete() {
  confirmDelete.value = false
  componentWorkspace.value?.close()
  try {
    const optOut = deleteOptOut.value
    await deleteLearningProfile(optOut)
    await objectiveWorkspace.value?.restorePreferences()
    if (optOut) {
      collectDisabled.value = true
      deleteOptOut.value = false
    }
    deleteAcknowledge.value = false
    MessagePlugin.success(t('knowledgeEditor.learningTab.deleteDone'))
    refresh()
  } catch (err) {
    console.error('[LearningTab] delete failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.deleteFailed'))
  }
}

// 键盘作答：A–D 选择选项、E 选择「不确定」、Enter 提交/下一题——练一练可以全程不碰鼠标。
function onKeydown(e: KeyboardEvent) {
  if (!quiz.value.active) return
  // Bug fix: don't hijack modified keys (Ctrl+A select-all, Cmd+C copy,
  // Alt+Tab, IME composition) — the bare-letter quiz shortcuts must not
  // fire when the user is doing something else with the keyboard.
  if (e.ctrlKey || e.metaKey || e.altKey || e.isComposing) return
  if (e.repeat && e.key === 'Enter') return // Bug fix: Enter auto-repeat re-submits
  const key = e.key.toUpperCase()
  if (quiz.value.result === null) {
    if (key === 'E') {
      chooseOption('E')
      e.preventDefault()
    } else if (/^[A-D]$/.test(key) && quizCurrent.value && key in quizCurrent.value.options) {
      chooseOption(key)
      e.preventDefault()
    } else if (e.key === 'Enter' && quiz.value.chosen) {
      submitAnswer()
      e.preventDefault()
    }
  } else if (e.key === 'Enter' && quiz.value.index < quiz.value.items.length - 1) {
    nextQuestion()
    e.preventDefault()
  }
}

// 时间线触底自动翻页：哨兵进入视口即加载下一页（root 缺省按视口计算，
// 嵌套滚动容器同样生效），加载到与 total 持平后哨兵隐藏、不再触发。
// 时间线本体在抽屉里，抽屉内容首次打开才挂载，故此处做成幂等的 ensure。
function ensureTimelineObserver() {
  if (timelineObserver || typeof IntersectionObserver === 'undefined') return
  timelineObserver = new IntersectionObserver(
    (entries) => {
      sentinelVisible.value = entries.some((e) => e.isIntersecting)
      if (sentinelVisible.value) loadMoreTimeline()
    },
    { rootMargin: '200px' },
  )
  if (timelineEndRef.value) timelineObserver.observe(timelineEndRef.value)
}

// Bug fix: the sentinel element lives inside `<template v-else>` of the
// timeline drawer — it does not exist at onMounted time (initialLoading
// renders the loading branch), and the drawer-open watch's
// ensureTimelineObserver early-returns because the observer already
// exists. Watch the ref itself: every time the sentinel enters the DOM,
// observe it (re-observing the same element is a spec-defined no-op).
watch(timelineEndRef, (el) => {
  if (el && timelineObserver) timelineObserver.observe(el)
})

onMounted(() => {
  refresh()
  maybeOpenPracticeFromQuery()
  window.addEventListener('keydown', onKeydown)
  ensureTimelineObserver()
  // The sentinel may already exist by the time the observer is created
  // (e.g., timeline data arrived before mount finished).
  if (timelineEndRef.value && timelineObserver) timelineObserver.observe(timelineEndRef.value)
})

// KeepAlive 失活/复激（挂在 KnowledgeBase 的 <KeepAlive> 里）：切页签回来
// 时静默刷新（refresh 首载后本就不设整屏 loading，无白屏），从 Wiki/图谱
// 阅读回来的 48h 闪烁态、推荐与今日统计即时对齐；失活期间全局键盘监听
// 与入场动画 rAF 一并挂起。
onActivated(() => {
  if (!initialLoading.value) refresh()
  window.addEventListener('keydown', onKeydown)
})
onDeactivated(() => {
  readerOpen.value = false
  window.removeEventListener('keydown', onKeydown)
  cancelAnimationFrame(animRAF)
  // 侧栏分区卡悬停置位的 highlightZone 在切页签（如经覆盖建议进入 wiki）
  // 时收不到 mouseleave——不清则回来后星图"卡"在该区的关联高亮。
  highlightZone.value = null
})

// 时间线抽屉首次打开时哨兵才存在于 DOM：挂载后再挂观察器；每次打开都
// 静默重取第 1 页（见 refreshTimelineSilently 注释）。
watch(timelineDrawer, (open) => {
  if (open) {
    refreshTimelineSilently()
    nextTick(() => {
      ensureTimelineObserver()
      if (timelineEndRef.value && timelineObserver) timelineObserver.observe(timelineEndRef.value)
    })
  }
})

// 今日学习记录条数：徽标直接告诉刚回来的用户"今天的动作都记上了"
const todayTimelineCount = computed(() => {
  const today = new Date().toDateString()
  return timeline.value.filter((it) => new Date(it.occurred_at).toDateString() === today).length
})

// ---- 技能矩阵自评轨：挑战系统对自己的判断 ----
// 「更熟」：分数直接抬进已掌握区间（后端 set-point 事件），档位仍被直接证据
// 门控封顶——系统说"来做题证明"；「更生」：四原因降档（全部=重置到地板，
// 三种部分原因=降一档并落内容反馈标签）。
const selfAssessDownOpen = ref(false)
const selfAssessReason = ref<SelfAssessDownReason | ''>('')
const selfAssessTarget = ref<Recommendation | null>(null)
const selfAssessReasonOptions = computed(() => [
  { value: 'all' as const, label: t('knowledgeEditor.learningTab.saReasonAll'), hint: t('knowledgeEditor.learningTab.saReasonAllHint') },
  { value: 'doc_gap' as const, label: t('knowledgeEditor.learningTab.saReasonDocGap'), hint: t('knowledgeEditor.learningTab.saReasonDocGapHint') },
  { value: 'doc_updated' as const, label: t('knowledgeEditor.learningTab.saReasonDocUpdated'), hint: t('knowledgeEditor.learningTab.saReasonDocUpdatedHint') },
  { value: 'quiz_easy' as const, label: t('knowledgeEditor.learningTab.saReasonQuizEasy'), hint: t('knowledgeEditor.learningTab.saReasonQuizEasyHint') },
])
const selfAssessTargetTitle = computed(() => selfAssessTarget.value?.title || selfAssessTarget.value?.slug || '')

async function confirmSelfAssessUp(rec: Recommendation) {
  try {
    await selfAssess(props.knowledgeBaseId, rec.slug, 'up')
    // 有题与无题走两条实话：无题时"去做两道题"是无法执行的指引。
    MessagePlugin.success(t(rec.has_quiz
      ? 'knowledgeEditor.learningTab.selfAssessUpDone'
      : 'knowledgeEditor.learningTab.selfAssessUpDoneNoQuiz'))
    await refresh()
    // 引导验证闭环：抬分完成，立刻请用户"做两道题证明"（跨 48h 门控）。
    if (rec.has_quiz) startQuiz(rec)
  } catch (err) {
    console.error('[LearningTab] self-assess up failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.selfAssessFailed'))
  }
}

// ---- 已掌握，不再推荐（跳过轨） ----
// 与自评「更熟」相反的意图：不是"请验证我"，而是"把这块让出队列"。
// 落库后节点立即从推荐/遗忘队列消失；恢复入口在档位清单与星图悬停里。
const skipSubmitting = ref(false)
async function confirmSkip(rec: Recommendation) {
  if (skipSubmitting.value) return
  skipSubmitting.value = true
  try {
    await skipNode(props.knowledgeBaseId, rec.slug, true)
    MessagePlugin.success(t('knowledgeEditor.learningTab.skipDone'))
    await refresh()
  } catch (err) {
    console.error('[LearningTab] skip failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.skipFailed'))
  } finally {
    skipSubmitting.value = false
  }
}
async function restoreSkip(slug: string) {
  if (skipSubmitting.value) return
  skipSubmitting.value = true
  try {
    await skipNode(props.knowledgeBaseId, slug, false)
    MessagePlugin.success(t('knowledgeEditor.learningTab.skipRestored'))
    await refresh()
  } catch (err) {
    console.error('[LearningTab] restore skip failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.skipFailed'))
  } finally {
    skipSubmitting.value = false
  }
}
function openSelfAssessDown(rec: Recommendation) {
  selfAssessTarget.value = rec
  selfAssessReason.value = ''
  selfAssessDownOpen.value = true
}
async function submitSelfAssessDown() {
  const rec = selfAssessTarget.value
  const reason = selfAssessReason.value
  if (!rec || !reason) return
  try {
    await selfAssess(props.knowledgeBaseId, rec.slug, 'down', reason)
    MessagePlugin.success(t('knowledgeEditor.learningTab.selfAssessDownDone'))
    selfAssessDownOpen.value = false
    await refresh()
  } catch (err) {
    console.error('[LearningTab] self-assess down failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.selfAssessFailed'))
  }
}
// 悬停回显：7 天内的自评状态（星图与档位明细共用）。
function selfAssessText(m?: { direction: string; event_type: string }): string {
  if (!m) return ''
  if (m.direction === 'up') return t('knowledgeEditor.learningTab.saBadgeUp')
  const reasonMap: Record<string, string> = {
    self_assess_down_all: t('knowledgeEditor.learningTab.saBadgeDownAll'),
    self_assess_down_doc_gap: t('knowledgeEditor.learningTab.saBadgeDownDocGap'),
    self_assess_down_doc_updated: t('knowledgeEditor.learningTab.saBadgeDownDocUpdated'),
    self_assess_down_quiz_easy: t('knowledgeEditor.learningTab.saBadgeDownQuizEasy'),
  }
  return reasonMap[m.event_type] || t('knowledgeEditor.learningTab.saBadgeDownAll')
}

// 图谱抽屉"练一练"直连：?tab=learning&practice=slug 打开该节点答题卡，
// 打开后即从 URL 移除，刷新不重放。watch 兼容已挂载时的再次跳转。
const route = useRoute()
async function maybeOpenPracticeFromQuery() {
  const slug = route.query.practice
  if (typeof slug !== 'string' || !slug) return
  const { ...rest } = route.query
  delete rest.practice
  router.replace({ query: rest }).catch(() => {})
  await startQuiz({ slug, title: slug, has_quiz: true } as Recommendation)
}
watch(() => route.query.practice, (v) => {
  if (v) maybeOpenPracticeFromQuery()
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  timelineObserver?.disconnect()
  timelineObserver = null
  cancelAnimationFrame(animRAF)
})
</script>

<style scoped>
/* 单屏星空：无卡片框。左 2/3 星图（占满视口高），右 1/3 数据栏（内部滚动） */
.learning-tab { box-sizing: border-box; height: 100%; min-height: 0; padding: 14px 18px; display: flex; flex-direction: column; gap: 12px; container-type: inline-size; }
.sky { position: relative; flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(0, 1.5fr) minmax(340px, 1fr); gap: 0 18px; animation: learning-enter 0.4s ease both; }
/* 星图始终可见；标题和图例不占用节点阅读空间。 */
.sky-chart { position: relative; min-width: 0; min-height: 0; display: flex; flex-direction: column; }
.sky-map { position: relative; flex: 1; min-height: 340px; display: flex; }
/* 零节点空态：居中引导卡（装饰轨道圆 + 一句话去向 + 去图谱按钮） */
.empty-nodes { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px; padding: 24px; }
.empty-nodes-orb { width: 88px; height: 88px; border-radius: 50%; border: 1px dashed var(--td-component-border, #d8d8d8); position: relative; }
.empty-nodes-orb::before { content: ''; position: absolute; inset: 18px; border-radius: 50%; border: 1px solid var(--td-component-border, #e8e8e8); }
.empty-nodes-orb::after { content: ''; position: absolute; left: 50%; top: 50%; width: 8px; height: 8px; margin: -4px 0 0 -4px; border-radius: 50%; background: var(--td-brand-color, #07c05f); opacity: 0.45; }
.empty-nodes-text { font-size: 13px; color: var(--td-text-color-secondary, #666); text-align: center; max-width: 340px; line-height: 1.7; }
.sky-head { display: flex; flex-direction: column; align-items: flex-start; gap: 6px; padding: 4px 0 8px; }
.sky-head .help-icon { pointer-events: auto; }
.ts-title { font-size: 15px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.ts-line { font-size: 12px; color: var(--td-text-color-secondary, #666); }
.ts-stat { font-size: 12px; color: var(--td-text-color-secondary, #555); white-space: nowrap; }
/* 右：数据栏。滚动区承载统计/档位/待巩固/分组/推荐，底栏固定放时间线/画像放大入口 */
.sky-side { min-width: 0; min-height: 0; display: flex; flex-direction: column; border-left: 1px solid var(--td-component-border, #eee); padding-left: 16px; }
.side-scroll { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 14px; padding-right: 10px; scrollbar-width: thin; }
.side-stats { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; }
.side-block { display: flex; flex-direction: column; gap: 8px; }
.side-title { font-size: 12px; font-weight: 600; color: var(--td-text-color-secondary, #555); display: flex; align-items: center; gap: 6px; }
.side-title::before { content: ''; width: 3px; height: 12px; border-radius: 2px; background: var(--td-brand-color, #07c05f); flex-shrink: 0; }
.side-foot { display: flex; gap: 8px; padding-top: 10px; margin-top: 2px; border-top: 1px solid var(--td-component-border, #eee); }
.side-foot :deep(.t-button) { flex: 1; }
.foot-badge {
  margin-left: 6px;
  background: var(--td-brand-color, #07c05f);
  color: #fff;
  border-radius: 999px;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  font-size: 11px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.sky-state { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; }
/* ② 今日待巩固清单 */
.todo-list { display: flex; flex-direction: column; gap: 6px; }
.todo-row { display: flex; align-items: center; gap: 10px; padding: 7px 10px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s; flex-wrap: wrap; }
.todo-row:hover { border-color: rgba(7, 192, 95, 0.5); }
.todo-tag { font-size: 11px; line-height: 1; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0; }
.todo-detail { font-size: 11px; color: var(--td-text-color-placeholder, #999); white-space: nowrap; }
.todo-actions { margin-left: auto; display: flex; gap: 6px; flex-shrink: 0; }
.todo-title { font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
.rec-hint-line { font-size: 11px; color: var(--td-text-color-placeholder, #999); line-height: 1.4; }
/* 承上启下叙述行：比下档提示略深半档，是卡片的"为什么是它" */
.rec-why-line { font-size: 11px; color: var(--td-text-color-secondary, #666); line-height: 1.5; margin-top: 1px; overflow-wrap: anywhere; }
/* 模块分区卡：并列独立的"1"，悬停联动星图高亮 */
.zone-card-list { display: flex; flex-direction: column; gap: 8px; }
.zone-card { position: relative; border: 1px solid var(--td-component-border, #eee); border-radius: 10px; padding: 8px 10px; display: flex; flex-direction: column; gap: 5px; transition: border-color 0.15s, box-shadow 0.15s; }
.zone-card:hover, .zone-card.hot { border-color: rgba(7, 192, 95, 0.55); box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.08); }
/* 「从这开始」：第一张可学卡的唯一入口标记——多模块时只需跟着它走 */
.zone-card.first-pick { border-color: rgba(7, 192, 95, 0.7); box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.12); }
.zone-start-badge {
  flex: none; font-size: 10px; font-weight: 700; line-height: 1;
  padding: 3px 6px; border-radius: 999px; white-space: nowrap;
  background: #07c05f; color: #fff;
}
.zone-card-head { display: flex; align-items: center; gap: 8px; }
.zone-card-name { font-size: 12px; font-weight: 600; color: var(--td-text-color-primary, #333); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.zone-card-count { margin-left: auto; font-size: 11px; color: var(--td-text-color-placeholder, #999); white-space: nowrap; cursor: help; }
/* 折叠态游标行：序号来自学习路径（与展开视图同一套编号），档位徽标紧贴
   本行节点，「当前」徽标标出推荐游标；视觉与展开态高亮行一致。 */
.zone-cursor { display: flex; align-items: center; gap: 6px; cursor: pointer; flex-wrap: wrap; padding: 3px 4px; border-radius: 6px; background: rgba(7, 192, 95, 0.08); }
.zone-cursor:hover { background: rgba(7, 192, 95, 0.14); }
.zone-cursor-mark { flex: none; font-size: 12px; color: #049b38; font-weight: 700; line-height: 1; }
.zone-cursor-order { flex: none; font-size: 12px; color: #049b38; font-weight: 700; min-width: 16px; text-align: center; }
.zone-cursor-title { font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
/* 档位徽标：紧贴所属行（游标行与路径行共用），百分比变化让进度可见 */
.zone-next-tier { flex: none; font-size: 10px; line-height: 1; padding: 3px 6px; border-radius: 999px; white-space: nowrap; }
.zone-next-hint { font-size: 11px; color: var(--td-text-color-secondary, #555); line-height: 1.5; padding-left: 20px; }
/* 自评/跳过入口：文字按钮（黑点图标无人能懂）；行式布局不再悬浮遮内容 */
.zone-actions-row { display: flex; justify-content: flex-end; }
.zone-path-row-togglehost { display: flex; justify-content: flex-end; margin-top: 2px; }
.zone-path-toggle {
  display: inline-flex; align-items: center;
  height: 20px; padding: 0 8px;
  border-radius: 999px; border: 1px solid var(--td-component-border, #e5e5e5);
  background: transparent; font-size: 10px;
  color: var(--td-text-color-secondary, #666); cursor: pointer;
}
.zone-path-toggle:hover { border-color: rgba(7, 192, 95, 0.5); background: var(--td-brand-color-light, rgba(7, 192, 95, 0.1)); color: #049b38; }
/* 展开的完整学习路径：连续序号 + 档位批注，学到哪一眼可追 */
.zone-path { display: flex; flex-direction: column; gap: 2px; border-top: 1px dashed var(--td-component-border, #eee); padding-top: 6px; }
.zone-path-cap { font-size: 10px; color: var(--td-text-color-placeholder, #aaa); margin-bottom: 1px; }
.zone-path-legend { font-size: 10px; color: var(--td-text-color-placeholder, #aaa); margin-bottom: 4px; }
.zone-path-row { display: flex; align-items: center; gap: 6px; cursor: pointer; padding: 2px 2px; border-radius: 6px; }
.zone-path-row:hover { background: var(--td-brand-color-light, rgba(7, 192, 95, 0.07)); }
.zone-path-row.current { background: rgba(7, 192, 95, 0.1); }
.zone-path-row.skipped .zone-path-title { color: var(--td-text-color-placeholder, #aaa); text-decoration: line-through; }
.zone-path-mark { flex: none; font-size: 11px; color: var(--td-text-color-placeholder, #999); font-weight: 600; min-width: 16px; text-align: center; }
.zone-path-row.current .zone-path-mark { color: #049b38; }
.zone-path-title { font-size: 12px; color: var(--td-text-color-primary, #333); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
.zone-path-now { flex: none; font-size: 9px; line-height: 1; padding: 2px 5px; border-radius: 999px; background: #07c05f; color: #fff; white-space: nowrap; }
/* 节点级目标徽标：文字优先（✓/⚠/⟳/◐/—），颜色仅辅助 */
.zone-obj-badge { flex: none; font-size: 9px; line-height: 1; padding: 2px 5px; border-radius: 4px; white-space: nowrap; font-weight: 500; }
.zone-obj-badge.obj-badge-verified { background: #e8f5e9; color: #1b5e20; }
.zone-obj-badge.obj-badge-conflicting { background: #ffebee; color: #b71c1c; }
.zone-obj-badge.obj-badge-stale { background: #fff3e0; color: #e65100; }
.zone-obj-badge.obj-badge-partial { background: #e3f2fd; color: #0d47a1; }
.zone-obj-badge.obj-badge-unverified { background: #f5f5f5; color: #757575; }
/* 节点级四维度文字：接触/自述/最后验证/下一步建议 */
.zone-node-detail {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 8px;
  padding: 0 18px;
  margin-top: -2px;
  margin-bottom: 2px;
}
.node-detail-item {
  font-size: 10px;
  color: var(--td-text-color-placeholder, #999);
  white-space: nowrap;
}
.node-detail-item.contact { color: #4b9bd8; }
.node-detail-item.no-contact { color: var(--td-text-color-placeholder, #aaa); }
.node-detail-item.self-report { color: #7b1fa2; }
.node-detail-item.last-verified { color: #049b38; }
.node-detail-item.next-step { color: var(--td-text-color-secondary, #666); }
.zone-esa-toggle {
  display: inline-flex; align-items: center;
  height: 20px; padding: 0 8px;
  border-radius: 999px; border: 1px solid var(--td-component-border, #e5e5e5);
  background: transparent; font-size: 10px;
  color: var(--td-text-color-placeholder, #999); cursor: pointer;
}
.zone-esa-toggle:hover { border-color: rgba(7, 192, 95, 0.5); background: var(--td-brand-color-light, rgba(7, 192, 95, 0.1)); color: #049b38; }
.zone-esa-toggle:focus-visible { outline: 2px solid var(--td-brand-color, #07c05f); }
.zone-esa { display: flex; flex-direction: column; gap: 4px; }
.zone-esa .esa-buttons { display: flex; gap: 6px; flex-wrap: wrap; }
.zone-esa .esa-note { font-size: 11px; color: var(--td-text-color-placeholder, #999); line-height: 1.5; }
.zone-done { font-size: 11px; color: var(--td-text-color-placeholder, #999); }
/* 分组进度：右栏内轻量折叠 */
.units-compact { border-top: 1px dashed var(--td-component-border, #eee); padding-top: 8px; }
/* 「不确定」选项：视觉上明确它不是第 5 个答案 */
.quiz-option.unsure { border-style: dashed; }
.quiz-option.unsure .option-key { border-style: dashed; }
.quiz-option.unsure.selected { border-color: #ed7b2f; background: rgba(237, 155, 47, 0.08); border-style: solid; }
.quiz-result.unsure { background: rgba(237, 155, 47, 0.07); }
@keyframes learning-enter {
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .sky { animation: none; }
  .unit-bar-fill { transition: none; }
}
.help-icon { color: var(--td-text-color-placeholder, #999); cursor: help; display: inline-flex; align-items: center; }
.help-icon:hover { color: var(--td-brand-color, #07c05f); }
.meta-mastered { color: #038626; font-weight: 500; }
.meta-tiers { display: flex; gap: 8px; flex-wrap: wrap; }
.tier-stat { display: inline-flex; align-items: center; gap: 6px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; padding: 5px 10px; font-size: 12px; background: var(--td-bg-color-container, #fff); cursor: pointer; transition: border-color 0.15s, box-shadow 0.15s; font-family: inherit; }
.tier-stat:hover { border-color: var(--td-brand-color-focus); }
.tier-stat.active { border-color: var(--td-brand-color, #07c05f); box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.12); }
.tier-stat.zero { opacity: 0.55; }
.tier-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.tier-name { color: var(--td-text-color-secondary, #666); }
.tier-num { font-weight: 600; color: var(--td-text-color-primary, #333); }
.tier-detail { margin-top: 10px; display: flex; flex-wrap: wrap; gap: 6px; max-height: 120px; overflow-y: auto; }
.tier-node { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; border: 1px solid var(--td-component-border, #eee); border-radius: 999px; padding: 3px 10px; cursor: pointer; transition: border-color 0.15s; }
.tier-node.is-skipped { border-style: dashed; opacity: 0.85; }
.tier-node-skip { font-size: 10px; line-height: 1; padding: 2px 6px; border-radius: 4px; background: rgba(123, 139, 161, 0.14); color: #5a6c80; }
.tier-restore { font-size: 10px; line-height: 1; padding: 3px 8px; border-radius: 4px; border: 1px solid var(--td-component-border, #eee); background: var(--td-bg-color-container, #fff); color: var(--td-text-color-secondary, #666); cursor: pointer; font-family: inherit; }
.tier-restore:hover { border-color: rgba(7, 192, 95, 0.5); color: #049b38; }
/* 自认定待确认但题未就绪：与普通下档提示区分的等待文案 */
.rec-hint-line.sv-pending { color: #8d77e8; }
.tier-node:focus-visible, .todo-row:focus-visible, .rec-mini:focus-visible, .sa-option:focus-visible { outline: 2px solid var(--td-brand-color, #07c05f); outline-offset: 1px; }
.tier-node:hover { border-color: var(--td-brand-color, #07c05f); color: #038626; }
.tier-node-title { max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tier-node-meta { color: var(--td-text-color-placeholder, #999); font-size: 11px; }
.howto { max-width: 360px; display: flex; flex-direction: column; gap: 6px; }
.howto-title { font-weight: 600; }
.howto-line { font-size: 12px; color: var(--td-text-color-secondary, #555); line-height: 1.6; }
.units-title { font-size: 12px; color: var(--td-text-color-placeholder, #999); display: flex; align-items: center; gap: 4px; cursor: pointer; user-select: none; }
.units-chevron { transition: transform 0.2s; }
.units-chevron.collapsed { transform: rotate(0deg); }
.units-chevron:not(.collapsed) { transform: rotate(90deg); }
.units-body { display: flex; flex-direction: column; gap: 6px; max-height: 150px; overflow-y: auto; }
.unit-row { display: flex; align-items: center; gap: 8px; font-size: 12px; }
.unit-name { width: 84px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--td-text-color-secondary, #666); flex-shrink: 0; }
.unit-bar { flex: 1; height: 6px; border-radius: 3px; background: var(--td-bg-color-component, #f0f0f0); overflow: hidden; }
.unit-bar-fill { height: 100%; background: var(--td-brand-color, #07c05f); border-radius: 3px; transition: width 0.4s ease; }
.unit-count { color: var(--td-text-color-secondary, #666); flex-shrink: 0; }
.section-empty { color: var(--td-text-color-placeholder, #999); font-size: 13px; }
/* 窄栏推荐紧凑行：标题/元信息/提示三行内聚，单按钮操作（整行点击=打开页面） */
.rec-mini-list { display: flex; flex-direction: column; gap: 8px; }
/* 岗位覆盖计划：推荐按目录分组（units-title 同款小标题），组内行结构不变 */
.role-plan-intro { display: flex; align-items: baseline; gap: 6px; flex-wrap: wrap; font-size: 11px; color: var(--td-text-color-placeholder, #999); line-height: 1.5; }
.rec-start { font-size: 10px; line-height: 1; padding: 2px 6px; border-radius: 4px; background: rgba(2, 106, 46, 0.12); color: #026a2e; white-space: nowrap; }
.rec-folder { font-size: 10px; line-height: 1; padding: 2px 6px; border-radius: 4px; background: rgba(0, 0, 0, 0.04); color: var(--td-text-color-placeholder, #999); white-space: nowrap; max-width: 96px; overflow: hidden; text-overflow: ellipsis; }
.role-plan-name { color: var(--td-text-color-secondary, #555); font-weight: 500; white-space: nowrap; }
.rec-mini { display: flex; align-items: flex-start; gap: 8px; padding: 8px 10px; border-radius: 8px; cursor: pointer; transition: background-color 0.15s; }
.rec-mini:hover { background: rgba(7, 192, 95, 0.05); }
.rec-mini.featured {
  background: radial-gradient(140% 160% at 0% 0%, rgba(7, 192, 95, 0.1), transparent 60%);
  border: 1px solid rgba(7, 192, 95, 0.25);
}
.rec-mini.featured .rec-mini-name { font-weight: 600; font-size: 14px; }
.rec-mini.featured .rec-rank { background: rgba(7, 192, 95, 0.14); color: #049b38; }
.rec-mini-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.rec-mini-title { display: flex; align-items: center; gap: 6px; min-width: 0; }
.rec-mini-name { font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rec-mini-meta { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; row-gap: 5px; }
.rec-mini-actions { display: flex; flex-direction: column; gap: 6px; flex-shrink: 0; align-items: stretch; }
/* 行内操作按钮给足最小宽度：窄栏下「练一练」三个字不再被压扁换行 */
.rec-mini-actions :deep(.t-button), .todo-actions :deep(.t-button) { min-width: 68px; justify-content: center; }
.rec-rank { width: 20px; height: 20px; border-radius: 50%; background: var(--td-bg-color-component, #f0f0f0); color: var(--td-text-color-secondary, #666); font-size: 11px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; }
.rec-rank.top { background: rgba(7, 192, 95, 0.12); color: #049b38; }
.rec-type-badge { font-size: 11px; line-height: 1; padding: 2px 6px; border-radius: 4px; flex-shrink: 0; }
.rec-type-badge.entity { background: rgba(43, 164, 113, 0.1); color: #2ba471; }
.rec-type-badge.concept { background: rgba(227, 115, 24, 0.1); color: #e37318; }
.rec-reason-tag { font-size: 11px; line-height: 1; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0; }
/* 推荐卡：档位内进度条（快变量）与下档提示 */
.rec-progress { display: inline-flex; align-items: center; }
.rec-progress-track { display: inline-block; width: 60px; height: 4px; border-radius: 2px; background: var(--td-bg-color-component, #f0f0f0); overflow: hidden; }
.rec-progress-fill { display: block; height: 100%; border-radius: 2px; transition: width 0.4s ease; }
.rec-hint-line { font-size: 11px; color: var(--td-text-color-placeholder, #999); line-height: 1.4; }
.explain { max-width: 320px; display: flex; flex-direction: column; gap: 6px; }
.explain-title { font-weight: 600; }
.explain-line { font-size: 12px; color: var(--td-text-color-secondary, #555); line-height: 1.6; }
.explain-warn { color: #b8860b; }
.explain-improve { color: #049b38; }
.explain-self-assess { margin-top: 8px; padding-top: 8px; border-top: 1px dashed var(--td-component-border, #eee); }
.esa-label { font-size: 11px; color: var(--td-text-color-placeholder, #999); display: block; margin-bottom: 6px; }
.esa-buttons { display: flex; gap: 6px; flex-wrap: wrap; }
.esa-btn { font-size: 11px; line-height: 1; padding: 5px 10px; border-radius: 4px; cursor: pointer; font-family: inherit; border: 1px solid var(--td-component-border, #eee); background: var(--td-bg-color-container, #fff); color: var(--td-text-color-secondary, #666); transition: all 0.15s; }
.esa-btn.up:hover { border-color: var(--td-brand-color, #07c05f); color: #049b38; }
.esa-btn.down:hover { border-color: #7b61ff; color: #7b61ff; }
/* 已掌握（移出推荐）：灰蓝实底 hover，与另两轨同权重但低饱和 */
.esa-btn.known:hover { border-color: #7b8ba1; color: #4d5f72; background: rgba(123, 139, 161, 0.08); }
.esa-note { font-size: 11px; color: var(--td-text-color-placeholder, #999); margin-top: 4px; line-height: 1.5; }
.sa-dialog { display: flex; flex-direction: column; gap: 8px; }
.sa-node { font-size: 13px; font-weight: 600; margin-bottom: 2px; }
.sa-option { display: flex; gap: 10px; padding: 8px 10px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s; }
.sa-option:hover { border-color: rgba(123, 97, 255, 0.5); }
.sa-option.selected { border-color: #7b61ff; background: rgba(123, 97, 255, 0.06); }
.sa-radio { width: 14px; height: 14px; border-radius: 50%; border: 1.5px solid var(--td-component-border, #ccc); flex-shrink: 0; margin-top: 2px; }
.sa-option.selected .sa-radio { border-color: #7b61ff; background: radial-gradient(circle, #7b61ff 40%, transparent 45%); }
.sa-option-label { font-size: 13px; font-weight: 500; }
.sa-option-hint { font-size: 11px; color: var(--td-text-color-placeholder, #999); margin-top: 2px; }
.rec-level.faded { border: 1px dashed #7b8ba1; }
/* rec-level 从 span 升级为原生 button（解释层 click 触发的键盘前提）：
   继承原视觉，只补按钮基线重置 */
.rec-level { display: inline-flex; align-items: center; gap: 5px; font-size: 11px; line-height: 1; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0; border: 1px solid transparent; font-family: inherit; cursor: pointer; }
.rec-level:focus-visible { outline: 2px solid var(--td-brand-color, #07c05f); outline-offset: 1px; }
.quiz-question { font-weight: 500; margin-bottom: 10px; }
.quiz-options { display: flex; flex-direction: column; gap: 6px; margin-bottom: 10px; }
.quiz-option { display: flex; gap: 10px; align-items: center; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; padding: 8px 10px; cursor: pointer; transition: border-color 0.15s, background-color 0.15s; }
.quiz-option:hover { border-color: rgba(7, 192, 95, 0.5); }
.quiz-option.selected { border-color: var(--td-brand-color, #07c05f); background: rgba(7, 192, 95, 0.06); }
.quiz-option.correct { border-color: #2ba471; background: rgba(43, 164, 113, 0.08); }
.quiz-option.wrong { border-color: #d54941; background: rgba(213, 73, 65, 0.08); }
.option-key { width: 22px; height: 22px; border-radius: 50%; border: 1px solid currentColor; display: inline-flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; }
.quiz-footer { display: flex; gap: 8px; align-items: center; }
.quiz-progress { font-size: 12px; color: var(--td-text-color-placeholder, #999); }
.quiz-result { margin-top: 10px; padding: 10px 12px; border-radius: 8px; background: rgba(213, 73, 65, 0.06); }
.quiz-result.ok { background: rgba(43, 164, 113, 0.08); }
.result-line { font-weight: 600; margin-bottom: 4px; }
.result-level { font-size: 12px; color: #049b38; margin-bottom: 4px; font-weight: 500; }
.result-schedule { font-size: 12px; color: #0052d9; margin-bottom: 4px; }
.result-explanation { font-size: 13px; color: var(--td-text-color-secondary, #555); }
.result-refs { margin-top: 6px; font-size: 11px; color: var(--td-text-color-placeholder, #999); display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.result-refs-label { flex-shrink: 0; }
.result-ref-link { color: var(--td-brand-color, #07c05f); text-decoration: none; border-bottom: 1px dashed rgba(7, 192, 95, 0.5); }
.result-ref-link:hover { color: #038626; border-bottom-style: solid; }
.result-ref-count { color: var(--td-text-color-placeholder, #999); font-size: 10px; margin-left: 1px; }
.quiz-kbd-hint { margin-left: auto; font-size: 11px; color: var(--td-text-color-placeholder, #999); }
.error-text { color: var(--td-text-color-secondary, #666); font-size: 13px; }
.kept-note { font-size: 12px; color: var(--td-text-color-placeholder, #999); margin-bottom: 10px; }
.collect-note { font-size: 12px; color: #8a6116; background: rgba(237, 155, 47, 0.1); border-radius: 6px; padding: 7px 10px; margin-bottom: 10px; }
.collect-banner {
  font-size: 13px;
  color: #8a6116;
  background: rgba(237, 155, 47, 0.12);
  border: 1px solid rgba(237, 155, 47, 0.3);
  border-radius: 8px;
  padding: 9px 14px;
}
/* 外层定位框：竖线装饰贴在框上，不随内容滚动；框高由内层滚动区决定 */
.timeline-box { position: relative; }
.timeline-box::before { content: ''; position: absolute; left: 3.5px; top: 8px; bottom: 8px; width: 1px; background: var(--td-component-stroke, #e7e7e7); }
/* 内层滚动区：固定可视 12 行（行高 24px + 行距 8px），最新在顶部，向下滚动翻阅 */
.timeline-list { display: flex; flex-direction: column; gap: 8px; max-height: calc(12 * 24px + 11 * 8px + 2 * 22px); overflow-y: auto; }
.timeline-item { display: flex; align-items: center; gap: 8px; font-size: 12px; min-height: 24px; }
.tl-group-header { font-size: 11px; color: var(--td-text-color-placeholder, #999); padding-left: 18px; height: 22px; display: flex; align-items: center; font-weight: 500; }
.tl-dot { width: 8px; height: 8px; border-radius: 50%; background: #4b9bd8; flex-shrink: 0; position: relative; z-index: 1; box-shadow: 0 0 0 2px var(--td-bg-color-container, #fff); }
.tl-dot.ev-answer_cite { background: #4b9bd8; }
.tl-dot.ev-cross_ref { background: #07c05f; }
.tl-dot.ev-re_ask { background: #ed7b2f; }
.tl-dot.ev-topic_signal { background: #a6a6a6; }
.tl-dot.ev-quiz_correct { background: #049b38; box-shadow: 0 0 0 2px var(--td-bg-color-container, #fff), 0 0 0 3px rgba(4, 155, 56, 0.35); }
.tl-dot.ev-quiz_wrong { background: #e34d59; }
.tl-dot.ev-quiz_unsure { background: transparent; border: 1.5px dashed #ed7b2f; box-shadow: none; width: 7px; height: 7px; }
.tl-dot.ev-backfill_cite { background: transparent; border: 1.5px dashed #a6a6a6; box-shadow: none; width: 7px; height: 7px; }
.tl-dot.ev-self_assess_up { background: #0052d9; box-shadow: 0 0 0 2px var(--td-bg-color-container, #fff), 0 0 0 3px rgba(0, 82, 217, 0.3); }
.tl-dot[class*="ev-self_assess_down"] { background: #7b61ff; box-shadow: none; width: 7px; height: 7px; }
/* 事件类型列：min/max 宽度 + 单行省略——"自评降档·题目效度"这类长标签在
   固定 72px 下会折成两行，而行高固定 24px 导致与下一行叠压 */
.tl-type {
  color: var(--td-text-color-secondary, #666);
  min-width: 72px; max-width: 112px; flex-shrink: 0;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.tl-badge {
  flex-shrink: 0;
  font-size: 11px;
  line-height: 1;
  padding: 2px 6px;
  border-radius: 4px;
  background: rgba(0, 82, 217, 0.08);
  color: #0052d9;
}
.tl-badge-entity { background: rgba(43, 164, 113, 0.1); color: #2ba471; }
.tl-badge-concept { background: rgba(227, 115, 24, 0.1); color: #e37318; }
.tl-slug { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tl-time { color: var(--td-text-color-placeholder, #999); flex-shrink: 0; }
.tl-filters { display: inline-flex; gap: 4px; flex-wrap: wrap; margin-bottom: 10px; }
.tl-filter { font-size: 11px; line-height: 1; padding: 4px 9px; border-radius: 999px; border: 1px solid var(--td-component-border, #eee); background: var(--td-bg-color-container, #fff); color: var(--td-text-color-secondary, #666); cursor: pointer; transition: all 0.15s; font-family: inherit; }
.tl-filter:hover { border-color: rgba(7, 192, 95, 0.5); }
.tl-filter.active { background: rgba(7, 192, 95, 0.1); border-color: rgba(7, 192, 95, 0.5); color: #049b38; }
.tl-node-filters { display: flex; flex-wrap: wrap; gap: 5px; margin-bottom: 10px; max-height: 84px; overflow-y: auto; }
.tl-node-chip {
  display: inline-flex; align-items: center; gap: 5px; font-size: 11px; line-height: 1;
  padding: 4px 9px; border-radius: 999px; border: 1px solid var(--td-component-border, #eee);
  background: var(--td-bg-color-container, #fff); color: var(--td-text-color-secondary, #666);
  cursor: pointer; transition: all 0.15s; font-family: inherit; max-width: 180px;
}
.tl-node-chip:hover { border-color: rgba(7, 192, 95, 0.5); }
.tl-node-chip.active { background: rgba(7, 192, 95, 0.12); border-color: var(--td-brand-color, #07c05f); color: #049b38; }
.tl-node-count { font-size: 10px; color: var(--td-text-color-placeholder, #999); }
.profile-scope-note { font-size: 12px; color: var(--td-text-color-placeholder, #999); margin-bottom: 6px; }
.profile-summary { font-size: 12px; color: var(--td-text-color-placeholder, #999); margin-bottom: 10px; }
.profile-row { display: flex; align-items: center; gap: 10px; }
.profile-label { font-size: 13px; }
.spacer { flex: 1; }
.timeline-end { display: flex; justify-content: center; padding: 8px 0; }
.timeline-all-loaded {
  text-align: center;
  color: var(--td-text-color-placeholder, #999);
  font-size: 12px;
  padding: 6px 0 2px;
}
.delete-body { display: flex; flex-direction: column; gap: 12px; font-size: 13px; line-height: 1.6; }
.delete-optout { display: flex; align-items: center; gap: 8px; cursor: pointer; color: var(--td-text-color-secondary, #555); }
@container (max-width: 780px) {
  /* 按实际内容宽度换行，侧栏或分屏打开时也保留星图。 */
  .sky { display: flex; flex-direction: column; flex: none; gap: 16px; }
  .sky-chart { min-height: 0; }
  .sky-map { height: clamp(450px, calc(100cqw + 110px), 680px); flex: none; }
  .sky-side { border-left: none; padding-left: 0; border-top: 1px solid var(--td-component-border, #eee); padding-top: 12px; }
  .side-scroll { overflow-y: visible; flex: none; padding-right: 0; }
}
@media (max-width: 640px) {
  .todo-row { flex-wrap: wrap; }
  .todo-actions { margin-left: 0; width: 100%; justify-content: flex-start; }
  .rec-mini-actions { flex-direction: row; }
  .timeline-item { flex-wrap: wrap; height: auto; }
  .tl-slug { min-width: 0; max-width: 180px; }
}
</style>
