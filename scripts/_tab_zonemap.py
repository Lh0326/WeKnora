# -*- coding: utf-8 -*-
"""Wire LearningTab to the zone-map: sidebar zone cards + 3D constellation props."""
import io

p = 'frontend/src/views/knowledge/learning/LearningTab.vue'
src = io.open(p, encoding='utf-8').read()

def sub(old, new, tag, count=1):
    global src
    assert src.count(old) == count, f'anchor fail ({tag}): count={src.count(old)}'
    src = src.replace(old, new)
    print('ok:', tag)

# ---- 1. imports + zone-map state ----
sub("""  getLearningProgress, getLearningRecommend, getLearningQuiz, submitLearningAnswer,""",
    """  getLearningProgress, getLearningZoneMap, getLearningQuiz, submitLearningAnswer,""",
    'api import')
sub("""  type LearningProgress, type Recommendation, type QuizQuestion, type AnswerResult, type TimelineItem, type MasteryView, type PassiveChangesSummary,
} from '@/api/learning'""",
    """  type LearningProgress, type Recommendation, type QuizQuestion, type AnswerResult, type TimelineItem, type MasteryView, type PassiveChangesSummary,
  type ZoneMapResponse, type ZoneSummary,
} from '@/api/learning'""",
    'api types')

sub("""const recommendations = ref<Recommendation[]>([])""",
    """const recommendations = ref<Recommendation[]>([])
// 模块分区视图：每个 wiki 目录一个知识区，各区自己的"1/2"+全区节点+先修边。
const zoneMap = ref<ZoneMapResponse | null>(null)
// 悬停右侧栏分区卡片时，星图仅亮该分区并标记其"1"。
const highlightZone = ref<string | null>(null)""",
    'zone state')

# ---- 2. constellation props: nodes/edges/zones/highlight from zone-map ----
sub("""            <LearningConstellation :nodes="constellationNodes" @open="openPage" />""",
    """            <LearningConstellation :nodes="constellationNodes" :edges="zoneEdges" :zones="zoneAxes"
              :highlight-zone="highlightZone" @open="openPage" />""",
    'constellation props')

sub("""const constellationNodes = computed<MasteryView[]>(() => masteryList.value ?? [])""",
    """// 星图数据源：zone-map 的全量节点（带分区/章节/位次），悬停徽标的自评
// 状态从掌握度列表补齐（两份缓存同一份后端事实）。
const constellationNodes = computed(() =>
  (zoneMap.value?.nodes ?? []).map((n) => ({
    ...n,
    self_assess: masteryList.value?.find((m) => m.slug === n.slug)?.self_assess,
  })))
const zoneEdges = computed(() => zoneMap.value?.edges ?? [])
const zoneAxes = computed(() =>
  (zoneMap.value?.zones ?? []).map((z) => ({
    id: z.folder_id,
    name: z.folder_name || t('knowledgeEditor.learningTab.rootUnit'),
    next: z.next?.slug ?? null,
  })))""",
    'constellation computed')

# ---- 3. refresh(): zone-map replaces the linear recommend fetch ----
sub("""      getLearningProgress(props.knowledgeBaseId),
      getLearningRecommend(props.knowledgeBaseId, 5),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
      getLearningSettings(),
      getLearningChanges(props.knowledgeBaseId, 20),
    ])
    progress.value = (p as any).data ?? p
    recommendations.value = (r as any).data ?? []""",
    """      getLearningProgress(props.knowledgeBaseId),
      getLearningZoneMap(props.knowledgeBaseId),
      getLearningTimeline(props.knowledgeBaseId, 1, TIMELINE_PAGE_SIZE),
      getLearningSettings(),
      getLearningChanges(props.knowledgeBaseId, 20),
    ])
    progress.value = (p as any).data ?? p
    zoneMap.value = (r as any).data ?? null
    recommendations.value = ((r as any).data?.zones ?? [])
      .map((z: ZoneSummary) => z.next)
      .filter((x: Recommendation | null): x is Recommendation => !!x)""",
    'refresh fetch')

