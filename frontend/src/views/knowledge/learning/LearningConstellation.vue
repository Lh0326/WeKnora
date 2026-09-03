<template>
  <div class="constellation-wrap">
    <svg :viewBox="`0 0 ${layout.size} ${layout.size}`" class="constellation-svg" role="img"
      :aria-label="$t('knowledgeEditor.learningTab.constellationTitle')">
      <defs>
        <radialGradient id="cs-bg" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stop-color="rgba(7, 192, 95, 0.07)" />
          <stop offset="70%" stop-color="rgba(7, 192, 95, 0.02)" />
          <stop offset="100%" stop-color="transparent" />
        </radialGradient>
        <filter id="cs-glow" x="-60%" y="-60%" width="220%" height="220%">
          <feGaussianBlur stdDeviation="4" result="blur" />
          <feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge>
        </filter>
      </defs>

    <!-- 背景辐射渐变 + 四条环带参考线（从内到外：掌握→未接触）；
         每条环带以不同周期慢速流转（内快外慢），像轨道一样让整图持续微动 -->
    <circle class="cs-bg" :cx="layout.center" :cy="layout.center" :r="layout.center" fill="url(#cs-bg)" />
    <circle v-for="band in layout.bands" :key="band.tier" class="cs-ring-guide"
      :cx="layout.center" :cy="layout.center" :r="band.radius"
      :stroke="tierColor(band.tier)" :style="ringStyle(band)" />

    <!-- 已点亮节点到中心的微光线束：星座感，同时把"向内推进"读出来 -->
    <line v-for="p in litPlaced" :key="`spoke-${p.node.slug}`" class="cs-spoke"
      :x1="layout.center" :y1="layout.center" :x2="p.x" :y2="p.y" :stroke="tierColor(p.tier)" />

    <!-- 中心枢纽：点亮/总数 + 已掌握数；外圈呼吸涟漪向外缓释 -->
    <circle class="cs-hub-halo" :cx="layout.center" :cy="layout.center" r="46" />
    <g class="cs-hub">
      <circle :cx="layout.center" :cy="layout.center" r="40" class="cs-hub-disc" />
      <text :x="layout.center" :y="layout.center - 4" text-anchor="middle" class="cs-hub-num">{{ litCount }}/{{ totalCount }}</text>
      <text :x="layout.center" :y="layout.center + 14" text-anchor="middle" class="cs-hub-sub">{{ $t('knowledgeEditor.learningTab.constellationLit') }}</text>
    </g>

    <!-- 环带标签（置于各环正上方，带计数） -->
    <text v-for="band in layout.bands" :key="`label-${band.tier}`"
      :x="layout.center" :y="layout.center - band.radius - 8" text-anchor="middle" class="cs-band-label"
      :fill="tierColor(band.tier)">{{ bandLabel(band) }}</text>

    <!-- 知识节点：半径=证据量；虚线描边=低置信；琥珀=已淡化；
         动效双通道刻意分离——呼吸=形状（缩放，慢节奏 4-6s）所有人都有；
         闪烁=光（雷达 ping 环 + 亮度闪光，快节奏 1.6s）只属于近 48h 学过的
         节点：不同运动类型 + 不同节奏 + 静态绿色标签，三重线索一眼可辨 -->
    <g v-for="(p, i) in layout.placed" :key="p.node.slug"
      class="cs-node" :class="{ recent: p.recent, faded: p.faded }"
      :style="{ animationDelay: `${Math.min(i * 40, 500)}ms`, '--breathe-delay': `${(-(i * 0.53) % 4.2).toFixed(2)}s`, '--breathe-dur': `${(4.4 + (i % 5) * 0.5).toFixed(2)}s` }"
      @click="emit('open', p.node.slug)">
      <circle v-if="p.recent" class="cs-ping" :cx="p.x" :cy="p.y" :r="p.r + 3" />
      <circle :cx="p.x" :cy="p.y" :r="p.r" class="cs-node-disc"
        :fill="p.faded ? '#d4a017' : tierColor(p.tier)"
        :stroke-dasharray="p.node.low_confidence ? '2 3' : 'none'"
        :filter="p.tier === 'mastered' ? 'url(#cs-glow)' : undefined" />
      <text :x="p.x" :y="p.y + p.r + 13" text-anchor="middle" class="cs-node-label">{{ nodeLabel(p.node.title, p.node.slug) }}</text>
      <!-- 原生 SVG 悬停提示：全部中文（标题/掌握度/证据量） -->
      <title>{{ hoverText(p) }}</title>
    </g>
    </svg>
    <div class="cs-caption">{{ $t('knowledgeEditor.learningTab.constellationHint') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { layoutConstellation, nodeLabel, type ConstellationNode, type Tier } from './constellationLayout'
import type { MasteryView } from '@/api/learning'

const props = defineProps<{ nodes: MasteryView[] }>()
const emit = defineEmits<{ (e: 'open', slug: string): void }>()
const { t } = useI18n()

const TIER_COLORS: Record<Tier, string> = {
  unseen: '#d0d0d0',
  touched: '#8ce0af',
  familiar: '#07c05f',
  mastered: '#038626',
}
function tierColor(tier: Tier): string {
  return TIER_COLORS[tier] || TIER_COLORS.unseen
}

const layout = computed(() => layoutConstellation(props.nodes as ConstellationNode[]))
const litPlaced = computed(() => layout.value.placed.filter((p) => p.tier !== 'unseen' && !p.faded))
const litCount = computed(() => litPlaced.value.length)
const totalCount = computed(() => layout.value.placed.length)

function bandLabel(band: { tier: Tier; count: number }): string {
  const names: Record<Tier, string> = {
    unseen: t('knowledgeEditor.learningTab.tierUnseen'),
    touched: t('knowledgeEditor.learningTab.tierTouched'),
    familiar: t('knowledgeEditor.learningTab.tierFamiliar'),
    mastered: t('knowledgeEditor.learningTab.tierMastered'),
  }
  return `${names[band.tier]} ${band.count}`
}

function hoverText(p: { node: ConstellationNode; tier: Tier; faded: boolean }): string {
  const lines = [
    p.node.title || nodeLabel(p.node.title, p.node.slug),
    t('knowledgeEditor.learningTab.constellationHoverP', { p: Math.round((p.node.p_eff ?? 0) * 100) }),
    t('knowledgeEditor.learningTab.constellationHoverN', { n: p.node.evidence_count ?? 0 }),
  ]
  if (p.faded) lines.push(t('knowledgeEditor.learningTab.tierFaded'))
  if (p.node.self_assess) lines.push(selfAssessLine(p.node.self_assess!))
  return lines.join('\n')
}

// 环带流转周期：内环快、外环慢（开普勒式轨道感），虚线周期 9 的整数倍保证无缝循环

// Hover line 4: the self-assessment echo (skills-matrix challenge track).
function selfAssessLine(m: { direction: string; event_type: string }): string {
  if (m.direction === 'up') return t('knowledgeEditor.learningTab.saBadgeUp')
  const reasonMap: Record<string, string> = {
    self_assess_down_all: 'saBadgeDownAll',
    self_assess_down_doc_gap: 'saBadgeDownDocGap',
    self_assess_down_doc_updated: 'saBadgeDownDocUpdated',
    self_assess_down_quiz_easy: 'saBadgeDownQuizEasy',
  }
  return t('knowledgeEditor.learningTab.' + (reasonMap[m.event_type] || 'saBadgeDownAll'))
}
function ringStyle(band: { radius: number }) {
  return { '--orbit-dur': `${(Math.round(9 * Math.ceil((16 + band.radius / 8) / 9))).toFixed(0)}s` }
}
</script>

<style scoped>
.constellation-wrap { position: relative; height: 100%; width: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.constellation-svg { width: 100%; height: 100%; max-width: none; display: block; }
/* 背景辉光整体缓释：整幅星域的最底层呼吸 */
.cs-bg { animation: cs-bg-breathe 11s ease-in-out infinite; }
@keyframes cs-bg-breathe { 0%, 100% { opacity: 0.72; } 50% { opacity: 1; } }
/* 环带：透明度呼吸 + 虚线极慢流转（周期为虚线节距 9 的整数倍，衔接无跳变） */
.cs-ring-guide {
  fill: none; stroke-width: 1; stroke-dasharray: 3 6; opacity: 0.35;
  transform-box: fill-box; transform-origin: center;
  animation: cs-ring-breathe 9s ease-in-out infinite, cs-ring-orbit var(--orbit-dur, 27s) linear infinite;
}
@keyframes cs-ring-breathe { 0%, 100% { opacity: 0.16; } 50% { opacity: 0.5; } }
@keyframes cs-ring-orbit { to { stroke-dashoffset: -54; } }
.cs-spoke { stroke-width: 1; animation: cs-spoke-breathe 8s ease-in-out infinite; }
@keyframes cs-spoke-breathe { 0%, 100% { opacity: 0.07; } 50% { opacity: 0.22; } }
/* 中心涟漪：一圈光环由内向外缓释淡出，节奏与呼吸同源 */
.cs-hub-halo {
  fill: none; stroke: var(--td-brand-color, #07c05f); stroke-width: 1;
  transform-box: fill-box; transform-origin: center;
  animation: cs-halo 4.8s ease-out infinite;
}
@keyframes cs-halo {
  0% { transform: scale(0.78); opacity: 0.42; }
  75% { transform: scale(1.26); opacity: 0; }
  100% { transform: scale(1.26); opacity: 0; }
}
.cs-hub-disc { fill: var(--td-bg-color-container, #fff); stroke: var(--td-brand-color, #07c05f); stroke-width: 2; }
.cs-hub-num { font-size: 18px; font-weight: 600; fill: var(--td-text-color-primary, #333); }
.cs-hub-sub { font-size: 9px; fill: var(--td-text-color-placeholder, #999); }
.cs-band-label { font-size: 10px; opacity: 0.75; }
.cs-node { cursor: pointer; transition: opacity 0.15s; animation: cs-node-in 0.5s ease both; transform-box: fill-box; transform-origin: center; }
.cs-node:hover { opacity: 0.85; }
.cs-node:hover .cs-node-disc { stroke: var(--td-brand-color, #07c05f); stroke-width: 2; }
.cs-node-disc {
  stroke: rgba(0, 0, 0, 0.12); stroke-width: 0.5;
  transform-box: fill-box; transform-origin: center;
  animation: cs-breathe var(--breathe-dur, 4.8s) ease-in-out infinite var(--breathe-delay, 0s);
}
/* 48h 标记 ①：雷达 ping 环——由内向外扩散淡出（1.6s 快节奏），
   与呼吸的"原地缓慢缩放"是不同的运动类型，不会认错 */
.cs-ping {
  fill: none; stroke: var(--td-brand-color, #07c05f); stroke-width: 1.5;
  transform-box: fill-box; transform-origin: center; pointer-events: none;
  animation: cs-ping 1.6s cubic-bezier(0.22, 0.61, 0.36, 1) infinite;
}
@keyframes cs-ping {
  0% { transform: scale(0.72); opacity: 0.95; }
  70% { transform: scale(1.6); opacity: 0; }
  100% { transform: scale(1.6); opacity: 0; }
}
/* 48h 标记 ②：亮度闪光。呼吸的 keyframes 刻意不含 opacity（亮度通道
   独占给闪烁），闪烁也不再叠加描边加粗（描边加粗与缩放呼吸视觉相近，
   是此前"分不清闪烁和呼吸"的主因） */
.cs-node.recent .cs-node-disc {
  animation: cs-breathe var(--breathe-dur, 4.8s) ease-in-out infinite var(--breathe-delay, 0s),
    cs-twinkle 1.6s ease-in-out infinite;
}
/* 48h 标记 ③：静态线索——近学节点的标签用品牌绿（reduced-motion 下也可辨） */
.cs-node.recent .cs-node-label { fill: #049b38; }
/* 已淡化节点：只缩放不提透明度，保留琥珀暗淡观感 */
.cs-node.faded .cs-node-disc { animation-name: cs-breathe-dim; opacity: 0.8; }
/* 已淡化 + 近48h学过：暗淡呼吸之上闪烁必须存活——单一 animation-name
   会整体覆盖名称列表，把 recent 的闪烁一并抹掉（曾导致"学了却不闪"） */
.cs-node.faded.recent .cs-node-disc { animation-name: cs-breathe-dim, cs-twinkle; }
/* 呼吸=形状通道：只做缩放，节奏 4-6s 慢起伏 */
@keyframes cs-breathe {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.15); }
}
@keyframes cs-breathe-dim {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.1); }
}
.cs-node-label { font-size: 10px; fill: var(--td-text-color-secondary, #666); paint-order: stroke; stroke: var(--td-bg-color-container, #fff); stroke-width: 3px; stroke-linejoin: round; }
/* 图例落在圆图左下天然空角，不遮节点 */
.cs-caption { position: absolute; left: 4px; bottom: 2px; font-size: 11px; color: var(--td-text-color-placeholder, #999); max-width: 46%; text-align: left; pointer-events: none; }
@keyframes cs-node-in {
  from { opacity: 0; transform: scale(0.6); }
  to { opacity: 1; transform: none; }
}
@keyframes cs-twinkle {
  /* 亮度闪光：只动辉光（亮度通道独占），与呼吸的缩放通道正交，
     "近48h学过"是用户最关心的即时反馈，节奏与 ping 环同为 1.6s 同步脉冲 */
  0%, 100% { filter: none; }
  50% { filter: drop-shadow(0 0 6px rgba(7, 192, 95, 0.9)); }
}
@media (prefers-reduced-motion: reduce) {
  .cs-node, .cs-node-disc, .cs-ring-guide, .cs-hub-halo, .cs-spoke, .cs-bg, .cs-ping { animation: none; }
}
</style>
