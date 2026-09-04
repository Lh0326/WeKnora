<template>
  <!-- 知识资产健康面板：owner/admin 的组织视角总览。视觉语言沿用 LearningTab
       的 side-block / unit-bar / todo-row 系（类名前缀 hp- 隔离作用域），
       色板只复用既有值（td-* 变量 + 琥珀 #a87b10 / 警示红 #c8383f 标签底）。 -->
  <div class="health-panel">
    <!-- 加载 / 失败态：健康接口仅 owner/admin 可调，403 或任何错误都静默降级为
         失败文案 + 重试入口，绝不伪装成"没有数据"，也绝不向父组件抛错 -->
    <div v-if="loading" class="hp-state"><t-loading size="large" /></div>
    <div v-else-if="!health" class="hp-state">
      <div class="hp-error">{{ $t('knowledgeEditor.learningTab.healthLoadFailed') }}</div>
      <t-button size="small" variant="outline" @click="refresh()">{{ $t('knowledgeEditor.learningTab.retry') }}</t-button>
    </div>
    <div v-else class="hp-scroll">
      <!-- 汇总 chips：节点总数 / 已覆盖 / 有学习记录人数 -->
      <div class="hp-chips">
        <span class="hp-chip">{{ $t('knowledgeEditor.learningTab.healthSummaryNodes', { total: health.nodes_total }) }}</span>
        <span class="hp-chip">{{ $t('knowledgeEditor.learningTab.healthSummaryCovered', { n: health.nodes_covered }) }}</span>
        <span class="hp-chip">{{ $t('knowledgeEditor.learningTab.healthSummarySubjects', { n: health.subjects_active }) }}</span>
      </div>

      <!-- 目录覆盖度：名称 + covered/total + 细进度条（unit-bar 同款） -->
      <div v-if="health.folders.length" class="hp-block">
        <div class="hp-title">{{ $t('knowledgeEditor.learningTab.healthFoldersTitle') }}</div>
        <div v-for="f in health.folders" :key="f.folder_id || '__root__'" class="hp-folder">
          <span class="hp-folder-name" :title="f.folder_name || $t('knowledgeEditor.learningTab.rootUnit')">
            {{ f.folder_name || $t('knowledgeEditor.learningTab.rootUnit') }}
          </span>
          <div class="hp-folder-bar">
            <div class="hp-folder-fill" :style="{ width: folderPercent(f) + '%' }"></div>
          </div>
          <span class="hp-folder-count">
            {{ $t('knowledgeEditor.learningTab.healthFolderCovered', { covered: f.covered_nodes, total: f.total_nodes }) }}
          </span>
        </div>
      </div>

      <!-- 知识风险：kind 标签（单点=琥珀系 / 遗忘=警示红系）+ 标题 + 熟悉人数 + 最高熟悉度 -->
      <div class="hp-block">
        <div class="hp-title">{{ $t('knowledgeEditor.learningTab.healthRisksTitle') }}</div>
        <div v-if="!health.risks.length" class="hp-empty">{{ $t('knowledgeEditor.learningTab.risksEmpty') }}</div>
        <div v-for="r in health.risks" :key="r.kind + '-' + r.key" class="hp-risk"
          :class="{ clickable: r.kind === 'single_point' }"
          :title="r.kind === 'single_point' ? $t('knowledgeEditor.learningTab.hoverOpenNode') : ''"
          @click="r.kind === 'single_point' && emit('open', r.key)">
          <span class="hp-risk-tag" :class="r.kind === 'stale_doc' ? 'stale' : 'single'">{{ riskLabel(r.kind) }}</span>
          <span class="hp-risk-title" :title="r.title || r.key">{{ r.title || r.key }}</span>
          <span class="hp-risk-meta">
            {{ $t('knowledgeEditor.learningTab.healthExpertCount', { n: r.familiar_count }) }} · {{ Math.round(r.best_p_eff * 100) }}%
          </span>
        </div>
      </div>

      <!-- 治理收件箱：维护标记按 kind 归组（保持服务端顺序、按首次出现分组） -->
      <div class="hp-block">
        <div class="hp-title">{{ $t('knowledgeEditor.learningTab.healthMaintenanceTitle') }}</div>
        <div v-if="!health.maintenance.length" class="hp-empty">{{ $t('knowledgeEditor.learningTab.maintEmpty') }}</div>
        <div v-for="g in maintenanceGroups" :key="g.kind" class="hp-maint-group">
          <div class="hp-maint-kind">{{ g.label }}</div>
          <div v-for="m in g.items" :key="m.kind + '-' + m.slug" class="hp-maint-row"
            :title="$t('knowledgeEditor.learningTab.hoverOpenNode')" @click="emit('open', m.slug)">
            <span class="hp-maint-title">{{ m.title || m.slug }}</span>
            <span class="hp-maint-meta">×{{ m.count }} · {{ $t('knowledgeEditor.learningTab.maintLatestAt', { t: formatTime(m.latest_at) }) }}</span>
          </div>
        </div>
      </div>

      <!-- 专家分布：紧凑行（标题 + n 人熟悉 chip），点击直达节点页面（与推荐行同通道） -->
      <div class="hp-block">
        <div class="hp-title">{{ $t('knowledgeEditor.learningTab.healthExpertsTitle') }}</div>
        <div v-if="!health.experts.length" class="hp-empty">{{ $t('knowledgeEditor.learningTab.expertsEmpty') }}</div>
        <div v-for="e in health.experts" :key="e.slug" class="hp-expert"
          :title="$t('knowledgeEditor.learningTab.hoverOpenNode')" @click="emit('open', e.slug)">
          <span class="hp-expert-title">{{ e.title || e.slug }}</span>
          <span class="hp-expert-chip">{{ $t('knowledgeEditor.learningTab.healthExpertCount', { n: e.familiar_count }) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button as TButton } from 'tdesign-vue-next'
import { getKnowledgeHealth, type HealthMaintenanceMark, type KnowledgeHealth } from '@/api/learning'

const props = defineProps<{ kbId: string }>()
// 专家/风险（单点）/维护标记行点击直达节点页面：与推荐行共用同一 open 通道。
const emit = defineEmits<{ (e: 'open', slug: string): void }>()
const { locale, t } = useI18n()

const health = ref<KnowledgeHealth | null>(null)
const loading = ref(true)

async function refresh() {
  loading.value = true
  try {
    const res = await getKnowledgeHealth(props.kbId)
    health.value = (((res as any).data ?? res) as KnowledgeHealth)
  } catch (err) {
    // 403（非 owner/admin）或任何网络错误：静默降级为失败文案，绝不向上抛。
    console.error('[KnowledgeHealthPanel] load failed:', err)
    health.value = null
  } finally {
    loading.value = false
  }
}

defineExpose({ refresh })

onMounted(refresh)

function folderPercent(f: { covered_nodes: number; total_nodes: number }): number {
  return f.total_nodes ? Math.round((f.covered_nodes / f.total_nodes) * 100) : 0
}

// 风险含义只按 kind 经 i18n 渲染；后端 note 是英文兜底，仅日志排查用，绝不展示。
function riskLabel(kind: string): string {
  return kind === 'stale_doc'
    ? t('knowledgeEditor.learningTab.riskStaleDoc')
    : t('knowledgeEditor.learningTab.riskSinglePoint')
}

function maintLabel(kind: string): string {
  const map: Record<string, string> = {
    self_assess_up: t('knowledgeEditor.learningTab.maintPendingVerify'),
    self_assess_down_all: t('knowledgeEditor.learningTab.maintDownAll'),
    self_assess_down_doc_gap: t('knowledgeEditor.learningTab.maintDocGap'),
    self_assess_down_doc_updated: t('knowledgeEditor.learningTab.maintDocUpdated'),
    self_assess_down_quiz_easy: t('knowledgeEditor.learningTab.maintQuizEasy'),
  }
  return map[kind] || kind
}

// 维护标记按 kind 归组：组序 = 该 kind 在服务端结果中的首次出现（确定性）。
const maintenanceGroups = computed(() => {
  const list = health.value?.maintenance ?? []
  const order: string[] = []
  const byKind = new Map<string, HealthMaintenanceMark[]>()
  for (const m of list) {
    if (!byKind.has(m.kind)) {
      byKind.set(m.kind, [])
      order.push(m.kind)
    }
    byKind.get(m.kind)!.push(m)
  }
  return order.map((kind) => ({ kind, label: maintLabel(kind), items: byKind.get(kind)! }))
})

// 与 LearningTab.formatTime 同款：Intl 按当前 locale 的短日期时间格式。
function formatTime(iso: string): string {
  try {
    return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(iso))
  } catch {
    return iso
  }
}
</script>

