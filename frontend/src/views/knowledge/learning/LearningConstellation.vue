<template>
  <div class="constellation-wrap">
    <svg :viewBox="`0 0 ${SIZE} ${SIZE}`" class="constellation-svg" :class="{ grabbing: dragging }" role="application"
      :aria-label="$t('knowledgeEditor.learningTab.constellationTitle')"
      @wheel.prevent="onWheel" @selectstart.prevent
      @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="onPointerUp" @pointercancel="onPointerUp"
      @dblclick.prevent="resetView">
      <defs>
        <radialGradient id="cs-bg" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stop-color="rgba(7, 192, 95, 0.07)" />
          <stop offset="70%" stop-color="rgba(7, 192, 95, 0.02)" />
          <stop offset="100%" stop-color="transparent" />
        </radialGradient>
      </defs>

      <circle class="cs-bg" :cx="SIZE / 2" :cy="SIZE / 2" :r="SIZE / 2" fill="url(#cs-bg)" />

      <!-- 扇区分隔线：分区结构一眼可辨，淡到只做引导（随视角旋转的赤道径向线） -->
      <line v-for="(l, i) in separatorViews" :key="`sep-${i}`" class="cs-separator"
        :x1="l.x1" :y1="l.y1" :x2="l.x2" :y2="l.y2" />

      <!-- 掌握度环：内环=已验证，外环=未接触（投影后的椭圆轨道） -->
      <polygon v-for="band in bands" :key="band.tier" class="cs-ring-guide"
        :points="band.ring" :stroke="tierColor(band.tier)" />

      <!-- 关系线：先修实线、wiki细线、跨区虚线。悬停时相关线亮绿、其余半隐；星尘折起时其连线不画 -->
      <line v-for="e in edgeViews" :key="`e-${e.key}`" class="cs-edge"
        :x1="e.x1" :y1="e.y1" :x2="e.x2" :y2="e.y2"
        :class="{ cross: e.cross && !e.lit, lit: e.lit, wikilink: e.wikilink && !e.lit, dim: e.dim }"
        :stroke-width="e.lit ? 2 : e.wikilink ? 0.7 : 1"
        :opacity="e.dim ? 0.10 : e.lit ? 0.95 : undefined" />

      <!-- 中心枢纽：点亮/总数（投影原点，始终在画布中心） -->
      <g class="cs-hub">
        <circle :cx="SIZE / 2" :cy="SIZE / 2" r="40" class="cs-hub-disc" />
        <text :x="SIZE / 2" :y="SIZE / 2 - 4" text-anchor="middle" class="cs-hub-num">{{ verification && nodes.some(n => n.verification_label === '验证状态暂不可用') ? '—' : `${litCount}/${totalCount}` }}</text>
        <text :x="SIZE / 2" :y="SIZE / 2 + 14" text-anchor="middle" class="cs-hub-sub">{{ verification ? '已接触的知识点' : $t('knowledgeEditor.learningTab.constellationLit') }}</text>
      </g>

      <!-- 星尘：折起的未接触微点云（无标签、不挡交互） -->
      <circle v-for="(d, i) in dustDotViews" :key="`dust-${i}`" class="cs-dust-dot"
        :cx="d.x" :cy="d.y" :r="d.r" :opacity="d.dim ? 0.15 : 0.55" />

      <!-- 知识节点：分区扇区 + 掌握度壳层，按深度排序（远者先画）。悬停时非关联半隐（图谱页语义）。
           透明命中圆把可点区域放大到 disc+7px；标签按优先级避让裁剪（cullLabels）。 -->
      <g v-for="p in nodeViews" :key="p.slug" class="cs-node"
        :class="{ recent: p.recent, star: p.isNext, dim: p.dim }"
        tabindex="0" role="button" :aria-label="p.tooltip"
        @click.stop="onNodeClick(p.slug)"
        @keydown.enter.prevent="emit('open', p.slug)"
        @pointerenter="hovered = p.slug" @pointerleave="hovered = null">
        <circle class="cs-node-hit" :cx="p.x" :cy="p.y" :r="p.r + 7" />
        <circle v-if="p.recent" class="cs-ping" :cx="p.x" :cy="p.y" :r="p.r + 3" />
        <circle v-if="p.isNext" class="cs-next-ring" :cx="p.x" :cy="p.y" :r="p.r + 6" />
        <circle :cx="p.x" :cy="p.y" :r="p.r" class="cs-node-disc"
          :fill="p.fill" :stroke-dasharray="p.lowConf ? '2 3' : 'none'"
          :opacity="p.opacity" />
        <text v-if="p.showLabel" :x="p.x" :y="p.y + p.r + 13" text-anchor="middle" class="cs-node-label"
          :font-size="10" :opacity="p.opacity">{{ p.label }}</text>
        <title>{{ p.tooltip }}</title>
      </g>

      <!-- 星尘徽章：+N 展开/收起该区未接触节点 -->
      <g v-for="d in dustChipViews" :key="`chip-${d.zone}`" class="cs-dust-chip"
        :class="{ dim: d.dim }" tabindex="0" role="button" :aria-label="d.tooltip"
        @click.stop="toggleDust(d.zone)" @keydown.enter.prevent="toggleDust(d.zone)">
        <circle :cx="d.x" :cy="d.y" r="10" />
        <text :x="d.x" :y="d.y + 3.5" text-anchor="middle">+{{ d.count }}</text>
        <title>{{ d.tooltip }}</title>
      </g>

      <!-- 分区名外圈：每区 1 个标签而非每节点 1 个——分布结构先读 -->
      <text v-for="z in zoneLabelViews" :key="`zl-${z.id}`" class="cs-zone-rim"
        :x="z.x" :y="z.y" text-anchor="middle" :opacity="z.dim ? 0.3 : 0.78">{{ z.name }}</text>
    </svg>
    <div class="cs-caption" :class="{'verification-caption':verification}">
      <div v-if="verification" class="verification-legend"><span><i style="background:#c9ced5"></i>未开始</span><span><i style="background:#5c9dce"></i>学习中</span><span><i style="background:#64b991"></i>自认已会</span><span><i style="background:#148452"></i>验证通过</span><span><i style="background:#d59b35"></i>待巩固</span></div>
      <div>{{ verification ? '点击阅读 · 自认已会可移出待学 · 验证通过单独标记' : $t('knowledgeEditor.learningTab.constellationHint3d') }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  layoutConstellation3, nodeLabel, truncateLabel, partitionDust, labelPriority, cullLabels, labelUnits,
  projectPoint, ringPoints, DEFAULT_VIEW,
  type ConstellationEdge, type ConstellationNode, type Tier, type Vec3, type ZoneAxis,
} from './constellationLayout'

