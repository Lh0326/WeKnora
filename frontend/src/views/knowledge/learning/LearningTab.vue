<template>
  <div class="learning-tab">
    <!-- 停采全局横幅：一进页签就能看到，避免"答了题却不亮"的困惑 -->
    <div v-if="collectDisabled && !initialLoading" class="collect-banner">
      {{ $t('knowledgeEditor.learningTab.collectOnNote') }}
    </div>
    <!-- 头部：进度概览仪表盘 -->
    <div class="learning-header" v-if="initialLoading">
      <t-loading size="large" style="margin: 40px auto; display: block;" />
    </div>
    <!-- 错误态与空态分离：网络/服务失败给重试入口，绝不伪装成"没有数据" -->
    <div class="learning-header learning-header-error" v-else-if="loadFailed">
      <div class="error-body">
        <div class="error-text">{{ $t('knowledgeEditor.learningTab.loadError') }}</div>
        <t-button size="small" variant="outline" @click="refresh()">{{ $t('knowledgeEditor.learningTab.retry') }}</t-button>
      </div>
    </div>
    <div class="learning-header" v-else>
      <div class="learning-progress-ring">
        <svg width="104" height="104" viewBox="0 0 104 104">
          <circle cx="52" cy="52" r="44" fill="none" stroke="var(--td-bg-color-component, #f0f0f0)" stroke-width="8" />
          <circle cx="52" cy="52" r="44" fill="none" stroke="var(--td-brand-color, #07c05f)" stroke-width="8"
            :stroke-dasharray="ringDash" stroke-linecap="round" transform="rotate(-90 52 52)"
            style="transition: stroke-dasharray 0.6s ease;" />
          <text x="52" y="50" text-anchor="middle" class="ring-text">{{ displayLit }}</text>
          <text x="52" y="64" text-anchor="middle" class="ring-sub">/ {{ progress?.total_nodes ?? 0 }}</text>
        </svg>
      </div>
      <div class="learning-progress-meta">
        <div class="meta-title">
          {{ $t('knowledgeEditor.learningTab.title') }}
          <t-popup trigger="hover" placement="bottom-left" show-arrow :overlay-style="{ maxWidth: '380px' }">
            <template #content>
              <div class="howto">
                <div class="howto-title">{{ $t('knowledgeEditor.learningTab.howComputedTitle') }}</div>
                <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedSignals') }}</div>
                <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedDecay') }}</div>
                <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedTiers') }}</div>
              </div>
            </template>
            <span class="help-icon"><HelpCircleIcon size="15" /></span>
          </t-popup>
        </div>
        <div class="meta-line" v-if="progress?.total_nodes">
          {{ $t('knowledgeEditor.learningTab.litOf', { lit: displayLit, total: progress.total_nodes }) }}
          <span v-if="masteredCount" class="meta-mastered">· {{ $t('knowledgeEditor.learningTab.masteredOf', { n: masteredCount }) }}</span>
        </div>
        <div class="meta-line" v-else>{{ $t('knowledgeEditor.learningTab.noNodes') }}</div>
        <!-- 四档统计卡片：点击展开该档节点清单 -->
        <div class="meta-tiers">
          <button v-for="tier in tierOrder" :key="tier.key" class="tier-stat"
            :class="{ active: tierExpanded === tier.key, zero: (progress?.levels?.[tier.key] ?? 0) === 0 }"
            :title="tier.hint" @click="toggleTier(tier.key)">
            <span class="tier-dot" :style="{ background: tier.color }"></span>
            <span class="tier-name">{{ tier.label }}</span>
            <span class="tier-num">{{ tierDisplay(tier.key) }}</span>
          </button>
        </div>
        <div v-if="tierExpanded" class="tier-detail">
          <div v-if="tierListLoading" class="section-empty">{{ $t('common.loading') }}</div>
          <div v-else-if="!tierList.length" class="section-empty">{{ $t('knowledgeEditor.learningTab.tierListEmpty') }}</div>
          <template v-else>
            <span v-for="m in tierList" :key="m.slug" class="tier-node" :title="m.slug" @click="openPage(m.slug)">
              <span v-if="tierExpanded === 'unseen' && m.evidence_count > 0" class="tier-dot" style="background: #d4a017;"></span>
              <span class="tier-node-title">{{ titleOf(m.slug, m.title) }}</span>
              <span class="tier-node-meta">{{ m.evidence_count }}{{ m.low_confidence ? '?' : '' }}</span>
            </span>
          </template>
        </div>
      </div>
      <div class="learning-units" v-if="units.length">
        <div class="units-title" @click="unitsCollapsed = !unitsCollapsed">
          {{ $t('knowledgeEditor.learningTab.unitsTitle') }}
          <ChevronRightIcon size="13" class="units-chevron" :class="{ collapsed: unitsCollapsed }" />
        </div>
        <div v-show="!unitsCollapsed" class="units-body">
          <div v-for="u in units" :key="u.folder_id" class="unit-row">
            <span class="unit-name" :title="u.folder_name || u.folder_id">{{ u.folder_name || $t('knowledgeEditor.learningTab.rootUnit') }}</span>
            <div class="unit-bar">
              <div class="unit-bar-fill" :style="{ width: (unitPercent(u) * animT) + '%' }"></div>
            </div>
            <span class="unit-count">{{ u.lit }}/{{ u.total }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 推荐列表 -->
    <div class="learning-section">
      <div class="section-title">{{ $t('knowledgeEditor.learningTab.recommendTitle') }}</div>
      <div v-if="recommendLoading" class="section-empty">{{ $t('common.loading') }}</div>
      <div v-else-if="!recommendations.length" class="section-empty">{{ $t('knowledgeEditor.learningTab.recommendEmpty') }}</div>
      <div v-else class="recommend-list">
        <div v-for="(rec, idx) in recommendations" :key="rec.slug" class="recommend-card" @click="openPage(rec.slug)">
          <span class="rec-rank" :class="{ top: idx === 0 }">{{ idx + 1 }}</span>
          <div class="rec-main">
            <div class="rec-title-line">
              <span v-if="pageTypeOf(rec.slug)" class="rec-type-badge" :class="pageTypeOf(rec.slug)">{{ pageTypeText(pageTypeOf(rec.slug)) }}</span>
              <span class="rec-title" :title="rec.slug">{{ rec.title || rec.slug }}</span>
              <span v-if="rec.folder_name" class="rec-folder"><FolderIcon size="13" />{{ rec.folder_name }}</span>
            </div>
            <div class="rec-reason-line">
              <span class="rec-reason-tag" :style="reasonStyle(rec.reason)">{{ reasonText(rec.reason) }}</span>
              <!-- 档位徽标：悬停展示量化归因（有效掌握度/证据构成/如何提升） -->
              <t-popup trigger="hover" placement="bottom" show-arrow :overlay-style="{ maxWidth: '340px' }">
                <template #content>
                  <div class="explain">
                    <div class="explain-title">{{ $t('knowledgeEditor.learningTab.explainTitle') }}</div>
                    <div class="explain-line">{{ $t('knowledgeEditor.learningTab.explainPEff', { p: Math.round((rec.p_eff ?? 0) * 100) }) }}（{{ $t('knowledgeEditor.learningTab.explainThresholds') }}）</div>
                    <div class="explain-line">{{ $t('knowledgeEditor.learningTab.explainEvidence', { pos: rec.positive_count ?? 0, neg: rec.negative_count ?? 0 }) }}</div>
                    <div v-if="rec.faded" class="explain-line explain-warn">{{ $t('knowledgeEditor.learningTab.explainFaded') }}</div>
                    <div class="explain-line explain-improve">{{ $t('knowledgeEditor.learningTab.explainImprove') }}</div>
                  </div>
                </template>
                <span v-if="rec.level" class="rec-level" :style="levelStyle(rec.level, rec.faded)"
                  :class="{ faded: rec.faded }">
                  <span class="tier-dot" :style="{ background: fadedColor(rec) }"></span>{{ levelLabel(rec.level, rec.faded) }}
                </span>
              </t-popup>
            </div>
          </div>
          <div class="rec-actions" @click.stop>
            <t-button size="small" variant="outline" @click="openPage(rec.slug)">
              {{ $t('knowledgeEditor.learningTab.openPage') }}<ChevronRightIcon size="14" style="margin-left: 2px;" />
            </t-button>
            <!-- 图谱直达：跳到以该节点为中心的局部视图，环色即掌握档 -->
            <t-button size="small" variant="outline" @click="openInGraph(rec.slug)">
              {{ $t('knowledgeEditor.learningTab.inGraph') }}
            </t-button>
            <t-button v-if="rec.has_quiz" size="small" @click="startQuiz(rec)">
              {{ rec.quiz_count ? $t('knowledgeEditor.learningTab.practiceN', { n: rec.quiz_count }) : $t('knowledgeEditor.learningTab.practice') }}
            </t-button>
          </div>
        </div>
      </div>
    </div>

    <!-- 遗忘动态：非用户操作导致的被动变化，读时派生、与时间线（主动事件）
         分栏——Anki 的"到期队列 vs 复习历史"分区：被动信息永不刷掉主动记录 -->
    <div class="learning-section" v-if="passiveChanges && (passiveChanges.demoted_count > 0 || passiveChanges.due_soon_count > 0)">
      <div class="section-title">{{ $t('knowledgeEditor.learningTab.changesTitle') }}</div>
      <div class="changes-badges">
        <span v-if="passiveChanges.demoted_count > 0" class="changes-badge demoted">
          {{ $t('knowledgeEditor.learningTab.changesDemoted', { n: passiveChanges.demoted_count }) }}
        </span>
        <span v-if="passiveChanges.due_soon_count > 0" class="changes-badge due">
          {{ $t('knowledgeEditor.learningTab.changesDue', { n: passiveChanges.due_soon_count }) }}
        </span>
      </div>
      <div class="changes-list">
        <div v-for="ch in passiveChanges.items" :key="ch.slug" class="changes-row" @click="openPage(ch.slug)">
          <span class="rec-title" :title="ch.slug">{{ ch.title || ch.slug }}</span>
          <span class="changes-tier">
            <span class="tier-dot" :style="{ background: tierColor(ch.anchor_level) }"></span>{{ levelLabel(ch.anchor_level) }}
            <span class="changes-arrow">→</span>
            <span class="tier-dot" :style="{ background: tierColor(ch.view_level) }"></span>{{ levelLabel(ch.view_level) }}
          </span>
          <span class="changes-p">{{ Math.round(ch.base_p * 100) }}% → {{ Math.round(ch.p_eff * 100) }}%</span>
          <span class="changes-hint" :class="{ overdue: ch.demoted }">
            {{ ch.demoted
              ? $t('knowledgeEditor.learningTab.changesIdle', { d: Math.round(ch.days_idle) })
              : $t('knowledgeEditor.learningTab.changesNext', { n: Math.ceil(ch.next_review_days ?? 0) }) }}
          </span>
        </div>
      </div>
    </div>

    <!-- 测验答题卡 -->
    <div class="learning-section" ref="quizSectionRef" v-if="quiz.active">
      <div class="section-title">{{ $t('knowledgeEditor.learningTab.quizTitle', { slug: quiz.title }) }}</div>
      <div v-if="collectDisabled" class="collect-note">
        {{ $t('knowledgeEditor.learningTab.optedOutNote') }}
      </div>
      <div v-if="quiz.loading" class="section-empty">{{ $t('common.loading') }}</div>
      <template v-else-if="quizCurrent">
        <div class="quiz-question">{{ quizCurrent.question }}</div>
        <div class="quiz-options">
          <div v-for="(text, key) in quizCurrent.options" :key="key" class="quiz-option"
            :class="{ selected: quiz.chosen === key, correct: quiz.result !== null && key === quiz.result.correct_key, wrong: quiz.result !== null && quiz.chosen === key && !quiz.result.correct }"
            @click="chooseOption(key)">
            <span class="option-key">{{ key }}</span>
            <span class="option-text">{{ text }}</span>
          </div>
        </div>
        <div class="quiz-footer">
          <t-button size="small" :disabled="!quiz.chosen || quiz.result !== null" @click="submitAnswer">{{ $t('knowledgeEditor.learningTab.submitAnswer') }}</t-button>
          <span v-if="quiz.items.length > 1" class="quiz-progress">{{ quiz.index + 1 }} / {{ quiz.items.length }}</span>
          <t-button v-if="quiz.result !== null && quiz.index < quiz.items.length - 1" size="small" variant="outline" @click="nextQuestion">{{ $t('knowledgeEditor.learningTab.nextQuestion') }}</t-button>
          <t-button size="small" variant="text" @click="closeQuiz">{{ $t('knowledgeEditor.learningTab.closeQuiz') }}</t-button>
          <span class="quiz-kbd-hint">{{ $t('knowledgeEditor.learningTab.kbdHint') }}</span>
        </div>
        <div v-if="quiz.result !== null" class="quiz-result" :class="{ ok: quiz.result.correct }">
          <div class="result-line">{{ quiz.result.correct ? $t('knowledgeEditor.learningTab.answerCorrect') : $t('knowledgeEditor.learningTab.answerWrong') }}</div>
          <div v-if="levelChangeText" class="result-level">{{ levelChangeText }}</div>
          <div v-if="reviewScheduleText" class="result-schedule">{{ reviewScheduleText }}</div>
          <div class="result-explanation">{{ quiz.result.explanation }}</div>
          <div v-if="currentSourceDocs.length" class="result-refs">
            <span class="result-refs-label">{{ $t('knowledgeEditor.learningTab.evidenceSource') }}</span>
            <a v-for="doc in currentSourceDocs" :key="doc.knowledge_id" href="#"
              class="result-ref-link" :title="doc.knowledge_id"
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
    </div>

    <!-- 点亮时间线 -->
    <div class="learning-section">
      <div class="section-title">
        {{ $t('knowledgeEditor.learningTab.timelineTitle') }}
        <span class="tl-filters">
          <button v-for="f in timelineFilters" :key="f.key" class="tl-filter" :class="{ active: timelineFilter === f.key }"
            @click="timelineFilter = f.key">{{ f.label }}</button>
        </span>
      </div>
      <div v-if="initialLoading" class="section-empty"><t-loading size="small" /></div>
      <div v-else-if="!filteredTimeline.length" class="section-empty">{{ $t('knowledgeEditor.learningTab.timelineEmpty') }}</div>
      <template v-else>
        <div class="tl-node-filters">
          <button v-for="c in timelineNodesVisible" :key="c.slug" class="tl-node-chip"
            :class="{ active: selectedNodeSlugs.has(c.slug) }" :title="c.slug"
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
                :title="item.event_type === 're_ask' ? $t('knowledgeEditor.learningTab.reAskHint') : item.slug">
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
    </div>

    <!-- 画像管理 -->
    <div class="learning-section">
      <div class="section-title">{{ $t('knowledgeEditor.learningTab.profileTitle') }}</div>
      <div class="profile-scope-note">{{ $t('knowledgeEditor.learningTab.profileScopeNote') }}</div>
      <div class="profile-summary">{{ profileSummary }}</div>
      <div v-if="collectDisabled" class="collect-note">{{ $t('knowledgeEditor.learningTab.collectOnNote') }}</div>
      <!-- 停采的数据去向说明：Google Activity Controls 模式——pause 不删历史 -->
      <div v-if="collectDisabled && timelineTotal > 0" class="kept-note">
        {{ $t('knowledgeEditor.learningTab.collectKeptNote', { n: timelineTotal }) }}
      </div>
      <div class="profile-row">
        <t-switch v-model="collectDisabled" size="small" @change="onToggleCollect" />
        <span class="profile-label">{{ $t('knowledgeEditor.learningTab.stopCollect') }}</span>
        <span class="spacer"></span>
        <t-button size="small" variant="outline" :loading="exporting" @click="exportProfile">{{ $t('knowledgeEditor.learningTab.export') }}</t-button>
        <t-button size="small" theme="danger" variant="outline" @click="openDeleteDialog">{{ $t('knowledgeEditor.learningTab.delete') }}</t-button>
      </div>
    </div>

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
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Button as TButton, Dialog as TDialog, MessagePlugin, Popup as TPopup, Switch as TSwitch } from 'tdesign-vue-next'
import { ChevronRightIcon, FolderIcon, HelpCircleIcon } from 'tdesign-icons-vue-next'
import {
  getLearningProgress, getLearningRecommend, getLearningQuiz, submitLearningAnswer,
  getLearningTimeline, exportLearningProfile, deleteLearningProfile,
  getLearningSettings, updateLearningSettings, getLearningMastery, getLearningChanges,
  type LearningProgress, type Recommendation, type QuizQuestion, type AnswerResult, type TimelineItem, type MasteryView, type PassiveChangesSummary,
} from '@/api/learning'

