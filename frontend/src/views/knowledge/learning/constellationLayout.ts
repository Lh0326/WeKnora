/**
 * Pure deterministic 3D layout for the learning tab's knowledge
 * constellation (知识星图). Positions are SEMANTIC, not physical:
 *
 *   - mastery tier   → sphere radius (mastered inner, unseen rim) —
 *                      "how deep into the zone my knowledge reached";
 *   - module zone    → azimuth sector (one wiki folder per wedge), with
 *                      the sector width PROPORTIONAL to √(member count)
 *                      so a 15-node module is never squeezed into the
 *                      same wedge as a 2-node one;
 *   - material order → position along the zone's arc (doc_rank), so a
 *                      zone reads front-to-back like its source material;
 *   - ring capacity  → a tier's nodes fill the arc at a spacing the eye
 *                      can read (LABEL_ARC_PX per label) and overflow to
 *                      a staggered sub-ring — the anti-collision maths
 *                      that keeps dense zones legible.
 *
 * This mirrors what 3d-force-graph achieves with physics + WebGL, minus
 * both: our coordinates carry meaning a force simulation would destroy,
 * and at enterprise-KB node counts (tens..hundreds) an SVG painter with a
 * hand-rolled perspective projection is smaller, themeable, and testable.
 */

export type Tier = 'unseen' | 'touched' | 'familiar' | 'mastered'

export interface ConstellationNode {
  slug: string
  title?: string
  level: string
  p_eff?: number
  evidence_count?: number
  low_confidence?: boolean
  last_evidence_at?: string
  /** 最新原始事件时间（含零权重触达）；缺省回退 last_evidence_at */
  last_activity_at?: string
  self_assess?: { direction: string; event_type: string; occurred_at: string }
  /** 用户声明「已掌握，不再推荐」：节点从推荐/遗忘队列移出，图上以灰蓝标记 */
  skipped?: boolean
  /** 模块知识区 = wiki 目录（folder_id），后端 zone-map 提供 */
  folder_id?: string
  folder_name?: string
  section?: string
  doc_title?: string
  doc_rank?: number
}

export interface ConstellationEdge {
  from: string
  to: string
  kind?: string
}

export interface ZoneAxis {
  id: string
  name: string
  /** 该区当前的"1"（zone next 的 slug），图上以脉冲环+①标记 */
  next?: string | null
}

export interface Vec3 {
  x: number
  y: number
  z: number
}

export interface PlacedNode3 {
  node: ConstellationNode
  tier: Tier
  pos: Vec3
  /** node circle radius in px (evidence volume, saturating) */
  r: number
  /** unseen tier but WITH history — rendered grey-blue, the 已淡化 tier */
  faded: boolean
  /** activity within RECENT_MS of `now` — rendered twinkling */
  recent: boolean
}

export interface Layout3 {
  placed: PlacedNode3[]
  /** the four tier shells, inner first, with their radii for ring guides */
  bands: { tier: Tier; radius: number }[]
  /** azimuth sector per zone id (radians: [start, end)) — hover targets */
  sectors: Map<string, [number, number]>
}

export interface Projected {
  x: number
  y: number
  /** perspective size factor: 1 at the focal plane, <1 farther, >1 nearer */
  scale: number
  /** camera-space depth: larger = farther (used for paint order + opacity) */
  depth: number
}

const TIER_RADII: Record<Tier, number> = { mastered: 58, familiar: 112, touched: 166, unseen: 220 }
/** Per-tier radial band for stagger rings — MUTUALLY DISJOINT with a ≥26px
 *  gap between neighbouring bands, so any cross-tier pair is ≥26px apart
 *  by radius alone. Each band is exactly one ARC_STAGGER wide; overflow
 *  past the edge becomes the absorbing ring (radius insetting when the
 *  clamp would land on the previous ring). Unseen stacks OUTWARD (it owns
 *  the rim, up to 248). */