# submitAnswer's post-answer refresh
sub("""      getLearningRecommend(props.knowledgeBaseId, 5),""",
    """      getLearningZoneMap(props.knowledgeBaseId),""",
    'submit refresh fetch')
sub("""    quiz.value.levelAfterP = after?.p_eff ?? 0
    recommendations.value = (r as any).data ?? []""",
    """    quiz.value.levelAfterP = after?.p_eff ?? 0
    zoneMap.value = (r as any).data ?? null
    recommendations.value = ((r as any).data?.zones ?? [])
      .map((z: ZoneSummary) => z.next)
      .filter((x: Recommendation | null): x is Recommendation => !!x)""",
    'submit refresh set')

# ---- 4. sidebar template: zone cards replace the flat list ----
old_block_start = src.find("""            <div v-if="recommendations.length" class="role-plan-intro">""")
old_block_end = src.find("""        </div>
        <!-- 固定底栏：放大查看时间线与画像；时间线按钮带"今日 N 条"徽标，""")
assert old_block_start != -1 and old_block_end != -1
new_block = """            <div v-if="zoneList.length" class="role-plan-intro">
              <span class="role-plan-name">{{ $t('knowledgeEditor.learningTab.zoneIntro') }}</span>
            </div>
            <div v-if="recommendLoading" class="section-empty">{{ $t('common.loading') }}</div>
            <div v-else-if="!zoneList.length" class="section-empty">{{ $t('knowledgeEditor.learningTab.recommendEmpty') }}</div>
            <div v-else class="zone-card-list">
              <div v-for="zone in zoneList" :key="zone.folder_id || 'root'" class="zone-card"
                :class="{ hot: highlightZone === (zone.folder_id || '') }"
                @mouseenter="highlightZone = zone.folder_id || ''" @mouseleave="highlightZone = null">
                <div class="zone-card-head">
                  <span class="zone-card-name" :title="zone.folder_name || $t('knowledgeEditor.learningTab.rootUnit')">
                    {{ zone.folder_name || $t('knowledgeEditor.learningTab.rootUnit') }}
                  </span>
                  <span class="zone-card-count">{{ zone.lit }}/{{ zone.total }}</span>
                </div>
                <template v-if="zone.next">
                  <div class="zone-next" role="button" tabindex="0"
                    @click="openPage(zone.next.slug)" @keydown.enter.prevent="openPage(zone.next.slug)">
                    <span class="zone-next-mark">①</span>
                    <span class="rec-reason-tag" :style="reasonStyle(zone.next.reason)">{{ reasonText(zone.next.reason) }}</span>
                    <span class="zone-next-title" :title="$t('knowledgeEditor.learningTab.hoverOpenNode')">
                      {{ zone.next.title || zone.next.slug }}
                    </span>
                  </div>
                  <div v-if="whyText(zone.next)" class="rec-why-line">{{ whyText(zone.next) }}</div>
                  <div v-if="zone.second" class="zone-then" role="button" tabindex="0"
                    @click="openPage(zone.second.slug)" @keydown.enter.prevent="openPage(zone.second.slug)"
                    :title="$t('knowledgeEditor.learningTab.hoverOpenNode')">
                    <span class="zone-then-mark">②</span>
                    <span class="zone-then-title">{{ zone.second.title || zone.second.slug }}</span>
                    <span class="zone-then-reason">{{ reasonText(zone.second.reason) }}</span>
                  </div>
                  <div class="zone-actions">
                    <t-button v-if="zone.next.has_quiz" size="small"
                      :theme="zone.next.reason === 'self-verify' ? 'primary' : 'default'"
                      @click="startQuiz(zone.next)">
                      {{ zone.next.reason === 'self-verify'
                        ? $t('knowledgeEditor.learningTab.selfVerifyGoVerify')
                        : zone.next.quiz_count ? $t('knowledgeEditor.learningTab.practiceN', { n: zone.next.quiz_count }) : $t('knowledgeEditor.learningTab.practice') }}
                    </t-button>
                    <t-button v-else size="small" variant="outline" @click="openPage(zone.next.slug)">
                      {{ $t('knowledgeEditor.learningTab.openPage') }}
                    </t-button>
                  </div>
                </template>
                <div v-else class="zone-done">{{ $t('knowledgeEditor.learningTab.zoneDone') }}</div>
              </div>
            </div>
          </div>
"""
src = src[:old_block_start] + new_block + src[old_block_end:]
print('ok: sidebar zone cards')