const props = defineProps<{ knowledgeBaseId: string }>()
// 出处文档跳转复用知识库页既有的源文档打开通道（WikiBrowser 同款事件）。
const emit = defineEmits<{ (e: 'open-source-doc', knowledgeId: string): void }>()
const router = useRouter()
const { t } = useI18n()

const progress = ref<LearningProgress | null>(null)
const recommendations = ref<Recommendation[]>([])
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
const ringDash = computed(() => {
  const pct = progress.value?.total_nodes ? progress.value.lit_nodes / progress.value.total_nodes : 0
  return `${(pct * animT.value * 276.5).toFixed(1)} 276.5`
})
const displayLit = computed(() => Math.round((progress.value?.lit_nodes ?? 0) * animT.value))
function tierDisplay(key: string): number {
  return Math.round((progress.value?.levels?.[key] ?? 0) * animT.value)
}
const units = computed(() => progress.value?.units || [])
const unitsCollapsed = ref(false)
const quizCurrent = computed(() => quiz.value.items[quiz.value.index] || null)
function unitPercent(u: { lit: number; total: number }) {
  return u.total ? Math.round((u.lit / u.total) * 100) : 0
}
function tierColor(level: string): string {
  return TIER_COLORS[level] || '#d0d0d0'
}
// 已淡化：有学习历史（答错/追问/衰减）但有效掌握度跌出点亮线的节点——
// 配色与"未接触"区分开，用户一眼看出"学过但掉了"与"从没看过"。
const FADED_COLOR = '#d4a017'
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
function levelStyle(level: string, faded?: boolean): { background: string; color: string } {
  const c = faded ? FADED_COLOR : tierColor(level)
  return { background: `${c}1f`, color: c === '#d0d0d0' ? '#8b8b8b' : c }
}

