<template>
  <div class="learning-tab">
    <!-- 停采全局横幅：一进页签就能看到，避免"答了题却不亮"的困惑 -->
    <div v-if="collectDisabled && !initialLoading" class="collect-banner">
      {{ $t('knowledgeEditor.learningTab.collectOnNote') }}
    </div>
    <!-- 头部：首屏只保留「今日」一行——任务导向（Khan/Duolingo 首屏模式），
         总进度仪表盘下移为次屏分区，避免一进页签就被四档+分组+遗忘+推荐
         全量信息淹没。 -->
    <!-- 加载态 / 错误态：整幅占位（错误态给重试入口，绝不伪装成"没有数据"） -->
    <div v-if="initialLoading" class="sky-state">
      <t-loading size="large" />
    </div>
    <div v-else-if="loadFailed" class="sky-state">
      <div class="error-text">{{ $t('knowledgeEditor.learningTab.loadError') }}</div>
      <t-button size="small" variant="outline" @click="refresh()">{{ $t('knowledgeEditor.learningTab.retry') }}</t-button>
    </div>

    <!-- 单屏星空：左 2/3 知识星图（无边框、标题悬浮于圆图左上空角），右 1/3 数据栏
         （今日统计→档位→待巩固→分组→推荐，内部滚动），底部固定入口放大查看时间线/画像 -->
    <div v-else class="sky" :class="{ 'health-view': healthView }">
      <!-- 头部行：左上标题 + 右上视图切换（仅 owner/admin 可见）；健康视图下标题
           切换为「知识资产健康」，星图与数据栏整体让位给健康面板，个人视图保持原样 -->
      <div class="sky-head">
        <div class="sky-head-main">
          <template v-if="!healthView">
          <div class="ts-title">
            {{ $t('knowledgeEditor.learningTab.title') }}
            <t-popup trigger="hover" placement="bottom-left" show-arrow :overlay-style="{ maxWidth: '380px' }">
              <template #content>
                <div class="howto">
                  <div class="howto-title">{{ $t('knowledgeEditor.learningTab.howComputedTitle') }}</div>
                  <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedSignals') }}</div>
                  <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedGate') }}</div>
                  <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedDecay') }}</div>
                  <div class="howto-line">{{ $t('knowledgeEditor.learningTab.howComputedTiers') }}</div>
                </div>
              </template>
              <span class="help-icon"><HelpCircleIcon size="15" /></span>
            </t-popup>
          </div>
          <div class="ts-line">
            {{ $t('knowledgeEditor.learningTab.litOf', { lit: displayLit, total: progress?.total_nodes ?? 0 }) }}
            <span v-if="masteredCount" class="meta-mastered">· {{ $t('knowledgeEditor.learningTab.masteredOf', { n: masteredCount }) }}</span>
          </div>
          </template>
          <div v-else class="ts-title">{{ $t('knowledgeEditor.learningTab.healthTitle') }}</div>
        </div>
        <!-- 视图切换：tl-filter 同款胶囊按钮（个人视图 / 知识健康） -->
        <div v-if="canManage" class="view-toggle">
          <button class="vt-btn" :class="{ active: !healthView }" @click="healthView = false">{{ $t('knowledgeEditor.learningTab.viewPersonal') }}</button>
          <button class="vt-btn" :class="{ active: healthView }" @click="healthView = true">{{ $t('knowledgeEditor.learningTab.viewHealth') }}</button>
        </div>
      </div>
      <template v-if="!healthView">
        <div class="sky-chart">
          <LearningConstellation :nodes="constellationNodes" @open="openPage" />
        </div>
        <div class="sky-side">
        <div class="side-scroll">
          <!-- 今日处理记录：企业数据整合视角的事实性状态。连续学习天数一类的
               激励话术属于教育产品语境，不符合 WeKnora（企业级数据管理）定位 -->
          <div class="side-stats">
            <span class="ts-stat" :title="$t('knowledgeEditor.learningTab.todayEventsNote')">
              {{ $t('knowledgeEditor.learningTab.todayEvents', { n: todayTimelineCount }) }}
            </span>
            <span class="ts-stat" :title="$t('knowledgeEditor.learningTab.todayAnswers')">
              {{ todayAnswersText }}
            </span>
            <span class="ts-stat" :title="$t('knowledgeEditor.learningTab.todayLit')">
              {{ $t('knowledgeEditor.learningTab.todayLit', { n: progress?.today?.lit_today ?? 0 }) }}
            </span>
          </div>
          <!-- 四档统计 + 档位明细 -->
          <div class="side-block">
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
                <span v-for="m in tierList" :key="m.slug" class="tier-node"
                  :title="$t('knowledgeEditor.learningTab.tierNodeTip', { p: Math.round((m.p_eff ?? 0) * 100), n: m.evidence_count }) + (m.self_assess ? '\n' + selfAssessText(m.self_assess) : '')"
                  @click="openPage(m.slug)">
                  <span v-if="tierExpanded === 'unseen' && m.evidence_count > 0" class="tier-dot" style="background: #d4a017;"></span>
                  <span class="tier-node-title">{{ titleOf(m.slug, m.title) }}</span>
                  <span class="tier-node-meta">{{ m.evidence_count }}{{ m.low_confidence ? '?' : '' }}</span>
                </span>
              </template>
            </div>
          </div>
          <!-- 今日待巩固 -->
          <div class="side-block" v-if="todayTodo.length">
            <div class="side-title">{{ $t('knowledgeEditor.learningTab.todayCardTitle') }}</div>
            <div class="todo-list">
              <div v-for="td in todayTodo" :key="td.slug" class="todo-row" @click="openPage(td.slug)">
                <span class="todo-tag" :style="todoTagStyle(td.kind)">{{ todoTagText(td.kind) }}</span>
                <span class="todo-title" :title="$t('knowledgeEditor.learningTab.hoverOpenNode')">{{ td.title }}</span>
                <span class="todo-actions" @click.stop>
                  <t-button v-if="td.hasQuiz" size="small" @click="startQuiz({ slug: td.slug, title: td.title, has_quiz: true } as any)">
                    {{ $t('knowledgeEditor.learningTab.practice') }}
                  </t-button>
                  <t-button size="small" variant="outline" @click="openPage(td.slug)">
                    {{ $t('knowledgeEditor.learningTab.openPage') }}
                  </t-button>
                </span>
              </div>
            </div>
          </div>
          <!-- 分组进度 -->
          <div class="side-block units-compact" v-if="units.length">
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
          <!-- 下一步看什么：窄栏紧凑行（岗位覆盖计划：按目录分组展示，行结构/排名/操作不变） -->
          <div class="side-block">
            <div class="side-title">{{ $t('knowledgeEditor.learningTab.recommendTitle') }}</div>
            <div v-if="recommendations.length" class="role-plan-intro">
              <span class="role-plan-name">{{ $t('knowledgeEditor.learningTab.rolePlanTitle') }}</span>
              <span>{{ $t('knowledgeEditor.learningTab.rolePlanIntro') }}</span>
            </div>
            <div v-if="recommendLoading" class="section-empty">{{ $t('common.loading') }}</div>
            <div v-else-if="!recommendations.length" class="section-empty">{{ $t('knowledgeEditor.learningTab.recommendEmpty') }}</div>
            <div v-else class="rec-mini-list">
              <div v-for="group in groupedRecommendations" :key="group.name" class="rec-group">
                <div class="rec-group-header">{{ group.name }}</div>
                <div v-for="item in group.items" :key="item.rec.slug" class="rec-mini"
                  :class="{ featured: item.rank === 0 }" @click="openPage(item.rec.slug)">
                  <span class="rec-rank" :class="{ top: item.rank === 0 }">{{ item.rank + 1 }}</span>
                  <div class="rec-mini-main">
                    <div class="rec-mini-title">
                      <span v-if="pageTypeOf(item.rec.slug)" class="rec-type-badge" :class="pageTypeOf(item.rec.slug)">{{ pageTypeText(pageTypeOf(item.rec.slug)) }}</span>
                      <span class="rec-mini-name" :title="$t('knowledgeEditor.learningTab.hoverOpenNode')">{{ item.rec.title || titleOf(item.rec.slug) }}</span>
                    </div>
                    <div class="rec-mini-meta">
                      <span class="rec-reason-tag" :style="reasonStyle(item.rec.reason)">{{ reasonText(item.rec.reason) }}</span>
                      <t-popup trigger="hover" placement="bottom" show-arrow :overlay-style="{ maxWidth: '340px' }">
                        <template #content>
                          <div class="explain">
                            <div class="explain-title">{{ $t('knowledgeEditor.learningTab.explainTitle') }}</div>
                            <div class="explain-line">{{ $t('knowledgeEditor.learningTab.explainPEff', { p: Math.round((item.rec.p_eff ?? 0) * 100) }) }}（{{ $t('knowledgeEditor.learningTab.explainThresholds') }}）</div>
                            <div class="explain-line">{{ $t('knowledgeEditor.learningTab.explainEvidence', { pos: item.rec.positive_count ?? 0, neg: item.rec.negative_count ?? 0 }) }}</div>
                            <div v-if="item.rec.faded" class="explain-line explain-warn">{{ $t('knowledgeEditor.learningTab.explainFaded') }}</div>
                            <div class="explain-line explain-improve">{{ $t('knowledgeEditor.learningTab.explainImprove') }}</div>
                            <!-- 技能矩阵自评轨：挑战这个判断——更熟则抬分+引导验证，更生则按原因降档 -->
                            <div class="explain-self-assess">
                              <span class="esa-label">{{ $t('knowledgeEditor.learningTab.selfAssessSection') }}</span>
                              <div class="esa-buttons">
                                <button class="esa-btn up" @click="confirmSelfAssessUp(item.rec)">{{ $t('knowledgeEditor.learningTab.selfAssessUp') }}</button>
                                <button class="esa-btn down" @click="openSelfAssessDown(item.rec)">{{ $t('knowledgeEditor.learningTab.selfAssessDown') }}</button>
                              </div>
                            </div>
                          </div>
                        </template>
                        <span v-if="item.rec.level" class="rec-level" :style="levelStyle(item.rec.level, item.rec.faded)"
                          :class="{ faded: item.rec.faded }">
                          <span class="tier-dot" :style="{ background: fadedColor(item.rec) }"></span>{{ levelLabel(item.rec.level, item.rec.faded) }}
                        </span>
                      </t-popup>
                      <span class="rec-progress" v-if="item.rec.level && item.rec.level !== 'unseen'">
                        <span class="rec-progress-track">
                          <span class="rec-progress-fill" :style="{ width: Math.round((item.rec.tier_progress ?? 0) * 100) + '%', background: fadedColor(item.rec) }"></span>
                        </span>
                      </span>
                    </div>
                    <div v-if="item.rec.next_tier_hint && item.rec.level" class="rec-hint-line">{{ hintText(item.rec.next_tier_hint) }}</div>
                  </div>
                  <div class="rec-mini-actions" @click.stop>
                    <t-button v-if="item.rec.has_quiz" size="small" @click="startQuiz(item.rec)">
                      {{ item.rec.quiz_count ? $t('knowledgeEditor.learningTab.practiceN', { n: item.rec.quiz_count }) : $t('knowledgeEditor.learningTab.practice') }}
                    </t-button>
                    <t-button v-else size="small" variant="outline" @click="openPage(item.rec.slug)">
                      {{ $t('knowledgeEditor.learningTab.openPage') }}
                    </t-button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <!-- 固定底栏：放大查看时间线与画像；时间线按钮带"今日 N 条"徽标，
             学完回页签不点开抽屉也能一眼看到今天的记录 -->
        <div class="side-foot">
          <t-button size="small" variant="outline" @click="timelineDrawer = true">
            {{ $t('knowledgeEditor.learningTab.timelineTitle') }}
            <span v-if="todayTimelineCount > 0" class="foot-badge"
              :title="$t('knowledgeEditor.learningTab.timelineTodayCount', { n: todayTimelineCount })">
              {{ todayTimelineCount }}
            </span>
          </t-button>
          <t-button size="small" variant="outline" @click="profileDrawer = true">
            {{ $t('knowledgeEditor.learningTab.profileTitle') }}
          </t-button>
        </div>
      </div>
      </template>
      <!-- 知识健康面板：整幅接管星图+数据栏（403/错误在面板内降级为失败文案） -->
      <KnowledgeHealthPanel v-else class="health-host" :kb-id="knowledgeBaseId" @open="openPage" />
    </div>

    <!-- 测验答题抽屉：放大作答，不打断单屏星空布局 -->
    <t-drawer :visible="quiz.active" size="600px" :footer="false"
      :header="$t('knowledgeEditor.learningTab.quizTitle', { slug: quiz.title })" @close="closeQuiz()">
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
          <div v-if="pEffMoveText" class="result-level">{{ pEffMoveText }}</div>
          <div v-if="levelChangeText" class="result-level">{{ levelChangeText }}</div>
          <div v-if="reviewScheduleText" class="result-schedule">{{ reviewScheduleText }}</div>
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
          @click="timelineFilter = f.key">{{ f.label }}</button>
      </div>
      <div v-if="initialLoading" class="section-empty"><t-loading size="small" /></div>
      <div v-else-if="!filteredTimeline.length" class="section-empty">{{ $t('knowledgeEditor.learningTab.timelineEmpty') }}</div>
      <template v-else>
        <div class="tl-node-filters">
          <button v-for="c in timelineNodesVisible" :key="c.slug" class="tl-node-chip"
            :class="{ active: selectedNodeSlugs.has(c.slug) }"
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

    <!-- 画像管理：右栏底部入口点击后放大查看 -->
    <t-drawer v-model:visible="profileDrawer" size="560px" :footer="false"
      :header="$t('knowledgeEditor.learningTab.profileTitle')">
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
    </t-drawer>

    <!-- 自评「更生」原因弹窗：技能矩阵面谈问题——哪方面不熟？四原因各自映射降档力度与内容反馈标签 -->
    <t-dialog v-model:visible="selfAssessDownOpen" :header="$t('knowledgeEditor.learningTab.selfAssessDialogTitle')"
      :confirm-btn="{ content: $t('knowledgeEditor.learningTab.selfAssessSubmit'), disabled: !selfAssessReason }"
      @confirm="submitSelfAssessDown">
      <div class="sa-dialog">
        <div class="sa-node">{{ selfAssessTargetTitle }}</div>
        <div v-for="opt in selfAssessReasonOptions" :key="opt.value" class="sa-option"
          :class="{ selected: selfAssessReason === opt.value }" @click="selfAssessReason = opt.value">
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
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Button as TButton, Dialog as TDialog, Drawer as TDrawer, MessagePlugin, Popup as TPopup, Switch as TSwitch } from 'tdesign-vue-next'
import { ChevronRightIcon, HelpCircleIcon } from 'tdesign-icons-vue-next'
import LearningConstellation from './LearningConstellation.vue'
import KnowledgeHealthPanel from './KnowledgeHealthPanel.vue'
import {
  getLearningProgress, getLearningRecommend, getLearningQuiz, submitLearningAnswer,
  getLearningTimeline, exportLearningProfile, deleteLearningProfile, selfAssess,
  type SelfAssessDownReason,
  getLearningSettings, updateLearningSettings, getLearningMastery, getLearningChanges,
  type LearningProgress, type Recommendation, type QuizQuestion, type AnswerResult, type TimelineItem, type MasteryView, type PassiveChangesSummary,
} from '@/api/learning'