const props = defineProps<{
  nodes: ConstellationNode[]
  edges: ConstellationEdge[]
  zones: ZoneAxis[]
  highlightZone?: string | null
  verification?: boolean
}>()
const emit = defineEmits<{ (e: 'open', slug: string): void }>()
const { t } = useI18n()

const SIZE = 560
// 3D 轨道视角：拖拽旋转（yaw/pitch），滚轮缩放，双击复位。分区扇区按方位角排列，
// 掌握度按同心壳层，投影见 constellationLayout.projectPoint。

const TIER_COLORS: Record<Tier, string> = {
  unseen: '#d0d0d0',
  touched: '#8ce0af',
  familiar: '#07c05f',
  mastered: '#038626',
}
const FADED_COLOR = '#7b8ba1'
const SKIPPED_COLOR = '#a9b7c6'
/** 放大到该档位自动展开全部星尘（放大=要看细节） */
const DUST_AUTO_EXPAND_ZOOM = 1.4
/** 初始/复位视角：轻微俯角+偏航，一眼读出这是颗可旋转的球（与布局
 *  松弛的度量视角同源，见 constellationLayout.DEFAULT_VIEW） */
const INITIAL_YAW = DEFAULT_VIEW.yaw
const INITIAL_PITCH = DEFAULT_VIEW.pitch
const YAW_PER_PX = 0.0062
const PITCH_PER_PX = 0.0048
const PITCH_LIMIT = 1.15
/** 拖拽在 4px 起转（旋转手感），但只有 ≥8px 才吞掉点击——触控板/鼠标
 * 点按常见的 4-8px 微漂移仍应打开节点页，而不是被当成拖拽。 */