// 四档卡片展开的节点清单：懒加载一次掌握度列表并缓存。
const masteryList = ref<MasteryView[] | null>(null)
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
  if (!masteryList.value) {
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

function reasonText(reason: string): string {
  const map: Record<string, string> = {
    affinity: t('knowledgeEditor.learningTab.reasonAffinity'),
    frontier: t('knowledgeEditor.learningTab.reasonFrontier'),
    consolidate: t('knowledgeEditor.learningTab.reasonConsolidate'),
    review: t('knowledgeEditor.learningTab.reasonReview'),
    remedial: t('knowledgeEditor.learningTab.reasonRemedial'),
    struggling: t('knowledgeEditor.learningTab.reasonStruggling'),
    'prerequisite-stuck': t('knowledgeEditor.learningTab.reasonStuck'),
    bypass: t('knowledgeEditor.learningTab.reasonBypass'),
    explore: t('knowledgeEditor.learningTab.reasonExplore'),
  }
  return map[reason] || reason
}
// 推荐理由色板：以绿色品牌为主，时间敏感的"待复习"用品牌深绿+时间图标色，
// 负向/警示理由（先修卡住）用暖色区分，绕行用中性蓝灰。
function reasonStyle(reason: string): { background: string; color: string } {
  const map: Record<string, { background: string; color: string }> = {
    frontier: { background: 'rgba(7, 192, 95, 0.10)', color: '#078d5c' },
    affinity: { background: 'rgba(4, 155, 56, 0.12)', color: '#049b38' },
    consolidate: { background: 'rgba(3, 134, 38, 0.12)', color: '#038626' },
    review: { background: 'rgba(0, 82, 217, 0.10)', color: '#0052d9' },
    remedial: { background: 'rgba(213, 73, 65, 0.10)', color: '#c8383f' },
    struggling: { background: 'rgba(212, 160, 23, 0.12)', color: '#a87b10' },
    'prerequisite-stuck': { background: 'rgba(227, 115, 24, 0.12)', color: '#e37318' },
    bypass: { background: 'rgba(93, 155, 218, 0.12)', color: '#3d6f9e' },
    explore: { background: 'rgba(140, 224, 175, 0.25)', color: '#3d8f66' },
  }
  return map[reason] || { background: 'rgba(0, 0, 0, 0.06)', color: '#666' }
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
  const map: Record<string, string> = {
    answer_cite: t('knowledgeEditor.learningTab.evCite'),
    cross_ref: t('knowledgeEditor.learningTab.evCross'),
    re_ask: t('knowledgeEditor.learningTab.evReAsk'),
    topic_signal: t('knowledgeEditor.learningTab.evTopic'),
    wiki_tool_read: t('knowledgeEditor.learningTab.evRead'),
    quiz_correct: t('knowledgeEditor.learningTab.evQuizOk'),
    quiz_wrong: t('knowledgeEditor.learningTab.evQuizBad'),
    backfill_cite: t('knowledgeEditor.learningTab.evBackfill'),
  }
  return map[type] || type
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
  if (type === 'answer_cite' || type === 'backfill_cite' || type === 'topic_signal' || type === 'wiki_tool_read') return 'cite'
  if (type === 'cross_ref') return 'cross'
  if (type === 're_ask') return 'reask'
  if (type === 'quiz_correct' || type === 'quiz_wrong') return 'quiz'
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
  const nodes = progress.value?.total_nodes ?? 0
  const last = timeline.value[0]?.occurred_at
  return t('knowledgeEditor.learningTab.profileSummary', {
    nodes,
    events: timelineTotal.value,
    last: last ? formatTime(last) : '—',
  })
})