<style scoped>
/* 单栏滚动布局：与 LearningTab.side-scroll 同一视觉节奏 */
.health-panel { height: 100%; min-height: 0; display: flex; flex-direction: column; }
.hp-state { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px; }
.hp-error { color: var(--td-text-color-secondary, #666); font-size: 13px; }
.hp-scroll { flex: 1; min-height: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 14px; padding-right: 10px; scrollbar-width: thin; }
/* 汇总 chips */
.hp-chips { display: flex; gap: 8px; flex-wrap: wrap; }
.hp-chip {
  font-size: 12px; line-height: 1; padding: 6px 10px; border-radius: 999px;
  border: 1px solid var(--td-component-border, #eee); background: var(--td-bg-color-container, #fff);
  color: var(--td-text-color-secondary, #666); white-space: nowrap;
}
/* 分区标题：side-title 同款品牌竖条 */
.hp-block { display: flex; flex-direction: column; gap: 8px; }
.hp-title { font-size: 12px; font-weight: 600; color: var(--td-text-color-secondary, #555); display: flex; align-items: center; gap: 6px; }
.hp-title::before { content: ''; width: 3px; height: 12px; border-radius: 2px; background: var(--td-brand-color, #07c05f); flex-shrink: 0; }
.hp-empty { color: var(--td-text-color-placeholder, #999); font-size: 13px; }
/* 目录覆盖度：unit-row / unit-bar 同款 */
.hp-folder { display: flex; align-items: center; gap: 8px; font-size: 12px; }
.hp-folder-name { width: 84px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--td-text-color-secondary, #666); flex-shrink: 0; }
.hp-folder-bar { flex: 1; height: 6px; border-radius: 3px; background: var(--td-bg-color-component, #f0f0f0); overflow: hidden; }
.hp-folder-fill { height: 100%; background: var(--td-brand-color, #07c05f); border-radius: 3px; transition: width 0.4s ease; }
.hp-folder-count { color: var(--td-text-color-secondary, #666); flex-shrink: 0; }
/* 知识风险：todo-row 同款行 + 既有琥珀/警示红标签底 */
.hp-risk { display: flex; align-items: center; gap: 10px; padding: 7px 10px; border: 1px solid var(--td-component-border, #eee); border-radius: 8px; flex-wrap: wrap; }
.hp-risk.clickable { cursor: pointer; transition: border-color 0.15s; }
.hp-risk.clickable:hover { border-color: rgba(7, 192, 95, 0.5); }
.hp-risk-tag { font-size: 11px; line-height: 1.4; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0; max-width: 220px; overflow: hidden; text-overflow: ellipsis; }
.hp-risk-tag.single { background: rgba(212, 160, 23, 0.12); color: #a87b10; }
.hp-risk-tag.stale { background: rgba(213, 73, 65, 0.10); color: #c8383f; }
.hp-risk-title { font-size: 13px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }
.hp-risk-meta { font-size: 11px; color: var(--td-text-color-placeholder, #999); white-space: nowrap; flex-shrink: 0; }
/* 治理收件箱：kind 小标题 + 明细行 */
.hp-maint-group { display: flex; flex-direction: column; gap: 6px; }
.hp-maint-kind { font-size: 11px; color: var(--td-text-color-placeholder, #999); font-weight: 500; }
.hp-maint-row {
  display: flex; align-items: center; gap: 10px; padding: 6px 10px; font-size: 12px;
  border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s;
}
.hp-maint-row:hover { border-color: rgba(7, 192, 95, 0.5); }
.hp-maint-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.hp-maint-meta { font-size: 11px; color: var(--td-text-color-placeholder, #999); white-space: nowrap; flex-shrink: 0; }
/* 专家分布：紧凑行 + n 人熟悉 chip */
.hp-expert {
  display: flex; align-items: center; gap: 10px; padding: 6px 10px; font-size: 12px;
  border: 1px solid var(--td-component-border, #eee); border-radius: 8px; cursor: pointer; transition: border-color 0.15s;
}
.hp-expert:hover { border-color: rgba(7, 192, 95, 0.5); }
.hp-expert-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
.hp-expert-chip {
  font-size: 11px; line-height: 1; padding: 3px 8px; border-radius: 4px; white-space: nowrap; flex-shrink: 0;
  background: rgba(7, 192, 95, 0.10); color: #049b38;
}
@media (prefers-reduced-motion: reduce) {
  .hp-folder-fill { transition: none; }
}
</style>