const CLICK_SUPPRESS_PX = 8

function tierColor(tier: Tier): string {
  return TIER_COLORS[tier] || TIER_COLORS.unseen
}

const zoom = ref(1)
const yaw = ref(INITIAL_YAW)
const pitch = ref(INITIAL_PITCH)
const hovered = ref<string | null>(null)
const dragging = ref(false)
const expandedDust = ref<Set<string>>(new Set())

function onWheel(e: WheelEvent) {
  const dy = e.deltaY * (e.deltaMode === 1 ? 33 : 1)
  zoom.value = Math.max(0.6, Math.min(1.8, zoom.value * Math.exp(-dy * 0.0012)))
}

function resetView() {
  zoom.value = 1
  yaw.value = INITIAL_YAW
  pitch.value = INITIAL_PITCH
}

// ---- 拖拽旋转：水平拖=yaw、垂直拖=pitch（俯仰钳位），>4px 判拖拽并抑制节点 click ----
let dragStart: { x: number; y: number; yaw: number; pitch: number; id: number } | null = null
let suppressClick = false

function onPointerDown(e: PointerEvent) {
  if (e.button !== 0) return
  dragStart = { x: e.clientX, y: e.clientY, yaw: yaw.value, pitch: pitch.value, id: e.pointerId }
}

function onPointerMove(e: PointerEvent) {
  if (!dragStart || e.pointerId !== dragStart.id) return
  const dx = e.clientX - dragStart.x
  const dy = e.clientY - dragStart.y
  const moved = Math.hypot(dx, dy)
  if (!dragging.value && moved > 4) {
    dragging.value = true
    // 只有确认为拖拽后才捕获指针：pointerdown 即捕获会把后续 pointerup/click
    // 全部重定向到 svg 根，节点 <g> 的 @click 永远收不到（节点点不开的根因）。
    // 捕获失败（如已释放的指针）不影响旋转本身。
    try { (e.currentTarget as SVGSVGElement).setPointerCapture?.(e.pointerId) } catch { /* noop */ }
  }
  if (dragging.value && !suppressClick && moved >= CLICK_SUPPRESS_PX) suppressClick = true
  if (!dragging.value) return
  yaw.value = dragStart.yaw + dx * YAW_PER_PX
  pitch.value = Math.max(-PITCH_LIMIT, Math.min(PITCH_LIMIT, dragStart.pitch + dy * PITCH_PER_PX))
}

function onPointerUp(e: PointerEvent) {
  if (!dragStart || e.pointerId !== dragStart.id) return
  dragStart = null
  if (dragging.value) {
    dragging.value = false
    window.setTimeout(() => { suppressClick = false }, 0)
  }
}

function onNodeClick(slug: string) {
  if (suppressClick) return
  emit('open', slug)
}

let nowTimer = 0
const nowTick = ref(Date.now())
onMounted(() => { nowTimer = window.setInterval(() => { nowTick.value = Date.now() }, 60_000) })
onUnmounted(() => clearInterval(nowTimer))
// KeepAlive 失活（切去 wiki/图谱页）时清交互残留：悬停高亮与进行中的拖拽
// 若不清，回来时会"卡"在离开前的关联减淡/旋转中态（用户实测问题）。
onDeactivated(() => {
  hovered.value = null
  dragging.value = false
  dragStart = null
})
onActivated(() => {
  // 复激时视角保持用户离开前的角度——只清指针态，不重置 yaw/pitch/zoom。
})

// ---- 布局：确定性纯函数（分区扇区 + 掌握度环），见 constellationLayout.ts ----
const layout = computed(() =>
  layoutConstellation3(props.nodes, props.zones, new Date(nowTick.value)))

const bands = computed(() =>
  layout.value.bands.map((b) => ({ tier: b.tier, ring: ringPoints(b.radius, yaw.value, pitch.value, SIZE, zoom.value) })))

const nextSlugs = computed(() => {
  const set = new Set<string>()
  for (const z of props.zones) if (z.next) set.add(z.next)
  return set
})