const TIER_BAND: Record<Tier, [number, number]> = {
  mastered: [58, 86],
  familiar: [112, 140],
  touched: [166, 194],
  unseen: [220, 248],
}
/** post-layout relaxation: minimum PROJECTED distance between two nodes
 * under the default orbit view. 3D separation alone is not enough on a
 * flat disc — at the default 0.34 rad pitch a pair separated along the
 * line of sight compresses to ~0.34× on screen. The metric is the default
 * view's projection (nodes never move when the user rotates: positions
 * stay view-stable); pushes remain azimuth-preserving radial nudges. */
const MIN_SEPARATION = 16
const RELAX_PASSES = 10
/** the relaxation metric's camera — matches the component's initial view */
export const DEFAULT_VIEW = { yaw: -0.28, pitch: 0.34 }
/** fraction of each sector left as a gap so wedges stay visually distinct */
const SECTOR_GAP = 0.12
/** max elevation of a zone arc off the equator, radians — deliberately
 *  shallow (±5.7°): a near-flat disc eliminates sphere occlusion while
 *  retaining a hint of 3D when the orbit camera pitches. */
const ARC_TILT = 0.10
const RECENT_MS = 48 * 60 * 60 * 1000
/** px between stagger sub-rings: a full ring's worth of nodes at the
 *  tier radius plus this offset never shares label rows with it. */
const ARC_STAGGER = 28
/** consecutive stagger rings rotate their slot grid by this fraction of
 *  the arc, so ring N's first slot never aligns azimuthally with ring
 *  N±1's — cross-ring pile-ups get both radial AND angular separation. */
const RING_AZ_SPIN = 0.37
/** guaranteed arc length per label on one ring (px) — the capacity model's
 *  readability budget. Exported for the no-overlap property test. */
export const LABEL_ARC_PX = 56
/** stagger rings stay on the 560 canvas (half side minus a margin) */
const RADIUS_MAX = 262
const RADIUS_MIN = 30
/** weight floor (√-units): an empty zone keeps a visible sliver of arc */
const ZONE_WEIGHT_FLOOR = 0.35

function tierOf(level: string): Tier {
  return level === 'mastered' || level === 'familiar' || level === 'touched' ? level : 'unseen'
}

/** Evidence-volume radius, saturating: 7px at one event, 15px at a dozen. */
function nodeRadius(evidence: number): number {
  return 7 + 8 * Math.min(1, Math.max(0, evidence - 1) / 11)
}

/** How many labels a ring of the given radius holds inside the zone's
 *  usable arc while keeping LABEL_ARC_PX of arc per label. */
function ringCapacity(usableSpan: number, radius: number): number {
  if (radius <= 0) return 1
  return Math.max(1, Math.floor((usableSpan * Math.abs(radius)) / LABEL_ARC_PX))
}

/**
 * Deterministic 3D placement. Zone sectors are weighted by √(members) so
 * dense modules earn wider wedges (the backend already orders zones
 * best-next-first); nodes inside a zone order by doc_rank (material
 * order), then title/slug so the layout never depends on input array
 * order. A tier's members fill ring after ring at readable spacing —
 * unseen stacks inward (it owns the rim), other tiers outward.
 */
