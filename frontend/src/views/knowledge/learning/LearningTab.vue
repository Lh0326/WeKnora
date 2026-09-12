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
          <ComponentWorkspace :key="learningScopeKey" ref="componentWorkspace" :kb-id="knowledgeBaseId" @view="componentView=$event" @source="openWikiPage" @changed="notifyLearningUpdated(knowledgeBaseId, '')" />
          <ObjectiveWorkspace :key="learningScopeKey" v-if="!hasComponents" ref="objectiveWorkspace" :kb-id="knowledgeBaseId"
            @open="openPage" @quiz="(slug, objective) => startQuiz({slug,title:slug,reason:'',has_quiz:true}, objective)"
            @view="objView=$event" @plan="currentPath=$event" @highlight="highlightZone=$event" />
        </div>
        <div class="side-foot">
          <t-button size="small" variant="outline" @click="timelineDrawer=true">{{ $t('knowledgeEditor.learningTab.timelineTitle') }}<span v-if="timelineTotal>0" class="foot-badge" title="全部历史记录条数">{{ timelineTotal }}</span></t-button>
          <t-button size="small" variant="outline" @click="profileDrawer=true">{{ $t('knowledgeEditor.learningTab.profileTitle') }}</t-button>
        </div>
      </div>
    </div>
    <LearningReader :key="learningScopeKey" :kb-id="knowledgeBaseId" :slug="readerSlug" :visible="readerOpen" :next-slug="readerNext?.slug" :next-title="readerNext?.title" @close="readerOpen=false" @next="openPage" @full="openWikiPage" />
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
        <t-switch :disabled="settingsBusy || initialLoading || loadFailed" v-model="collectDisabled" size="small" @change="onToggleCollect" />
        <span class="profile-label">{{ $t('knowledgeEditor.learningTab.stopCollect') }}</span>
        <span class="spacer"></span>
        <t-button size="small" theme="danger" variant="outline" @click="openDeleteDialog">{{ $t('knowledgeEditor.learningTab.delete') }}</t-button>
      </div>
      <div class="profile-export">
        <strong>保存我的学习记录</strong>
        <p>包含当前账号在所有知识库中的记录。阅读、自评与检查分别展示；历史记录不等于能力认证。</p>
        <div class="profile-row">
          <t-button size="small" variant="outline" :loading="exporting === 'html'" :disabled="exporting !== null" @click="exportProfile('html')">学习记录（可阅读）</t-button>
          <t-button size="small" variant="outline" :loading="exporting === 'json'" :disabled="exporting !== null" @click="exportProfile('json')">原始数据 JSON</t-button>
        </div>
        <p>学习记录可用浏览器离线打开、回顾或打印；JSON 保留接口返回的完整字段，便于备份和程序处理。</p>
      </div>
      <div class="profile-row">
        <t-button size="small" variant="outline" :loading="exportingAssessment" @click="exportAssessment">导出评估快照</t-button>
        <span class="profile-label">保存当前知识库的个人状态，供独立测验使用。</span>
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
import { useAuthStore } from '@/stores/auth'
import { Button as TButton, Dialog as TDialog, Drawer as TDrawer, MessagePlugin, Switch as TSwitch } from 'tdesign-vue-next'
import LearningConstellation from './LearningConstellation.vue'
import ComponentWorkspace from './ComponentWorkspace.vue'
import {componentGraph} from './componentPresentation'
import {renderLearningProfileHTML} from './profileExport'
import type {ComponentView} from '@/api/learning/components'
import ObjectiveWorkspace from './ObjectiveWorkspace.vue'
import { projectObjectiveNodes } from './objectivePresentation'
import LearningReader from './LearningReader.vue'
import {LEARNING_UPDATED,notifyLearningUpdated} from './learningEvents'
import {createRequestScope} from './requestScope'
import type { LearningPathPlan, ObjectiveViewResponse } from '@/api/learning/objectives'
import {
  getLearningProgress, getLearningZoneMap, getLearningQuiz, submitLearningAnswer,
  getLearningTimeline, deleteLearningProfile, selfAssess,
  type SelfAssessDownReason,
  getLearningSettings, updateLearningSettings, getLearningMastery, getLearningChanges,
  type LearningProgress, type Recommendation, type QuizQuestion, type AnswerResult, type TimelineItem, type MasteryView, type PassiveChangesSummary,
  type ZoneMapResponse, type ZoneSummary,
} from '@/api/learning'