// canManage：owner/admin 才渲染「知识健康」视图切换（健康接口仅 owner/admin 可调）。
const props = defineProps<{ knowledgeBaseId: string; canManage?: boolean }>()
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
// 知识健康视图开关（owner/admin 才渲染切换入口）；默认个人视图。
const healthView = ref(false)
// 权限撤回（canManage 变 false）时退出健康视图：健康接口仅 owner/admin 可调，
// 避免留下一个必然 403 的空态视图。
watch(() => props.canManage, (v) => { if (!v) healthView.value = false })
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
// 知识星图的数据源：与档位清单共用一份缓存，refresh 时后台补拉一次。
const constellationNodes = computed<MasteryView[]>(() => masteryList.value ?? [])
async function ensureMasteryList() {
  if (masteryList.value) return
  try {
    const res = await getLearningMastery(props.knowledgeBaseId)
    masteryList.value = (((res as any).data ?? res) as MasteryView[])
  } catch {
    masteryList.value = []
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
    'self-verify': t('knowledgeEditor.learningTab.reasonSelfVerify'),
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
    // 自评验证轨用与自评按钮一致的紫：用户显式声明 awaiting confirmation
    'self-verify': { background: 'rgba(123, 97, 255, 0.10)', color: '#7b61ff' },
  }
  return map[reason] || { background: 'rgba(0, 0, 0, 0.06)', color: '#666' }
}
function pageTypeOf(slug: string): string {
  const i = slug.indexOf('/')
  return i > 0 ? slug.slice(0, i) : ''
}