export function layoutConstellation3(nodes: ConstellationNode[], zones: ZoneAxis[], now: Date): Layout3 {
  // Zone universe: the given axes first, then any folder seen on nodes but
  // missing from the axes (defensive), then root.
  const zoneIds: string[] = []
  const seen = new Set<string>()
  for (const z of zones) {
    if (!seen.has(z.id)) {
      seen.add(z.id)
      zoneIds.push(z.id)
    }
  }
  for (const n of nodes) {
    const id = n.folder_id ?? ''
    if (!seen.has(id)) {
      seen.add(id)
      zoneIds.push(id)
    }
  }

  // Per-zone ordering by material position (doc_rank asc, unranked last).
  const byZone = new Map<string, ConstellationNode[]>()
  for (const n of nodes) {
    const id = n.folder_id ?? ''
    const arr = byZone.get(id) ?? []
    arr.push(n)
    byZone.set(id, arr)
  }
  for (const arr of byZone.values()) {
    arr.sort((a, b) => {
      const ra = a.doc_rank ?? Number.MAX_SAFE_INTEGER
      const rb = b.doc_rank ?? Number.MAX_SAFE_INTEGER
      if (ra !== rb) return ra - rb
      return (a.title ?? a.slug).localeCompare(b.title ?? b.slug) || a.slug.localeCompare(b.slug)
    })
  }

  // Sector widths ∝ √(member count): parallel modules share the disc by
  // demand, not by equal slices — the core fix for crowded wedges.
  const weights = zoneIds.map((id) => Math.max(ZONE_WEIGHT_FLOOR, Math.sqrt(byZone.get(id)?.length ?? 0)))
  const wsum = weights.reduce((a, b) => a + b, 0) || 1
  const sectors = new Map<string, [number, number]>()
  let acc = 0
  zoneIds.forEach((id, i) => {
    const span = (Math.PI * 2 * weights[i]) / wsum
    sectors.set(id, [acc, acc + span])
    acc += span
  })

  const placed: PlacedNode3[] = []
  for (const id of zoneIds) {
    const members = byZone.get(id) ?? []
    const [start, end] = sectors.get(id)!
    const span = end - start
    const inner = start + (span * SECTOR_GAP) / 2
    const usable = span * (1 - SECTOR_GAP)
    const single = members.length === 1
    // per-tier ring cursor: members fill ring 0 to capacity, then ring 1…;
    // `absorbCap`/`absorbR` freeze the terminal ring's grid size AND radius
    // at open time — recomputing either per node would shift the slot grid
    // under placed feet and collide earlier members
    const cursor = new Map<Tier, { ring: number; used: number; absorb: boolean; absorbCap: number; absorbR: number }>()
    const tierTotal = new Map<Tier, number>()
    for (const m of members) {
      const t = tierOf(m.level)
      tierTotal.set(t, (tierTotal.get(t) ?? 0) + 1)
    }
    const tierPlaced = new Map<Tier, number>()
    for (const node of members) {
      const tier = tierOf(node.level)
      let st = cursor.get(tier)
      if (!st) {
        st = { ring: 0, used: 0, absorb: false, absorbCap: 0, absorbR: 0 }
        cursor.set(tier, st)
      }
      // all tiers stack outward inside their own disjoint band; unseen
      // simply owns the outermost band (220→248)
      const remaining = tierTotal.get(tier)! - (tierPlaced.get(tier) ?? 0)
      const band = TIER_BAND[tier]
      const ringAt = (ring: number) => {
        const raw = TIER_RADII[tier] + ring * ARC_STAGGER
        return Math.min(band[1], Math.max(band[0], raw))
      }
      let ringR = st.absorb ? st.absorbR : ringAt(st.ring)
      let cap = st.absorb ? Math.max(1, st.absorbCap) : ringCapacity(usable, ringR)
      if (!st.absorb && st.used >= cap) {
        const lastR = ringR
        const pastBand = TIER_RADII[tier] + (st.ring + 1) * ARC_STAGGER > band[1]
        st.ring++
        st.used = 0
        ringR = ringAt(st.ring)
        if (pastBand) {
          // absorbing ring: the stagger grid would clamp onto the previous
          // ring's radius — inset half a step so the two rings stay distinct;
          // grid size and radius are frozen for the ring's whole remainder.
          if (Math.abs(ringR - lastR) < 1e-9) ringR = Math.max(band[0], band[1] - ARC_STAGGER / 2)
          st.absorb = true
          st.absorbCap = remaining
          st.absorbR = ringR
          cap = Math.max(1, remaining)
        } else {
          cap = ringCapacity(usable, ringR)
        }
      }
      const frac = single
        ? 0.5
        : (((st.used + 0.5) / cap + st.ring * RING_AZ_SPIN) % 1)
      const az = inner + usable * frac
      // flat-disc elevation: a shallow ramp along the arc (±ARC_TILT) —
      // just enough 3D to read under pitch, no per-node latitude lanes.
      const elev = single ? 0 : (frac - 0.5) * 2 * ARC_TILT
      const radius = Math.min(RADIUS_MAX, Math.max(RADIUS_MIN, Math.abs(ringR)))
      const hz = radius * Math.cos(elev)
      placed.push({
        node,
        tier,
        pos: { x: hz * Math.cos(az), y: radius * Math.sin(elev), z: hz * Math.sin(az) },
        r: nodeRadius(node.evidence_count ?? 0),
        faded: tier === 'unseen' && (node.evidence_count ?? 0) > 0,
        recent: Date.parse(node.last_activity_at || node.last_evidence_at || '') > now.getTime() - RECENT_MS,
      })
      st.used++
      tierPlaced.set(tier, (tierPlaced.get(tier) ?? 0) + 1)
    }
  }

  // ---- relaxation pass: pairs whose PROJECTED distance under the default
  // view falls below MIN_SEPARATION get pushed apart — azimuth-preserving
  // radial nudges first (material order on the arc and sector membership
  // stay intact), a tiny azimuth nudge only for equal-radius same-zone
  // pairs. Cross-zone pairs relax too: adjacent sectors' boundary nodes
  // would otherwise overlap on screen. Deterministic: fixed pair order,
  // fixed pass count; convergence is best-effort by design (at absurd
  // single-zone density the circumference itself is the bound — cullLabels
  // still guards the text).
  for (let pass = 0; pass < RELAX_PASSES; pass++) {
    let moved = false
    for (let i = 0; i < placed.length; i++) {
      for (let j = i + 1; j < placed.length; j++) {
        const a = placed[i]
        const b = placed[j]
        const pa = projectPoint(a.pos, DEFAULT_VIEW.yaw, DEFAULT_VIEW.pitch, 560)
        const pb = projectPoint(b.pos, DEFAULT_VIEW.yaw, DEFAULT_VIEW.pitch, 560)
        const d = Math.hypot(pa.x - pb.x, pa.y - pb.y)
        if (d >= MIN_SEPARATION) continue
        const need = MIN_SEPARATION - d
        const ra = Math.hypot(a.pos.x, a.pos.z)
        const rb = Math.hypot(b.pos.x, b.pos.z)
        const sameZone = (a.node.folder_id ?? '') === (b.node.folder_id ?? '')
        if (sameZone && Math.abs(ra - rb) < 1) {
          // same ring: separate azimuthally, keep the radius semantics
          const push = Math.min(need / Math.max(ra, 1), 0.02)
          const ca = Math.cos(push), sa = Math.sin(push)
          const rot = (p: Vec3) => ({ x: p.x * ca - p.z * sa, y: p.y, z: p.x * sa + p.z * ca })
          a.pos = rot(a.pos)
          b.pos = { x: b.pos.x * ca + b.pos.z * sa, y: b.pos.y, z: -b.pos.x * sa + b.pos.z * ca }
        } else {
          const outer = ra > rb ? a : b
          const inner = ra > rb ? b : a
          const ro = ra2d(outer.pos)
          const ri = ra2d(inner.pos)
          // full-need push on the outer node (iteration converges; nodes
          // near the camera axis convert radial motion to screen motion
          // poorly, so half-steps under-correct there)
          const scaleO = Math.min(RADIUS_MAX, ro + need) / ro
          const scaleI = Math.max(RADIUS_MIN, ri - need / 2) / ri
          outer.pos = { x: outer.pos.x * scaleO, y: outer.pos.y * scaleO, z: outer.pos.z * scaleO }
          inner.pos = { x: inner.pos.x * scaleI, y: inner.pos.y * scaleI, z: inner.pos.z * scaleI }
        }
        moved = true
      }
    }
    if (!moved) break
  }

  const bands = (['mastered', 'familiar', 'touched', 'unseen'] as Tier[]).map((tier) => ({
    tier,
    radius: TIER_RADII[tier],
  }))
  return { placed, bands, sectors }
}