// ---- 星尘折叠：纯函数分区 + 交互态（点开 / 高亮区 / 放大）决定最终折起集 ----
const dustPartition = computed(() => partitionDust(props.nodes, nextSlugs.value, new Date(nowTick.value)))

const collapsedDust = computed(() => {
  const m = new Map<string, ConstellationNode[]>()
  for (const [zone, arr] of dustPartition.value.dust) {
    const expanded = expandedDust.value.has(zone) || zoom.value >= DUST_AUTO_EXPAND_ZOOM || props.highlightZone === zone
    if (!expanded) m.set(zone, arr)
  }
  return m
})

const collapsedSlugs = computed(() => {
  const s = new Set<string>()
  for (const arr of collapsedDust.value.values()) for (const n of arr) s.add(n.slug)
  return s
})

function toggleDust(zone: string) {
  const s = new Set(expandedDust.value)
  if (s.has(zone)) s.delete(zone)
  else s.add(zone)
  expandedDust.value = s
}

/** 悬停关联集（图谱页语义）：该节点 + 有边相连的实体 + 同区成员。 */
const adjacency = computed(() => {
  if (!hovered.value) return null
  const set = new Set<string>([hovered.value])
  for (const e of props.edges) {
    if (e.from === hovered.value) set.add(e.to)
    if (e.to === hovered.value) set.add(e.from)
  }
  const hoveredNode = props.nodes.find((n) => n.slug === hovered.value)
  const hoveredZone = hoveredNode?.folder_id ?? ''
  if (hoveredZone) {
    for (const n of props.nodes) {
      if ((n.folder_id ?? '') === hoveredZone) set.add(n.slug)
    }
  }
  return set
})

const zoneOf = computed(() => {
  const m = new Map<string, string>()
  for (const p of layout.value.placed) m.set(p.node.slug, p.node.folder_id ?? '')
  return m
})

/** 3D→2D 投影：轨道相机（yaw/pitch 由拖拽驱动）+ 透视 + 缩放。 */
function proj(p: Vec3) {
  return projectPoint(p, yaw.value, pitch.value, SIZE, zoom.value)
}

/** 深度→透明度：远侧节点略淡，近侧实——不用遮挡剔除也能读出球面层次。 */
function depthOpacity(depth: number, dim: boolean): number {
  if (dim) return 0.22
  const k = Math.max(0, Math.min(1, (depth + 260) / 520)) // 0=近 … 1=远
  return 0.95 - 0.33 * k
}

interface NodeView {
  slug: string; x: number; y: number; r: number
  fill: string; tier: Tier; label: string
  opacity: number; lowConf: boolean; recent: boolean
  skipped: boolean; isNext: boolean; tooltip: string; dim: boolean; showLabel: boolean
  faded: boolean; evidence: number; selfAssess: boolean
}

