/**
 * Pure deterministic layout for the learning tab's knowledge constellation
 * (知识星图): every node of the KB placed on four concentric tier bands —
 * mastered nearest the centre, unseen on the rim — so "how far in my
 * knowledge has progressed" reads as a single glance, not a list.
 *
 * Extracted as a pure module for the same reason as graphMasteryColors.ts:
 * positioning is policy (deterministic, no physics), and policy belongs in a
 * unit-testable file rather than a 1000-line component.
 *
 * Layout contract:
 * - Deterministic: same node set → identical positions (stable sort by
 *   title, then slug; fixed per-tier angular offsets so rings never share a
 *   spoke).
 * - All coordinates are finite and inside the canvas for any input,
 *   including empty tiers and huge evidence counts.
 * - Node radius encodes evidence volume (7..15px, saturating); the `faded`
 *   (unseen-with-history) and `recent` (activity ≤48h) flags drive styling.
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
}

export interface PlacedNode {
  node: ConstellationNode
  tier: Tier
  x: number
  y: number
  /** node circle radius in px (evidence volume, saturating) */
  r: number
  /** angle in degrees from 12 o'clock, for label anchoring */
  angle: number
  /** unseen tier but WITH history — rendered amber, the 已淡化 tier */
  faded: boolean
  /** evidence within RECENT_MS of `now` — rendered twinkling */
  recent: boolean
}

export interface TierBand {
  tier: Tier
  /** mid-band radius */
  radius: number
  count: number
}

export interface ConstellationLayout {
  placed: PlacedNode[]
  bands: TierBand[]
  size: number
  center: number
}

/** Mid-band radii for the four tiers, innermost = mastered. */
const BAND_RADII: Record<Tier, number> = {
  mastered: 64,
  familiar: 122,
  touched: 180,
  unseen: 238,
}

/** Per-tier starting angle (degrees) so adjacent rings' nodes interleave. */
const TIER_OFFSET: Record<Tier, number> = {
  mastered: -90,
  familiar: -74,
  touched: -58,
  unseen: -42,
}

export const RECENT_MS = 48 * 3600 * 1000

function tierOf(level: string): Tier {
  return level === 'mastered' || level === 'familiar' || level === 'touched' ? (level as Tier) : 'unseen'
}

export function layoutConstellation(nodes: ConstellationNode[], now: Date = new Date()): ConstellationLayout {
  const size = 560
  const center = size / 2

  const byTier: Record<Tier, ConstellationNode[]> = { unseen: [], touched: [], familiar: [], mastered: [] }
  for (const n of nodes) {
    if (!n || !n.slug) continue
    byTier[tierOf(n.level || 'unseen')].push(n)
  }

  const placed: PlacedNode[] = []
  const bands: TierBand[] = []
  const tiers: Tier[] = ['mastered', 'familiar', 'touched', 'unseen']
  for (const tier of tiers) {
    const members = byTier[tier]
    // Stable order: title, then slug — identical input, identical picture.
    members.sort((a, b) => (a.title || a.slug).localeCompare(b.title || b.slug, 'en') || a.slug.localeCompare(b.slug, 'en'))
    bands.push({ tier, radius: BAND_RADII[tier], count: members.length })
    members.forEach((node, i) => {
      const step = members.length > 0 ? 360 / members.length : 360
      const angle = TIER_OFFSET[tier] + i * step
      const rad = ((angle - 90) * Math.PI) / 180
      const evidence = Math.max(0, Math.floor(node.evidence_count ?? 0))
      placed.push({
        node,
        tier,
        x: Math.round((center + BAND_RADII[tier] * Math.cos(rad)) * 100) / 100,
        y: Math.round((center + BAND_RADII[tier] * Math.sin(rad)) * 100) / 100,
        r: 7 + Math.min(evidence, 8),
        angle,
        faded: tier === 'unseen' && evidence > 0,
        recent: isRecent(node.last_activity_at || node.last_evidence_at, now),
      })
    })
  }
  return { placed, bands, size, center }
}

function isRecent(at: string | undefined, now: Date): boolean {
  if (!at) return false
  const t = new Date(at).getTime()
  if (!Number.isFinite(t)) return false
  return now.getTime() - t >= 0 && now.getTime() - t <= RECENT_MS
}

/** Truncate a node label for the ring: 6 CJK chars keeps rings readable.
 * The no-title fallback strips the type prefix (concept/rag → rag) so the
 * degraded path never paints a raw English path on the canvas. */
export function nodeLabel(title: string | undefined, slug: string): string {
  const raw = (title && title.trim()) || slug.split('/').pop() || slug
  return raw.length > 7 ? `${raw.slice(0, 6)}…` : raw
}