/** horizontal (x,z) radius of a point — the push direction of relaxation */
function ra2d(p: { x: number; z: number }): number {
  return Math.hypot(p.x, p.z) || 1
}

/**
 * Perspective projection with an orbit camera (yaw around Y, pitch around
 * X — the two axes a mouse drag controls naturally). Focal length keeps
 * the whole shell inside the viewBox at any rotation; `zoom` scales the
 * projected plane for wheel zoom.
 */
export function projectPoint(p: Vec3, yaw: number, pitch: number, size: number, zoom = 1): Projected {
  const cy = Math.cos(yaw)
  const sy = Math.sin(yaw)
  const x1 = p.x * cy - p.z * sy
  const z1 = p.x * sy + p.z * cy
  const cp = Math.cos(pitch)
  const sp = Math.sin(pitch)
  const y1 = p.y * cp - z1 * sp
  const z2 = p.y * sp + z1 * cp
  const focal = size * 2.2 // long focal ≈ orthographic: less size distortion, easier to scan
  const scale = Math.min(2.2, Math.max(0.3, focal / (focal + z2)))
  const k = scale * zoom
  return {
    x: size / 2 + x1 * k,
    y: size / 2 + y1 * k,
    scale,
    depth: z2,
  }
}

/** Projected polyline for a tier's equatorial ring (the band guide). */
export function ringPoints(radius: number, yaw: number, pitch: number, size: number, zoom = 1, samples = 72): string {
  const pts: string[] = []
  for (let i = 0; i <= samples; i++) {
    const a = (i / samples) * Math.PI * 2
    const p = projectPoint({ x: radius * Math.cos(a), y: 0, z: radius * Math.sin(a) }, yaw, pitch, size, zoom)
    pts.push(`${p.x.toFixed(1)},${p.y.toFixed(1)}`)
  }
  return pts.join(' ')
}