const nodeViews = computed<NodeView[]>(() => {
  const views = layout.value.placed
    .filter((p) => !collapsedSlugs.value.has(p.node.slug))
    .map((p) => {
      const node = p.node
      const isNext = nextSlugs.value.has(node.slug)
      const inZone = props.highlightZone == null || (node.folder_id ?? '') === props.highlightZone
      const isAdjacent = adjacency.value?.has(node.slug) ?? false
      let dim = !inZone
      if (!dim && adjacency.value) dim = !isAdjacent
      const label = truncateLabel(nodeLabel(node.title, node.slug), 9)
      const lines = props.verification ? [nodeLabel(node.title, node.slug), node.verification_label || '验证状态暂不可用'] : [
        nodeLabel(node.title, node.slug),
        t('knowledgeEditor.learningTab.constellationHoverP', { p: Math.round((node.p_eff ?? 0) * 100) }),
        t('knowledgeEditor.learningTab.constellationHoverN', { n: node.evidence_count ?? 0 }),
      ]
      if (p.faded && !props.verification) lines.push(t('knowledgeEditor.learningTab.tierFaded'))
      if (node.skipped) lines.push(t('knowledgeEditor.learningTab.skippedBadge'))
      const place = node.section || node.doc_title
      if (place) lines.push(place)
      if (node.self_assess) lines.push(selfAssessLine(node.self_assess))
      if (isNext) lines.push(t('knowledgeEditor.learningTab.zoneNextMark'))
      const pr = proj(p.pos)
      return {
        slug: node.slug,
        x: pr.x, y: pr.y,
        depth: pr.depth,
        r: p.r * pr.scale * zoom.value,
        fill: props.verification ? (node.verification_color || '#a7afb8') : node.skipped ? SKIPPED_COLOR : p.faded ? FADED_COLOR : tierColor(p.tier),
        tier: p.tier,
        faded: p.faded,
        recent: p.recent,
        skipped: !!node.skipped,
        isNext,
        evidence: node.evidence_count ?? 0,
        selfAssess: !!node.self_assess,
        label,
        opacity: depthOpacity(pr.depth, dim),
        lowConf: !!node.low_confidence,
        tooltip: lines.join('\n'),
        dim,
        showLabel: false,
      }
    })
    .sort((a, b) => a.depth - b.depth) // 远者先画，近者覆盖在上
  // 标签预算：优先级排序 + 贪心矩形避让（含中心枢纽与外圈分区名障碍），悬停节点钉住必显。
  const pinned = hovered.value
  const rimObstacles = zoneLabelViews.value.map((z) => {
    const w = labelUnits(z.name) * 10
    return { x0: z.x - w / 2 - 2, x1: z.x + w / 2 + 2, y0: z.y - 9, y1: z.y + 3 }
  })
  const keep = cullLabels(
    views.map((v) => ({
      slug: v.slug,
      x: v.x,
      y: v.y + v.r + 13,
      widthPx: labelUnits(v.label) * 10,
      priority: labelPriority({
        tier: v.tier, faded: v.faded, recent: v.recent, isNext: v.isNext,
        selfAssess: v.selfAssess, skipped: v.skipped, evidence: v.evidence,
      }),
      pinned: v.slug === pinned,
    })),
    { x: SIZE / 2, y: SIZE / 2, r: 36 },
    rimObstacles,
  )
  for (const v of views) v.showLabel = keep.has(v.slug) || v.slug === pinned
  return views
})

interface EdgeView { key: string; x1: number; y1: number; x2: number; y2: number; cross: boolean; lit: boolean; wikilink: boolean; dim: boolean }
const edgeViews = computed<EdgeView[]>(() => {
  const out: EdgeView[] = []
  for (const e of props.edges) {
    if (collapsedSlugs.value.has(e.from) || collapsedSlugs.value.has(e.to)) continue
    const a = layout.value.placed.find((p) => p.node.slug === e.from)
    const b = layout.value.placed.find((p) => p.node.slug === e.to)
    if (!a || !b) continue
    const za = zoneOf.value.get(e.from) ?? ''
    const zb = zoneOf.value.get(e.to) ?? ''
    const cross = za !== zb
    const zoneLit = props.highlightZone != null && (za === props.highlightZone || zb === props.highlightZone)
    const hoverLit = !!hovered.value && (e.from === hovered.value || e.to === hovered.value)
    const dim = !!hovered.value && !hoverLit
    const pa = proj(a.pos)
    const pb = proj(b.pos)
    out.push({
      key: `${e.from}->${e.to}`, x1: pa.x, y1: pa.y, x2: pb.x, y2: pb.y,
      cross, lit: hoverLit || zoneLit, wikilink: e.kind === 'wikilink', dim,
    })
  }
  return out
})