function openPage(slug: string) {
  // Jump to the wiki tab and open the page drawer there.
  router.push({ path: `/platform/knowledge-bases/${props.knowledgeBaseId}`, query: { tab: 'wiki', slug } })
}

// 图谱直达：切到图谱页签并以该节点为中心加载局部视图（掌握档环色随之渲染），
// 解决"推荐里的节点在全局图谱里肉眼找不到"的导航缺口。
function openInGraph(slug: string) {
  router.push({ path: `/platform/knowledge-bases/${props.knowledgeBaseId}`, query: { tab: 'graph', slug } })
}

// 后端单页上限 100：首屏一次拉满，原型规模下根本不出现"翻页"这回事。
const TIMELINE_PAGE_SIZE = 100

async function refresh() {
  recommendLoading.value = true
  try {
    const [p, r, tl, s, ch] = await Promise.all([
      getLearningProgress(props.knowledgeBaseId),
      getLearningRecommend(props.knowledgeBaseId, 5),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
      getLearningSettings(),
      getLearningChanges(props.knowledgeBaseId, 20),
    ])
    progress.value = (p as any).data ?? p
    recommendations.value = (r as any).data ?? r ?? []
    timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
    timelineTotal.value = ((tl as any).total ?? 0) as number
    passiveChanges.value = ((ch as any).data ?? ch) as PassiveChangesSummary
    // 刷新即回到第 1 页：否则触底续拉会带着旧页码跳页（关闭答题卡/
    // 删除画像后的刷新都走这里）。
    timelinePage.value = 1
    collectDisabled.value = (s as any).data?.collect_disabled ?? false
    masteryList.value = null // 画像可能已变化，档位清单缓存失效
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

const quizSectionRef = ref<HTMLElement | null>(null)

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
// 当前题的出处文档：作答后的溯源链接数据源。
const currentSourceDocs = computed(() => quizCurrent.value?.source_docs ?? [])
async function currentMasteryOf(slug: string): Promise<MasteryView | null> {
  try {
    const res = await getLearningMastery(props.knowledgeBaseId)
    const list = (((res as any).data ?? res) as MasteryView[])
    return list.find((m) => m.slug === slug) || null
  } catch {
    return null
  }
}

async function startQuiz(rec: Recommendation) {
  quiz.value = { active: true, slug: rec.slug, title: rec.title || rec.slug, loading: true, items: [], index: 0, chosen: '', result: null, levelBefore: '', levelAfter: '', levelAfterP: 0, levelBeforeFaded: false, levelAfterFaded: false }
  const [before] = await Promise.all([currentMasteryOf(rec.slug), (async () => {
    try {
      const res = await getLearningQuiz(props.knowledgeBaseId, rec.slug)
      quiz.value.items = ((res as any).data ?? res) as QuizQuestion[]
    } catch (err) {
      // 取题失败给出明确反馈，而不是静默渲染"暂无题目"的误导性空态。
      console.error('[LearningTab] quiz fetch failed:', err)
      quiz.value.items = []
      MessagePlugin.error(t('knowledgeEditor.learningTab.quizLoadFailed'))
    } finally {
      quiz.value.loading = false
    }
  })()])
  quiz.value.levelBefore = before?.level || ''
  quiz.value.levelBeforeFaded = fadedOf(before)
  // 答题卡渲染在推荐列表下方：滚动定位过去，让"练一练"点了就有可见反馈。
  await nextTick()
  quizSectionRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
function chooseOption(key: string) {
  if (!quiz.value.result) quiz.value.chosen = key
}
async function submitAnswer() {
  const current = quizCurrent.value
  if (!current || !quiz.value.chosen) return
  try {
    const res = await submitLearningAnswer(props.knowledgeBaseId, current.id, quiz.value.chosen)
    quiz.value.result = ((res as any).data ?? res) as AnswerResult
    // "看→练→掌握度更新→下一轮推荐"闭环在一屏内完成：作答后即时重拉
    // 进度、该节点掌握度、推荐列表与时间线首屏，不等关闭答题卡。
    const [p, after, r, tl] = await Promise.all([
      getLearningProgress(props.knowledgeBaseId),
      currentMasteryOf(quiz.value.slug),
      getLearningRecommend(props.knowledgeBaseId, 5),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
    ])
    progress.value = (p as any).data ?? p
    quiz.value.levelAfter = after?.level || quiz.value.levelBefore
    quiz.value.levelAfterFaded = fadedOf(after)
    quiz.value.levelAfterP = after?.p_eff ?? 0
    recommendations.value = (r as any).data ?? r ?? []
    timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
    timelineTotal.value = ((tl as any).total ?? 0) as number
    timelinePage.value = 1
    masteryList.value = null // 档位清单缓存失效
  } catch (err) {
    console.error('[LearningTab] submit failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.submitFailed'))
  }
}
function nextQuestion() {
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
  try {
    const optOut = deleteOptOut.value
    await deleteLearningProfile(optOut)
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

// 键盘作答：A–D 选择选项、Enter 提交/下一题——练一练可以全程不碰鼠标。
function onKeydown(e: KeyboardEvent) {
  if (!quiz.value.active) return
  const key = e.key.toUpperCase()
  if (quiz.value.result === null) {
    if (/^[A-D]$/.test(key) && quizCurrent.value && key in quizCurrent.value.options) {
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

onMounted(() => {
  refresh()
  maybeOpenPracticeFromQuery()
  window.addEventListener('keydown', onKeydown)
  // 时间线触底自动翻页：哨兵进入视口即加载下一页（root 缺省按视口计算，
  // 嵌套滚动容器同样生效），加载到与 total 持平后哨兵隐藏、不再触发。
  if (typeof IntersectionObserver !== 'undefined') {
    timelineObserver = new IntersectionObserver(
      (entries) => {
        sentinelVisible.value = entries.some((e) => e.isIntersecting)
        if (sentinelVisible.value) loadMoreTimeline()
      },
      { rootMargin: '200px' },
    )
    if (timelineEndRef.value) timelineObserver.observe(timelineEndRef.value)
  }
})

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
.learning-tab { padding: 16px 20px; max-width: 920px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px; }
/* 入场动态：各区卡片淡入上滑、按序 stagger（对齐文档页签"若隐若现滑出"的语言） */
.learning-tab > * { animation: learning-enter 0.4s ease both; }
.learning-tab > *:nth-child(2) { animation-delay: 0.06s; }
.learning-tab > *:nth-child(3) { animation-delay: 0.12s; }
.learning-tab > *:nth-child(4) { animation-delay: 0.18s; }
.learning-tab > *:nth-child(5) { animation-delay: 0.24s; }
@keyframes learning-enter {
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .learning-tab > * { animation: none; }
  .unit-bar-fill { transition: none; }
}
.learning-header { display: flex; align-items: center; gap: 24px; padding: 18px 20px; border: 1px solid var(--td-component-border, #e7e7e7); border-radius: 10px; background: var(--td-bg-color-container, #fff); box-shadow: var(--td-shadow-1); flex-wrap: wrap; }
.ring-text { font-size: 22px; font-weight: 600; fill: var(--td-text-color-primary, #333); }
.ring-sub { font-size: 11px; fill: var(--td-text-color-placeholder, #999); }
.learning-progress-meta { flex: 1; min-width: 240px; }
.meta-title { font-size: 16px; font-weight: 600; margin-bottom: 6px; display: flex; align-items: center; gap: 6px; }
.help-icon { color: var(--td-text-color-placeholder, #999); cursor: help; display: inline-flex; align-items: center; }
.help-icon:hover { color: var(--td-brand-color, #07c05f); }
.meta-line { color: var(--td-text-color-secondary, #666); font-size: 13px; margin-bottom: 10px; }
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
.tier-node:hover { border-color: var(--td-brand-color, #07c05f); color: #038626; }
.tier-node-title { max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tier-node-meta { color: var(--td-text-color-placeholder, #999); font-size: 11px; }
.howto { max-width: 360px; display: flex; flex-direction: column; gap: 6px; }
.howto-title { font-weight: 600; }
.howto-line { font-size: 12px; color: var(--td-text-color-secondary, #555); line-height: 1.6; }
.learning-units { min-width: 230px; max-width: 280px; display: flex; flex-direction: column; gap: 6px; border-left: 1px solid var(--td-component-border, #eee); padding-left: 20px; }
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
.learning-section { border: 1px solid var(--td-component-border, #e7e7e7); border-radius: 10px; padding: 14px 16px; background: var(--td-bg-color-container, #fff); box-shadow: var(--td-shadow-1); }
.section-title { display: flex; align-items: center; gap: 8px; font-weight: 600; margin-bottom: 12px; }
.section-title::before { content: ''; width: 3px; height: 14px; border-radius: 2px; background: var(--td-brand-color, #07c05f); flex-shrink: 0; }
.section-empty { color: var(--td-text-color-placeholder, #999); font-size: 13px; }
.recommend-list { display: flex; flex-direction: column; gap: 8px; }
.recommend-card { display: flex; align-items: center; gap: 12px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; padding: 10px 12px; cursor: pointer; transition: border-color 0.2s, box-shadow 0.2s; }
.recommend-card:hover { border-color: rgba(7, 192, 95, 0.5); box-shadow: 0 2px 8px rgba(7, 192, 95, 0.1); }
.rec-rank { width: 22px; height: 22px; border-radius: 50%; background: var(--td-bg-color-component, #f0f0f0); color: var(--td-text-color-secondary, #666); font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; }
.rec-rank.top { background: rgba(7, 192, 95, 0.12); color: #049b38; }
.rec-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.rec-title-line { display: flex; align-items: center; gap: 8px; min-width: 0; flex-wrap: wrap; }
.rec-type-badge { font-size: 11px; line-height: 1; padding: 2px 6px; border-radius: 4px; flex-shrink: 0; }
.rec-type-badge.entity { background: rgba(43, 164, 113, 0.1); color: #2ba471; }
.rec-type-badge.concept { background: rgba(227, 115, 24, 0.1); color: #e37318; }
.rec-title { font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
.rec-folder { display: inline-flex; align-items: center; gap: 3px; font-size: 12px; color: var(--td-text-color-placeholder, #999); flex-shrink: 0; }
.rec-reason-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.rec-reason-tag { font-size: 11px; line-height: 1; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0; }
.explain { max-width: 320px; display: flex; flex-direction: column; gap: 6px; }
.explain-title { font-weight: 600; }
.explain-line { font-size: 12px; color: var(--td-text-color-secondary, #555); line-height: 1.6; }
.explain-warn { color: #b8860b; }
.explain-improve { color: #049b38; }
.rec-level.faded { border: 1px dashed #d4a017; }
.rec-level { display: inline-flex; align-items: center; gap: 5px; font-size: 11px; line-height: 1; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0; }
.rec-actions { display: flex; gap: 8px; flex-shrink: 0; }
/* 遗忘动态（被动变化通道）：徽章汇总 + 明细行，与时间线分栏互不挤占。 */
.changes-badges { display: flex; gap: 8px; margin-bottom: 10px; flex-wrap: wrap; }
.changes-badge { font-size: 11px; line-height: 1; padding: 4px 9px; border-radius: 4px; }
.changes-badge.demoted { background: rgba(213, 73, 65, 0.10); color: #c8383f; }
.changes-badge.due { background: rgba(212, 160, 23, 0.12); color: #a87b10; }
.changes-list { display: flex; flex-direction: column; gap: 6px; }
.changes-row { display: flex; align-items: center; gap: 10px; padding: 6px 10px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s; flex-wrap: wrap; }
.changes-row:hover { border-color: rgba(7, 192, 95, 0.5); }
.changes-tier { display: inline-flex; align-items: center; gap: 4px; font-size: 11px; white-space: nowrap; }
.changes-arrow { color: var(--td-text-color-placeholder, #999); margin: 0 2px; }
.changes-p { font-size: 11px; color: var(--td-text-color-secondary, #555); white-space: nowrap; }
.changes-hint { font-size: 11px; color: #a87b10; margin-left: auto; white-space: nowrap; }
.changes-hint.overdue { color: #c8383f; }
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
.learning-header-error { justify-content: center; }
.error-body { display: flex; align-items: center; gap: 12px; padding: 20px 0; }
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
.timeline-item { display: flex; align-items: center; gap: 8px; font-size: 12px; height: 24px; }
.tl-group-header { font-size: 11px; color: var(--td-text-color-placeholder, #999); padding-left: 18px; height: 22px; display: flex; align-items: center; font-weight: 500; }
.tl-dot { width: 8px; height: 8px; border-radius: 50%; background: #4b9bd8; flex-shrink: 0; position: relative; z-index: 1; box-shadow: 0 0 0 2px var(--td-bg-color-container, #fff); }
.tl-dot.ev-answer_cite { background: #4b9bd8; }
.tl-dot.ev-cross_ref { background: #07c05f; }
.tl-dot.ev-re_ask { background: #ed7b2f; }
.tl-dot.ev-topic_signal { background: #a6a6a6; }
.tl-dot.ev-quiz_correct { background: #049b38; box-shadow: 0 0 0 2px var(--td-bg-color-container, #fff), 0 0 0 3px rgba(4, 155, 56, 0.35); }
.tl-dot.ev-quiz_wrong { background: #e34d59; }
.tl-dot.ev-backfill_cite { background: transparent; border: 1.5px dashed #a6a6a6; box-shadow: none; width: 7px; height: 7px; }
.tl-type { color: var(--td-text-color-secondary, #666); width: 72px; flex-shrink: 0; }
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
.tl-filters { display: inline-flex; gap: 4px; margin-left: auto; flex-wrap: wrap; }
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
.tl-node-chip span.tl-node-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
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
@media (max-width: 640px) {
  .learning-header { flex-direction: column; align-items: flex-start; }
  .learning-units { width: 100%; max-width: none; border-left: none; padding-left: 0; }
  .recommend-card { flex-direction: column; align-items: flex-start; }
  .rec-actions { width: 100%; justify-content: flex-start; }
  .timeline-item { flex-wrap: wrap; height: auto; }
  .tl-slug { min-width: 0; max-width: 180px; }
  .tl-filters { margin-left: 0; }
}
</style>
