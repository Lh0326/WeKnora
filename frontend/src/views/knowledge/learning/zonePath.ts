// Pure helpers for the zone card's expandable learning path: group the
// ZoneMap nodes by module folder, order each module's nodes into the
// 从浅入深 material order, and stamp a 1-based path position. Deliberately
// framework-free so the ordering contract is unit-testable like
// constellationLayout.ts.

export interface ZonePathSourceNode {
  slug: string
  title?: string
  level: string
  p_eff: number
  faded?: boolean
  skipped?: boolean
  /** 1-based position in the source material (从浅入深); 0/absent = not resolved. */
  doc_rank?: number
  folder_id: string
}

export interface ZonePathEntry {
  slug: string
  title: string
  level: string
  pEff: number
  faded: boolean
  skipped: boolean
  /** 1-based position on the module's learning path. */
  order: number
  /** Material rank carried through for callers that want it; 0 = unordered. */
  rank: number
}

/**
 * buildZonePaths groups the constellation nodes per module and orders each
 * module into its learning path: material order first (doc_rank asc — the
 * 从浅入深 channel's position in the source docs), nodes without a material
 * position trail afterwards, and ties break deterministically by title then
 * slug so the path never re-shuffles between refreshes.
 */
export function buildZonePaths(nodes: ZonePathSourceNode[]): Record<string, ZonePathEntry[]> {
  const grouped: Record<string, ZonePathEntry[]> = {}
  for (const n of nodes) {
    const key = n.folder_id || 'root'
    ;(grouped[key] ??= []).push({
      slug: n.slug,
      title: n.title || n.slug,
      level: n.level,
      pEff: n.p_eff,
      faded: !!n.faded,
      skipped: !!n.skipped,
      order: 0,
      rank: n.doc_rank || 0,
    })
  }
  for (const key of Object.keys(grouped)) {
    const list = grouped[key]
    // Unranked (rank 0) trails the material order: rank 0 reads as +∞.
    const materialRank = (r: number) => (r > 0 ? r : Number.POSITIVE_INFINITY)
    list.sort((a, b) => {
      const ra = materialRank(a.rank), rb = materialRank(b.rank)
      if (ra !== rb) return ra - rb
      if (a.title !== b.title) return a.title < b.title ? -1 : 1
      return a.slug < b.slug ? -1 : 1
    })
    list.forEach((entry, i) => { entry.order = i + 1 })
  }
  return grouped
}

/** circledNum renders 1..20 as ①..⑳ (the card's ①② vocabulary) and falls
 * back to plain numerals past twenty, so a 40-node module stays readable. */
const CIRCLED = ['①', '②', '③', '④', '⑤', '⑥', '⑦', '⑧', '⑨', '⑩',
  '⑪', '⑫', '⑬', '⑭', '⑮', '⑯', '⑰', '⑱', '⑲', '⑳']
export function circledNum(n: number): string {
  return n >= 1 && n <= CIRCLED.length ? CIRCLED[n - 1] : `${n}.`
}

/**
 * pathOrderOf answers "which stop on the module's learning path is this
 * slug" — the single ordinal source the collapsed cursor row and the
 * expanded path rows share. Returns 0 when the slug is not on the path
 * (callers render no number rather than a wrong one).
 */
export function pathOrderOf(paths: Record<string, ZonePathEntry[]>, key: string, slug: string): number {
  const list = paths[key]
  if (!list) return 0
  const hit = list.find((e) => e.slug === slug)
  return hit ? hit.order : 0
}
