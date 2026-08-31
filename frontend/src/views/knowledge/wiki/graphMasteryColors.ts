/**
 * Mastery tier → visual attributes for the wiki graph, extracted as a
 * pure module so the mapping is testable without mounting the 6000-line
 * browser component (the wikiDirectoryState.ts precedent).
 *
 * Design contract (stage 4 of the knowledge-network feature):
 * - Two orthogonal visual channels, never competing for the same pixel:
 *   the node FILL always carries the page-type color (the pre-existing
 *   semantic — "upgrade, don't replace"), and the mastery tier is
 *   expressed ONLY by the ring around the node. Overriding the fill with
 *   tier colors ate the type information and, with the green brand ramp,
 *   collided head-on with the entity type green (#2ba471 vs #07c05f).
 * - The ring palette rides WeKnora's green brand ramp (TDesign light
 *   theme: brand-4 #07c05f main, brand-7 #038626 deep); unseen keeps the
 *   spec's neutral gray. `mastered` earns the widest ring plus a soft
 *   glow; low-confidence rings are dashed.
 */

export type MasteryLevel = 'unseen' | 'touched' | 'familiar' | 'mastered' | '';

const TIER_COLORS: Record<Exclude<MasteryLevel, ''>, string> = {
  unseen: '#d0d0d0',
  touched: '#8ce0af',
  familiar: '#07c05f',
  mastered: '#038626',
};

/** The tier color for chips, legends, cards — anything needing the raw ramp value. */
export function tierColor(level: MasteryLevel | undefined | null): string {
  if (!level) return '';
  return TIER_COLORS[level] || '';
}

/**
 * The mastery ring attributes for a node: stroke color, stroke width,
 * dashed (low confidence), and glow (mastered emphasis). `visible` is
 * false when there is no overlay at all — the caller skips the ring.
 */
export interface MasteryRingPaint {
  visible: boolean;
  stroke: string;
  width: number;
  dashed: boolean;
  glow: boolean;
}

export function masteryRing(level: MasteryLevel | undefined | null, lowConfidence?: boolean): MasteryRingPaint {
  const stroke = tierColor(level);
  if (!stroke) {
    return { visible: false, stroke: '', width: 0, dashed: false, glow: false };
  }
  return {
    visible: true,
    stroke,
    width: level === 'mastered' ? 3.5 : 2,
    dashed: Boolean(lowConfidence),
    glow: level === 'mastered',
  };
}

/**
 * Node fill resolution: the page-type color, period. The mastery tier
 * lives on the ring — the fill channel stays reserved for "what is this
 * node" (type), so the two semantics can be read side by side.
 */
export function nodeFill(type: string, typeColor: string, _level?: MasteryLevel | null): string {
  void type;
  void _level;
  return typeColor;
}

/**
 * Diff two mastery snapshots and return the slugs whose tier ROSE into
 * lit territory (touched/familiar/mastered) since the last render — the
 * "newly lit this session" pulse set.
 */
export function newlyLitSlugs(
  previous: Record<string, MasteryLevel>,
  current: Record<string, MasteryLevel>,
): string[] {
  const isLit = (l?: MasteryLevel) => l === 'touched' || l === 'familiar' || l === 'mastered';
  const out: string[] = [];
  for (const [slug, level] of Object.entries(current)) {
    if (isLit(level) && !isLit(previous[slug])) {
      out.push(slug);
    }
  }
  return out.sort();
}