/** Stable display label: page title, else the slug's last path segment. */
export function nodeLabel(title: string | undefined, slug: string): string {
  return title || slug.split('/').pop() || slug
}

// ---------------------------------------------------------------------------
// Decluttering toolkit: truncation, dust folding, label budget. All pure
// and deterministic — the 星图 stays a function of its input, never of
// interaction history (the component owns expansion/pan state).
// ---------------------------------------------------------------------------

/** CJK-aware width units: full-width chars count 1, everything else 0.5. */
export function labelUnits(text: string): number {
  let u = 0
  for (const ch of text) u += isFullWidth(ch) ? 1 : 0.5
  return u
}

function isFullWidth(ch: string): boolean {
  const c = ch.codePointAt(0) ?? 0
  return (
    (c >= 0x2e80 && c <= 0xa4cf) ||
    (c >= 0xf900 && c <= 0xfaff) ||
    (c >= 0x3000 && c <= 0x303f) ||
    (c >= 0xfe30 && c <= 0xfe4f) ||
    (c >= 0xff00 && c <= 0xffef)
  )
}

/** Truncate a display label to `maxUnits` width units, ellipsis included. */
export function truncateLabel(label: string, maxUnits = 9): string {
  if (labelUnits(label) <= maxUnits) return label
  let acc = 0
  let out = ''
  for (const ch of label) {
    const u = isFullWidth(ch) ? 1 : 0.5
    if (acc + u > maxUnits - 1) break
    acc += u
    out += ch
  }
  return out + '…'
}

/** A zone must hold at least this many pure-unseen nodes before folding
 *  them into 星尘 — collapsing two dots behind a badge costs more than it
 *  saves. */
export const DUST_MIN_PER_ZONE = 5

export interface DustPartition {
  /** nodes rendered as individual stars */
  visible: ConstellationNode[]
  /** per zone id: collapsed low-signal unseen nodes (slug-sorted) */
  dust: Map<string, ConstellationNode[]>
}

/**
 * Split nodes into individually-rendered stars and per-zone 星尘 (dust):
 * the dust criterion is "carries no signal worth a pixel" — pure unseen,
 * zero evidence, no fresh activity, not the zone's next, no self-assess
 * declaration, not skipped. Everything with any signal stays visible.
 */