const props = defineProps<{ knowledgeBaseId: string }>()
const assessmentAuth = useAuthStore()
const learningScopeKey=computed(()=>JSON.stringify([props.knowledgeBaseId,assessmentAuth.user?.id,assessmentAuth.currentTenantId]))
const requestScope=createRequestScope(()=>learningScopeKey.value)
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
// 悬停右侧栏分区卡片时，星图仅亮该分区并标记其"1"。
const highlightZone = ref<string | null>(null)
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
const exporting = ref<'html' | 'json' | null>(null)
const exportingAssessment = ref(false)
const quiz = ref<{
  active: boolean; slug: string; title: string; loading: boolean
  items: QuizQuestion[]; index: number; chosen: string; result: AnswerResult | null
  levelBefore: string; levelAfter: string; levelAfterP: number
  levelBeforeFaded: boolean; levelAfterFaded: boolean
}>({ active: false, slug: '', title: '', loading: false, items: [], index: 0, chosen: '', result: null, levelBefore: '', levelAfter: '', levelAfterP: 0, levelBeforeFaded: false, levelAfterFaded: false })

function fadedOf(m: MasteryView | null): boolean {
  return !!m && m.level === 'unseen' && m.evidence_count > 0
}
// 底部两块低频分区的折叠状态：时间线默认展开（课题核心演示物），画像默认收起。
// 时间线/画像改为放大抽屉：默认关闭，右栏底部按钮进入
const timelineDrawer = ref(false)
const profileDrawer = ref(false)
const quizCurrent = computed(() => quiz.value.items[quiz.value.index] || null)
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
async function ensureMasteryList() {
  if (masteryList.value) return
  const current=requestScope.capture(), gen=refreshGeneration
  try {
    const res=await getLearningMastery(props.knowledgeBaseId)
    if(!current()||gen!==refreshGeneration)return
    masteryList.value=((res as any).data??res) as MasteryView[]
    masteryLoadFailed.value=false
  } catch {if(current()&&gen===refreshGeneration)masteryLoadFailed.value=true}
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

// 今日待巩固：被动遗忘（已降档/临近降档）+ 主动推荐中的错题重练/反复追问/
// 前置阻塞，合并去重为一张待办清单——"今天该做什么"一眼可答。
type TodoKind = 'demoted' | 'due' | 'remedial' | 'struggling' | 'stuck'
interface TodoRow { slug: string; title: string; kind: TodoKind; detail: string; hasQuiz: boolean }
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

// 后端单页上限 100：首屏一次拉满，原型规模下根本不出现"翻页"这回事。
const TIMELINE_PAGE_SIZE = 100
let refreshGeneration=0,timelineGeneration=0

async function refresh(workspaces=true) {
  const gen=++refreshGeneration,scope=learningScopeKey.value,timelineGen=++timelineGeneration
  const current=()=>gen===refreshGeneration&&scope===learningScopeKey.value
  recommendLoading.value = true
  timelineLoading.value = false
  try {
    const [p, r, tl, s, ch] = await Promise.all([
      getLearningProgress(props.knowledgeBaseId),
      getLearningZoneMap(props.knowledgeBaseId),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
      getLearningSettings(),
      getLearningChanges(props.knowledgeBaseId, 20),
    ])
    if(!current())return
    progress.value = (p as any).data ?? p
    zoneMap.value = (r as any).data ?? null
    recommendations.value = ((r as any).data?.zones ?? [])
      .map((z: ZoneSummary) => z.next)
      .filter((x: Recommendation | null): x is Recommendation => !!x)
    if(timelineGen===timelineGeneration){
      timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
      timelineTotal.value = ((tl as any).total ?? 0) as number
      timelinePage.value = 1
    }
    passiveChanges.value = ((ch as any).data ?? ch) as PassiveChangesSummary
    // 刷新即回到第 1 页：否则触底续拉会带着旧页码跳页（关闭答题卡/
    // 删除画像后的刷新都走这里）。
    collectDisabled.value = (s as any).data?.collect_disabled ?? false
    masteryList.value = null // 画像可能已变化，档位清单缓存失效
    ensureMasteryList() // 星图随刷新重建
    if(workspaces)await objectiveWorkspace.value?.refresh()
    if(!current())return
    if(workspaces)await componentWorkspace.value?.refresh()
    if(!current())return
    loadFailed.value = false
  } catch (err) {
    if(!current())return
    console.error('[LearningTab] refresh failed:', err)
    loadFailed.value = true
    MessagePlugin.error(t('knowledgeEditor.learningTab.loadFailed'))
  } finally {
    if(current()){
      recommendLoading.value = false
      initialLoading.value = false
    }
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
  const gen=timelineGeneration,scope=learningScopeKey.value
  const current=()=>gen===timelineGeneration&&scope===learningScopeKey.value
  timelineLoading.value = true
  timelinePage.value++
  let items: TimelineItem[] = []
  try {
    const res = await getLearningTimeline(props.knowledgeBaseId, timelinePage.value, TIMELINE_PAGE_SIZE)
    if(!current())return
    items = ((res as any).data ?? res) as TimelineItem[]
    timeline.value.push(...items)
  } catch {
    if(current())timelinePage.value--
    return // 网络失败：停止本轮，哨兵再次进出视口时会重试
  } finally {
    if(current())timelineLoading.value = false
  }
  if(!current())return
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
  timelineLoading.value=false
  const gen=++timelineGeneration,scope=learningScopeKey.value
  try {
    const res = await getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE)
    if(gen!==timelineGeneration||scope!==learningScopeKey.value)return
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
  const current=requestScope.capture(), gen=refreshGeneration
  if (!masteryList.value) {
    try {
      const res=await getLearningMastery(props.knowledgeBaseId)
      if(!current()||gen!==refreshGeneration)return null
      masteryList.value=((res as any).data??res) as MasteryView[]
    } catch { return null }
  }
  return masteryList.value.find(m=>m.slug===slug)||null
}

// Bug fix: generation counter — when quiz A is closed and quiz B opened
// quickly, A's late-arriving response would overwrite B's items/levelBefore
// (wrong questions displayed under B's slug, wrong mastery comparison).
const quizHelped=ref(false)
let quizGeneration = 0
async function startQuiz(rec: Recommendation, objectiveID?: string) {
  quizHelped.value=false
  const gen = ++quizGeneration, scopeCurrent=requestScope.capture()
  const current=()=>scopeCurrent()&&gen===quizGeneration
  quizSubmitting.value=false
  quiz.value = { active: true, slug: rec.slug, title: rec.title || rec.slug, loading: true, items: [], index: 0, chosen: '', result: null, levelBefore: '', levelAfter: '', levelAfterP: 0, levelBeforeFaded: false, levelAfterFaded: false }
  const [before] = await Promise.all([currentMasteryOf(rec.slug), (async () => {
    try {
      const res = await getLearningQuiz(props.knowledgeBaseId, rec.slug)
      if (!current()) return // a newer quiz session superseded this one
      quiz.value.items = (((res as any).data ?? res) as QuizQuestion[]).filter(q=>!objectiveID||q.objective_id===objectiveID)
    } catch (err) {
      if(!current())return
      // 取题失败给出明确反馈，而不是静默渲染"暂无题目"的误导性空态。
      console.error('[LearningTab] quiz fetch failed:', err)
      quiz.value.items = []
      MessagePlugin.error(t('knowledgeEditor.learningTab.quizLoadFailed'))
    } finally {
      if(current())quiz.value.loading = false
    }
  })()])
  if (!current()) return // stale session: don't overwrite a newer quiz's state
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
  if (!quiz.value.result && !quizSubmitting.value) quiz.value.chosen = key
}
// Bug fix: in-flight guard — without this, holding Enter (auto-repeat) or
// double-clicking during network RTT fires multiple POSTs; the backend has
// no idempotency key, so each lands as a duplicate attempt+event.
const quizSubmitting = ref(false)
async function submitAnswer() {
  const item=quizCurrent.value
  if(quizSubmitting.value||quiz.value.result||!item||!quiz.value.chosen)return
  const kb=props.knowledgeBaseId,slug=quiz.value.slug,gen=quizGeneration,scopeCurrent=requestScope.capture()
  const current=()=>scopeCurrent()&&gen===quizGeneration
  quizSubmitting.value=true
  try {
    const res=await submitLearningAnswer(kb,item.id,quiz.value.chosen,quizHelped.value?'assistant_helped':item.assistance_mode)
    if(!scopeCurrent())return
    // The action still belongs to this library after its drawer has closed.
    notifyLearningUpdated(kb,slug)
    if(!current())return
    quiz.value.result=((res as any).data??res) as AnswerResult
    masteryList.value=null
    const after=await currentMasteryOf(slug)
    if(!current())return
    quiz.value.levelAfter=after?.level||quiz.value.levelBefore
    quiz.value.levelAfterFaded=fadedOf(after)
    quiz.value.levelAfterP=after?.p_eff??0
  } catch(err) {
    if(current())MessagePlugin.error(t('knowledgeEditor.learningTab.submitFailed'))
  } finally {if(current())quizSubmitting.value=false}
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
  ++quizGeneration
  quizSubmitting.value=false
  quiz.value.active = false
  refresh()
}

const settingsBusy=ref(false)
async function onToggleCollect(value: unknown) {
  const current=requestScope.capture()
  settingsBusy.value=true
  try {
    await updateLearningSettings(Boolean(value))
    if(current())notifyLearningUpdated(props.knowledgeBaseId,'')
  } catch {
    if(current()){
      collectDisabled.value=!Boolean(value)
      MessagePlugin.error(t('knowledgeEditor.learningTab.toggleFailed'))
    }
  } finally {if(current())settingsBusy.value=false}
}

// 打开删除确认即重置勾选：上一次会话留下的"已了解不可撤销"不能预授权本次删除。
function openDeleteDialog() {
  deleteOptOut.value = false
  deleteAcknowledge.value = false
  confirmDelete.value = true
}

async function exportProfile(format: 'html' | 'json') {
  if (exporting.value !== null) return
  const current=requestScope.capture()
  const kb = props.knowledgeBaseId
  exporting.value = format
  try {
    const raw = await (await import('@/api/learning')).downloadLearningProfile()
    if (!current()) return
    const text = format === 'html' ? renderLearningProfileHTML(JSON.parse(raw), kb) : raw
    const blob = new Blob([text], { type: format === 'html' ? 'text/html;charset=utf-8' : 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    const stamp = new Date().toISOString().replace(/[:.]/g, '-')
    a.download = `WeKnora-${format === 'html' ? '学习记录' : '原始学习数据'}-全部知识库-${stamp}.${format}`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) {
    if(!current())return
    console.error('[LearningTab] export failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.exportFailed'))
  } finally {
    if(current())exporting.value = null
  }
}

async function exportAssessment() {
  if(exportingAssessment.value)return
  const current=requestScope.capture()
  const kb = props.knowledgeBaseId
  const user = assessmentAuth.user?.id
  const tenant = assessmentAuth.currentTenantId
  exportingAssessment.value = true
  try {
    const text = await (await import('@/api/learning')).downloadLearningAssessment(kb)
    if (!current()) return
    const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `weknora-assessment-${kb}-${Date.now()}.json`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch {
    if(current())MessagePlugin.error('评估快照导出失败，请重试。')
  } finally {
    if(current())exportingAssessment.value = false
  }
}

async function doDelete() {
  const current=requestScope.capture()
  readerOpen.value=false
  ++quizGeneration;quiz.value.active=false;quizSubmitting.value=false
  confirmDelete.value = false
  componentWorkspace.value?.close()
  try {
    const optOut = deleteOptOut.value
    await nextTick()
    if(!current())return
    await deleteLearningProfile(optOut)
    if(!current())return
    await objectiveWorkspace.value?.restorePreferences()
    if(!current())return
    if (optOut) {
      collectDisabled.value = true
      deleteOptOut.value = false
    }
    deleteAcknowledge.value = false
    MessagePlugin.success(t('knowledgeEditor.learningTab.deleteDone'))
    refresh()
  } catch (err) {
    if(!current())return
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
  if((e.target as HTMLElement)?.closest?.('input,textarea,select,[contenteditable=true]'))return
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

let active=true
function onLearningUpdated(event:Event){
  if(active&&(event as CustomEvent).detail?.kbId===props.knowledgeBaseId)void refresh()
}
onMounted(() => {
  window.addEventListener(LEARNING_UPDATED,onLearningUpdated)
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
  active=true
  if (!initialLoading.value) refresh()
  window.addEventListener('keydown', onKeydown)
})
onDeactivated(() => {
  active=false
  ++quizGeneration;quiz.value.active=false;quizSubmitting.value=false
  readerOpen.value = false
  window.removeEventListener('keydown', onKeydown)
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

watch(profileDrawer, (open) => {if(open)refreshTimelineSilently()})
watch(learningScopeKey,()=>{
  requestScope.invalidate()
  ++refreshGeneration;++timelineGeneration;++quizGeneration
  quiz.value.active=false;quizSubmitting.value=false;readerOpen.value=false;readerSlug.value=''
  masteryList.value=null;masteryLoadFailed.value=false;currentPath.value=null
  timelineLoading.value=false;collectDisabled.value=false;settingsBusy.value=false
  selectedNodeSlugs.value=new Set();timelineFilter.value='all'
  confirmDelete.value=false;selfAssessDownOpen.value=false;selfAssessTarget.value=null
  exporting.value=null;exportingAssessment.value=false;initialLoading.value=true
  timeline.value=[];timelineTotal.value=0;timelinePage.value=1
  componentView.value=null;objView.value=null;progress.value=null;zoneMap.value=null
  profileDrawer.value=false;timelineDrawer.value=false
  refresh()
},{flush:'sync'})

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
  requestScope.dispose()
  ++refreshGeneration;++timelineGeneration;++quizGeneration
  window.removeEventListener(LEARNING_UPDATED,onLearningUpdated)
  window.removeEventListener('keydown', onKeydown)
  timelineObserver?.disconnect()
  timelineObserver = null
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
.ts-title { font-size: 15px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.ts-line { font-size: 12px; color: var(--td-text-color-secondary, #666); }
/* 右：数据栏。滚动区承载统计/档位/待巩固/分组/推荐，底栏固定放时间线/画像放大入口 */
.sky-side { min-width: 0; min-height: 0; display: flex; flex-direction: column; border-left: 1px solid var(--td-component-border, #eee); padding-left: 16px; }
.side-scroll { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 14px; padding-right: 10px; scrollbar-width: thin; }
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
/* 展开的完整学习路径：连续序号 + 档位批注，学到哪一眼可追 */
.zone-path { display: flex; flex-direction: column; gap: 2px; border-top: 1px dashed var(--td-component-border, #eee); padding-top: 6px; }
.zone-path-row { display: flex; align-items: center; gap: 6px; cursor: pointer; padding: 2px 2px; border-radius: 6px; }
.zone-path-row:hover { background: var(--td-brand-color-light, rgba(7, 192, 95, 0.07)); }
.zone-path-row.current { background: rgba(7, 192, 95, 0.1); }
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
}
.tier-node:focus-visible, .todo-row:focus-visible, .rec-mini:focus-visible, .sa-option:focus-visible { outline: 2px solid var(--td-brand-color, #07c05f); outline-offset: 1px; }
.section-empty { color: var(--td-text-color-placeholder, #999); font-size: 13px; }
.explain { max-width: 320px; display: flex; flex-direction: column; gap: 6px; }
.sa-dialog { display: flex; flex-direction: column; gap: 8px; }
.sa-node { font-size: 13px; font-weight: 600; margin-bottom: 2px; }
.sa-option { display: flex; gap: 10px; padding: 8px 10px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s; }
.sa-option:hover { border-color: rgba(123, 97, 255, 0.5); }
.sa-option.selected { border-color: #7b61ff; background: rgba(123, 97, 255, 0.06); }
.sa-radio { width: 14px; height: 14px; border-radius: 50%; border: 1.5px solid var(--td-component-border, #ccc); flex-shrink: 0; margin-top: 2px; }
.sa-option.selected .sa-radio { border-color: #7b61ff; background: radial-gradient(circle, #7b61ff 40%, transparent 45%); }
.sa-option-label { font-size: 13px; font-weight: 500; }
.sa-option-hint { font-size: 11px; color: var(--td-text-color-placeholder, #999); margin-top: 2px; }
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
.profile-export { margin: 18px 0; padding: 14px; border: 1px solid var(--td-component-border); border-radius: 8px; }
.profile-export strong { font-size: 14px; }
.profile-export p { margin: 8px 0; font-size: 12px; line-height: 1.7; color: var(--td-text-color-secondary); }
.profile-export .profile-row { flex-wrap: wrap; }
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
  .timeline-item { flex-wrap: wrap; height: auto; }
  .tl-slug { min-width: 0; max-width: 180px; }
}
</style>