# ---- 5. zoneList computed + todayTodo from zone data ----
sub("""const zoneAxes = computed(() =>
  (zoneMap.value?.zones ?? []).map((z) => ({
    id: z.folder_id,
    name: z.folder_name || t('knowledgeEditor.learningTab.rootUnit'),
    next: z.next?.slug ?? null,
  })))""",
    """const zoneAxes = computed(() =>
  (zoneMap.value?.zones ?? []).map((z) => ({
    id: z.folder_id,
    name: z.folder_name || t('knowledgeEditor.learningTab.rootUnit'),
    next: z.next?.slug ?? null,
  })))
const zoneList = computed<ZoneSummary[]>(() => zoneMap.value?.zones ?? [])""",
    'zoneList computed')

sub("""  for (const r of recommendations.value) {
    const kind: TodoKind | null =
      r.reason === 'remedial' ? 'remedial'
        : r.reason === 'struggling' ? 'struggling'
          : r.reason === 'prerequisite-stuck' ? 'stuck'
            : null
    if (!kind) continue
    put({ slug: r.slug, title: r.title || r.slug, kind, detail: '', hasQuiz: r.has_quiz })
  }""",
    """  // 分区视图下的错题/阻塞来源：各区的"1/2"携带同一套多通道理由。
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
  }""",
    'todayTodo source')

# ---- 6. zone card styles ----
sub(""".rec-why-line { font-size: 11px; color: var(--td-text-color-secondary, #666); line-height: 1.5; margin-top: 1px; overflow-wrap: anywhere; }""",
    """.rec-why-line { font-size: 11px; color: var(--td-text-color-secondary, #666); line-height: 1.5; margin-top: 1px; overflow-wrap: anywhere; }
/* 模块分区卡：并列独立的"1"，悬停联动星图高亮 */
.zone-card-list { display: flex; flex-direction: column; gap: 8px; }
.zone-card { border: 1px solid var(--td-component-border, #eee); border-radius: 10px; padding: 8px 10px; display: flex; flex-direction: column; gap: 5px; transition: border-color 0.15s, box-shadow 0.15s; }
.zone-card:hover, .zone-card.hot { border-color: rgba(7, 192, 95, 0.55); box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.08); }
.zone-card-head { display: flex; align-items: center; gap: 8px; }
.zone-card-name { font-size: 12px; font-weight: 600; color: var(--td-text-color-primary, #333); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.zone-card-count { margin-left: auto; font-size: 11px; color: var(--td-text-color-placeholder, #999); white-space: nowrap; }
.zone-next { display: flex; align-items: center; gap: 6px; cursor: pointer; flex-wrap: wrap; }
.zone-next-mark { font-size: 13px; color: #049b38; font-weight: 700; }
.zone-next-title { font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
.zone-then { display: flex; align-items: center; gap: 6px; cursor: pointer; padding-left: 2px; }
.zone-then-mark { font-size: 11px; color: var(--td-text-color-placeholder, #999); font-weight: 600; }
.zone-then-title { font-size: 12px; color: var(--td-text-color-secondary, #555); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.zone-then-reason { font-size: 10px; color: var(--td-text-color-placeholder, #999); white-space: nowrap; }
.zone-actions { display: flex; gap: 6px; }
.zone-done { font-size: 11px; color: var(--td-text-color-placeholder, #999); }""",
    'zone styles')

io.open(p, 'w', encoding='utf-8', newline='\n').write(src)
print('ALL TAB PATCHES DONE')