export function partitionDust(nodes: ConstellationNode[], nextSlugs: Set<string>, now: Date): DustPartition {
  const candidates = new Map<string, ConstellationNode[]>()
  const visible: ConstellationNode[] = []
  for (const n of nodes) {
    const zone = n.folder_id ?? ''
    const isDustCandidate =
      tierOf(n.level) === 'unseen' &&
      (n.evidence_count ?? 0) === 0 &&
      !n.skipped &&
      !n.self_assess &&
      !nextSlugs.has(n.slug) &&
      !(Date.parse(n.last_activity_at || n.last_evidence_at || '') > now.getTime() - RECENT_MS)
    if (!isDustCandidate) {
      visible.push(n)
      continue
    }
    const arr = candidates.get(zone) ?? []
    arr.push(n)
    candidates.set(zone, arr)
  }
  const dust = new Map<string, ConstellationNode[]>()
  for (const [zone, arr] of candidates) {
    if (arr.length < DUST_MIN_PER_ZONE) {
      visible.push(...arr)
    } else {
      arr.sort((a, b) => a.slug.localeCompare(b.slug))
      dust.set(zone, arr)
    }
  }
  return { visible, dust }
}

/** Deterministic importance for the label budget: higher wins the slot.
 *  Reading order mirrors what the learning tab already privileges —
 *  verified mastery, fresh activity, the zone's next step, the user's own
 *  declarations — before tier, before evidence volume. */
export function labelPriority(args: {
  tier: Tier
  faded?: boolean
  recent?: boolean
  isNext?: boolean
  selfAssess?: boolean
  skipped?: boolean
  evidence?: number
}): number {
  const base =
    args.tier === 'mastered' ? 800 : args.tier === 'familiar' ? 500 : args.tier === 'touched' ? 300 : args.faded ? 120 : 40
  let p = base
  if (args.recent) p += 150
  if (args.isNext) p += 120
  if (args.selfAssess) p += 100
  if (args.skipped) p -= 60
  p += Math.min(90, Math.max(0, args.evidence ?? 0) * 8)
  return p
}

export interface LabelSlot {
  slug: string
  /** screen-space anchor: label centre x, text baseline y (below the dot) */
  x: number
  y: number
  widthPx: number
  priority: number
  /** placed first and never dropped (the hovered node) */
  pinned?: boolean
}

/** label rect height around the baseline, in screen px */
export const LABEL_HEIGHT_PX = 12

/**
 * Greedy no-overlap label placement (the decluttering core): candidates
 * are placed in priority order; a label whose rect hits an already-placed
 * rect — the hub disc (the map's fixed landmark), or a caller-supplied
 * obstacle (e.g. the zone-name rim labels) — is DROPPED, not nudged: its
 * node stays clickable, only the text waits for hover/zoom.
 * Returns the set of slugs whose labels are shown.
 */
export function cullLabels(
  slots: LabelSlot[],
  hub: { x: number; y: number; r: number },
  obstacles: { x0: number; y0: number; x1: number; y1: number }[] = [],
): Set<string> {
  const order = [...slots].sort(
    (a, b) =>
      (b.pinned ? 1 : 0) - (a.pinned ? 1 : 0) || b.priority - a.priority || a.slug.localeCompare(b.slug),
  )
  const placedRects = [...obstacles]
  const keep = new Set<string>()
  for (const s of order) {
    const x0 = s.x - s.widthPx / 2 - 2
    const x1 = s.x + s.widthPx / 2 + 2
    const y0 = s.y - 9
    const y1 = s.y + 3
    // hub obstacle: rect-vs-circle, closest-point test
    const cx = Math.max(x0, Math.min(hub.x, x1))
    const cy = Math.max(y0, Math.min(hub.y, y1))
    const hitsHub = (cx - hub.x) ** 2 + (cy - hub.y) ** 2 < hub.r * hub.r
    const hitsLabel = placedRects.some((r) => x0 < r.x1 + 1 && r.x0 - 1 < x1 && y0 < r.y1 + 1 && r.y0 - 1 < y1)
    if ((hitsHub || hitsLabel) && !s.pinned) continue
    placedRects.push({ x0, y0, x1, y1 })
    keep.add(s.slug)
  }
  return keep
}