// 岗位覆盖计划：推荐按目录分组展示——保持服务端顺序、按首次出现归组，
// 根目录（无 folder_name）沿用 rootUnit 文案；行内排名/结构/操作完全不变。
const groupedRecommendations = computed(() => {
  const groups: { name: string; items: { rec: Recommendation; rank: number }[] }[] = []
  const byName = new Map<string, { name: string; items: { rec: Recommendation; rank: number }[] }>()
  recommendations.value.forEach((rec, rank) => {
    const name = rec.folder_name || t('knowledgeEditor.learningTab.rootUnit')
    let g = byName.get(name)
    if (!g) {
      g = { name, items: [] }
      byName.set(name, g)
      groups.push(g)
    }
    g.items.push({ rec, rank })
  })
  return groups
})

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
  for (const r of recommendations.value) {
    const kind: TodoKind | null =
      r.reason === 'remedial' ? 'remedial'
        : r.reason === 'struggling' ? 'struggling'
          : r.reason === 'prerequisite-stuck' ? 'stuck'
            : null
    if (!kind) continue
    put({ slug: r.slug, title: r.title || r.slug, kind, detail: '', hasQuiz: r.has_quiz })
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
  if (type === 'answer_cite' || type === 'backfill_cite' || type === 'topic_signal' || type === 'wiki_tool_read') return 'cite'
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
    recommendations.value = (r as any).data ?? []
    timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
    timelineTotal.value = ((tl as any).total ?? 0) as number
    passiveChanges.value = ((ch as any).data ?? ch) as PassiveChangesSummary
    // 刷新即回到第 1 页：否则触底续拉会带着旧页码跳页（关闭答题卡/
    // 删除画像后的刷新都走这里）。
    timelinePage.value = 1
    collectDisabled.value = (s as any).data?.collect_disabled ?? false
    masteryList.value = null // 画像可能已变化，档位清单缓存失效
    ensureMasteryList() // 星图随刷新重建
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
async function currentMasteryOf(slug: string): Promise<MasteryView | null> {
  try {
    const res = await getLearningMastery(props.knowledgeBaseId)
    const list = (((res as any).data ?? res) as MasteryView[])
    return list.find((m) => m.slug === slug) || null
  } catch {
    return null
  }
}

// Bug fix: generation counter — when quiz A is closed and quiz B opened
// quickly, A's late-arriving response would overwrite B's items/levelBefore
// (wrong questions displayed under B's slug, wrong mastery comparison).
let quizGeneration = 0
async function startQuiz(rec: Recommendation) {
  const gen = ++quizGeneration
  quiz.value = { active: true, slug: rec.slug, title: rec.title || rec.slug, loading: true, items: [], index: 0, chosen: '', result: null, levelBefore: '', levelAfter: '', levelAfterP: 0, levelBeforeFaded: false, levelAfterFaded: false }
  const [before] = await Promise.all([currentMasteryOf(rec.slug), (async () => {
    try {
      const res = await getLearningQuiz(props.knowledgeBaseId, rec.slug)
      if (gen !== quizGeneration) return // a newer quiz session superseded this one
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
    recommendations.value = (r as any).data ?? []
    timeline.value = ((tl as any).data ?? tl) as TimelineItem[]
    timelineTotal.value = ((tl as any).total ?? 0) as number
    timelinePage.value = 1
    masteryList.value = null // 档位清单缓存失效
    ensureMasteryList()
  } catch (err) {
    console.error('[LearningTab] submit failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.submitFailed'))
  } finally {
    quizSubmitting.value = false
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

// 时间线抽屉首次打开时哨兵才存在于 DOM：挂载后再挂观察器
watch(timelineDrawer, (open) => {
  if (open) nextTick(() => {
    ensureTimelineObserver()
    if (timelineEndRef.value && timelineObserver) timelineObserver.observe(timelineEndRef.value)
  })
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
    MessagePlugin.success(t('knowledgeEditor.learningTab.selfAssessUpDone'))
    await refresh()
    // 引导验证闭环：抬分完成，立刻请用户"做两道题证明"（跨 48h 门控）。
    if (rec.has_quiz) startQuiz(rec)
  } catch (err) {
    console.error('[LearningTab] self-assess up failed:', err)
    MessagePlugin.error(t('knowledgeEditor.learningTab.selfAssessFailed'))
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
.learning-tab { box-sizing: border-box; height: 100%; min-height: 0; padding: 14px 18px; display: flex; flex-direction: column; gap: 12px; }
.sky { position: relative; flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(0, 2fr) minmax(330px, 1fr); gap: 0 18px; animation: learning-enter 0.4s ease both; }
/* 左：星图区（圆图四角天然留空，标题悬浮左上空角） */
.sky-chart { position: relative; min-width: 0; min-height: 0; display: flex; }
.sky-head { position: absolute; top: 6px; left: 6px; right: 6px; z-index: 1; pointer-events: none; display: flex; justify-content: space-between; align-items: flex-start; gap: 8px; }
.sky-head .help-icon { pointer-events: auto; }
/* 视图切换（个人视图 / 知识健康）：tl-filter 同款胶囊按钮，仅 owner/admin 渲染 */
.view-toggle { display: inline-flex; gap: 4px; pointer-events: auto; }
.vt-btn { font-size: 11px; line-height: 1; padding: 4px 9px; border-radius: 999px; border: 1px solid var(--td-component-border, #eee); background: var(--td-bg-color-container, #fff); color: var(--td-text-color-secondary, #666); cursor: pointer; transition: all 0.15s; font-family: inherit; }
.vt-btn:hover { border-color: rgba(7, 192, 95, 0.5); }
.vt-btn.active { background: rgba(7, 192, 95, 0.1); border-color: rgba(7, 192, 95, 0.5); color: #049b38; }
/* 知识健康视图：星图+数据栏的二栏网格让位，头部转为常规行（标题+切换），面板整幅接管 */
.sky.health-view { display: flex; flex-direction: column; gap: 10px; }
.sky.health-view .sky-head { position: static; pointer-events: auto; }
.health-host { flex: 1; min-height: 0; }
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
/* 推荐卡：档位内进度条（快变量）与下档提示 */
.rec-progress { display: inline-flex; align-items: center; }
.rec-progress-track { display: inline-block; width: 72px; height: 4px; border-radius: 2px; background: var(--td-bg-color-component, #f0f0f0); overflow: hidden; }
.rec-progress-fill { display: block; height: 100%; border-radius: 2px; transition: width 0.4s ease; }
.rec-hint-line { font-size: 11px; color: var(--td-text-color-placeholder, #999); line-height: 1.4; }
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
.rec-group { display: flex; flex-direction: column; gap: 8px; }
.rec-group + .rec-group { margin-top: 4px; }
.rec-group-header { font-size: 11px; color: var(--td-text-color-placeholder, #999); font-weight: 500; }
.role-plan-intro { display: flex; align-items: baseline; gap: 6px; flex-wrap: wrap; font-size: 11px; color: var(--td-text-color-placeholder, #999); line-height: 1.5; }
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
.esa-buttons { display: flex; gap: 6px; }
.esa-btn { font-size: 11px; line-height: 1; padding: 5px 10px; border-radius: 4px; cursor: pointer; font-family: inherit; border: 1px solid var(--td-component-border, #eee); background: var(--td-bg-color-container, #fff); color: var(--td-text-color-secondary, #666); transition: all 0.15s; }
.esa-btn.up:hover { border-color: var(--td-brand-color, #07c05f); color: #049b38; }
.esa-btn.down:hover { border-color: #7b61ff; color: #7b61ff; }
.sa-dialog { display: flex; flex-direction: column; gap: 8px; }
.sa-node { font-size: 13px; font-weight: 600; margin-bottom: 2px; }
.sa-option { display: flex; gap: 10px; padding: 8px 10px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s; }
.sa-option:hover { border-color: rgba(123, 97, 255, 0.5); }
.sa-option.selected { border-color: #7b61ff; background: rgba(123, 97, 255, 0.06); }
.sa-radio { width: 14px; height: 14px; border-radius: 50%; border: 1.5px solid var(--td-component-border, #ccc); flex-shrink: 0; margin-top: 2px; }
.sa-option.selected .sa-radio { border-color: #7b61ff; background: radial-gradient(circle, #7b61ff 40%, transparent 45%); }
.sa-option-label { font-size: 13px; font-weight: 500; }
.sa-option-hint { font-size: 11px; color: var(--td-text-color-placeholder, #999); margin-top: 2px; }
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
@media (max-width: 980px) {
  /* 窄屏放弃单屏锁定：恢复自然纵向流，星图给足高度后数据栏自然下排 */
  .learning-tab { height: auto; }
  .sky { display: flex; flex-direction: column; }
  .sky-chart { min-height: 68vw; max-height: 78vh; }
  .sky-head { position: static; }
  .sky-side { border-left: none; padding-left: 0; border-top: 1px solid var(--td-component-border, #eee); padding-top: 12px; }
  .side-scroll { overflow-y: visible; }
}
@media (max-width: 640px) {
  .todo-row { flex-wrap: wrap; }
  .todo-actions { margin-left: 0; width: 100%; justify-content: flex-start; }
  .rec-mini-actions { flex-direction: row; }
  .timeline-item { flex-wrap: wrap; height: auto; }
  .tl-slug { min-width: 0; max-width: 180px; }
}
</style>