// ---- 星尘渲染：微点云（slug 哈希确定性微扰）+ 质心 +N 徽章 ----
function slugHash(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

const dustDotViews = computed(() => {
  const out: { x: number; y: number; r: number; dim: boolean }[] = []
  for (const [zone, arr] of collapsedDust.value) {
    const dim = props.highlightZone != null && props.highlightZone !== zone
    for (const n of arr) {
      const p = layout.value.placed.find((q) => q.node.slug === n.slug)
      if (!p) continue
      const pr = proj(p.pos)
      const h = slugHash(n.slug)
      out.push({
        x: pr.x + (((h & 15) - 7.5) * 0.8),
        y: pr.y + ((((h >>> 4) & 15) - 7.5) * 0.8),
        r: (1.6 + (h % 5) * 0.2) * pr.scale * zoom.value,
        dim,
      })
    }
  }
  return out
})

const dustChipViews = computed(() => {
  const out: { zone: string; x: number; y: number; count: number; tooltip: string; dim: boolean }[] = []
  for (const [zone, arr] of collapsedDust.value) {
    let cx = 0
    let cy = 0
    let n = 0
    for (const node of arr) {
      const p = layout.value.placed.find((q) => q.node.slug === node.slug)
      if (!p) continue
      const pr = proj(p.pos)
      cx += pr.x
      cy += pr.y
      n++
    }
    if (!n) continue
    const names = arr.map((node) => nodeLabel(node.title, node.slug)).join('、')
    out.push({
      zone,
      x: cx / n,
      y: cy / n - 18,
      count: arr.length,
      tooltip: t('knowledgeEditor.learningTab.constellationDustHint', { n: arr.length }) + '\n' + names,
      dim: props.highlightZone != null && props.highlightZone !== zone,
    })
  }
  return out
})

// ---- 分区结构：分隔线 + 外圈分区名（每区 1 个标签——分布先读，细节后读） ----
const separatorViews = computed(() => {
  const sectors = layout.value.sectors
  if (sectors.size < 2) return []
  const out: { x1: number; y1: number; x2: number; y2: number }[] = []
  for (const [start] of sectors.values()) {
    const cos = Math.cos(start)
    const sin = Math.sin(start)
    const a = proj({ x: cos * 46, y: 0, z: sin * 46 })
    const b = proj({ x: cos * 242, y: 0, z: sin * 242 })
    out.push({ x1: a.x, y1: a.y, x2: b.x, y2: b.y })
  }
  return out
})

const zoneLabelViews = computed(() => {
  const sectors = layout.value.sectors
  const out: { id: string; name: string; x: number; y: number; dim: boolean }[] = []
  const mids = props.zones
    .filter((z) => sectors.has(z.id))
    .map((z) => {
      const [s, e] = sectors.get(z.id)!
      return { z, mid: (s + e) / 2 }
    })
  const R1 = 258
  const R2 = 270
  let bumped = false
  mids.forEach((m, i) => {
    // 相邻分区名角距过近时错开到第二半径（确定性单遍）
    const prev = mids[(i - 1 + mids.length) % mids.length]
    const gap = Math.abs(m.mid - prev.mid)
    if (mids.length > 1 && Math.min(gap, Math.PI * 2 - gap) < 0.42) bumped = !bumped
    else bumped = false
    const r = bumped ? R2 : R1
    const pr = proj({ x: Math.cos(m.mid) * r, y: 0, z: Math.sin(m.mid) * r })
    out.push({
      id: m.z.id,
      name: truncateLabel(m.z.name || m.z.id, 8),
      x: pr.x,
      y: pr.y,
      dim: props.highlightZone != null && props.highlightZone !== m.z.id,
    })
  })
  return out
})

const litCount = computed(() =>
  layout.value.placed.filter((p) => props.verification ? p.node.learning_contacted ?? p.node.verification_complete : p.tier !== 'unseen' && !p.faded && !p.node.skipped).length)
const totalCount = computed(() => layout.value.placed.length)

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
</script>

<style scoped>
.constellation-wrap { position: relative; height: 100%; width: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.constellation-svg {
  width: 100%; height: 100%; max-width: none; display: block;
  cursor: grab; touch-action: none;
  -webkit-user-select: none; user-select: none; -webkit-touch-callout: none;
}
.constellation-svg.grabbing { cursor: grabbing; }
.cs-bg { animation: cs-bg-breathe 11s ease-in-out infinite; }
@keyframes cs-bg-breathe { 0%, 100% { opacity: 0.72; } 50% { opacity: 1; } }
.cs-separator { stroke: rgba(125, 145, 165, 0.12); stroke-width: 1; pointer-events: none; }
.cs-ring-guide { fill: none; stroke-width: 1; stroke-dasharray: 3 6; opacity: 0.25; pointer-events: none; }
.cs-edge { stroke: rgba(125, 145, 165, 0.30); }
.cs-edge.cross { stroke-dasharray: 5 5; }
.cs-edge.wikilink { opacity: 0.45; }
.cs-edge.lit { stroke: var(--td-brand-color, #07c05f); opacity: 0.95; }
.cs-hub-disc { fill: var(--td-bg-color-container, #fff); stroke: var(--td-brand-color, #07c05f); stroke-width: 2; }
.cs-hub-num { font-size: 18px; font-weight: 600; fill: var(--td-text-color-primary, #333); }
.cs-hub-sub { font-size: 9px; fill: var(--td-text-color-placeholder, #999); }
.cs-dust-dot { fill: #c9cdd4; pointer-events: none; }
.cs-dust-chip { cursor: pointer; }
.cs-dust-chip circle { fill: var(--td-bg-color-container, #fff); stroke: var(--td-component-stroke, #dcdcdc); stroke-width: 1; }
.cs-dust-chip text { font-size: 9px; font-weight: 600; fill: var(--td-text-color-secondary, #555); }
.cs-dust-chip:hover circle { stroke: var(--td-brand-color, #07c05f); }
.cs-dust-chip:focus { outline: none; }
.cs-dust-chip:focus-visible circle { stroke: var(--td-brand-color, #07c05f); stroke-width: 2; }
.cs-dust-chip.dim { opacity: 0.45; }
.cs-zone-rim {
  font-size: 10px; fill: var(--td-text-color-placeholder, #999); font-weight: 500;
  paint-order: stroke; stroke: var(--td-bg-color-container, #fff); stroke-width: 3px; stroke-linejoin: round;
  pointer-events: none; -webkit-user-select: none; user-select: none;
}
.cs-node { cursor: pointer; }
.cs-node:focus { outline: none; }
.cs-node:focus-visible .cs-node-disc { stroke: var(--td-brand-color, #07c05f); stroke-width: 2.5; }
.cs-node-hit { fill: transparent; pointer-events: all; }
.cs-node-disc { stroke: rgba(0, 0, 0, 0.12); stroke-width: 0.5; }
.cs-node:hover .cs-node-disc { stroke: var(--td-brand-color, #07c05f); stroke-width: 2; }
.cs-node-label {
  font-size: 10px; fill: var(--td-text-color-secondary, #555); font-weight: 500;
  paint-order: stroke; stroke: var(--td-bg-color-container, #fff); stroke-width: 3px; stroke-linejoin: round;
  pointer-events: none;
  -webkit-user-select: none; user-select: none;
}
.cs-node.recent .cs-node-label { fill: #049b38; }
.cs-next-ring { fill: none; stroke: #049b38; stroke-width: 1.1; transform-box: fill-box; transform-origin: center; pointer-events: none; animation: cs-next 3.2s ease-in-out infinite; }
@keyframes cs-next { 0%, 100% { transform: scale(0.92); opacity: 0.42; } 50% { transform: scale(1.14); opacity: 0.16; } }
.cs-ping { fill: none; stroke: var(--td-brand-color, #07c05f); stroke-width: 0.8; transform-box: fill-box; transform-origin: center; pointer-events: none; animation: cs-ping 3.6s ease-in-out infinite; }
/* 近学光环：低透明度缓呼吸（曾经的强脉冲扩散在多实体时令人眼花——
   刻意压到"有呼吸感即可"，配合球体整体不显死寂） */
@keyframes cs-ping { 0%, 100% { transform: scale(0.92); opacity: 0.30; } 50% { transform: scale(1.16); opacity: 0.10; } }
.cs-caption { position: absolute; left: 4px; bottom: 2px; font-size: 11px; color: var(--td-text-color-placeholder, #999); max-width: 46%; text-align: left; pointer-events: none; }
.cs-caption.verification-caption{max-width:95%;line-height:1.7;color:var(--td-text-color-secondary,#666)}.verification-legend{display:flex;flex-wrap:wrap;gap:4px 12px;margin-bottom:3px}.verification-legend span{display:inline-flex;align-items:center;gap:4px}.verification-legend i{width:7px;height:7px;border-radius:50%;display:inline-block;flex-shrink:0}
@media (prefers-reduced-motion: reduce) {
  .cs-bg, .cs-ping, .cs-next-ring { animation: none; }
}
</style>
