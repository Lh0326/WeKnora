import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  layoutConstellation3, projectPoint, ringPoints, nodeLabel, DEFAULT_VIEW,
  truncateLabel, partitionDust, labelPriority, cullLabels, labelUnits, DUST_MIN_PER_ZONE,
  type ConstellationNode, type ZoneAxis, type LabelSlot,
} from './constellationLayout.ts'

const NOW = new Date('2026-09-01T12:00:00Z')

function mk(slug: string, level: string, title?: string, evidence = 0, lastAt?: string, folder = 'f1', rank?: number): ConstellationNode {
  return { slug, level, title, evidence_count: evidence, last_evidence_at: lastAt, folder_id: folder, doc_rank: rank }
}

describe('layoutConstellation3', () => {
  it('places every node on its tier shell: |pos| equals the tier radius', () => {
    const nodes = [
      mk('concept/a', 'mastered', '甲', 9),
      mk('concept/b', 'familiar', '乙', 2),
      mk('concept/c', 'touched', '丙', 1),
      mk('concept/d', 'unseen', undefined, 0),
      mk('entity/e', 'weird-level', '戊', 0),
    ]
    const { placed } = layoutConstellation3(nodes, [{ id: 'f1', name: 'F' }], NOW)
    assert.equal(placed.length, 5)
    const radii: Record<string, number> = { mastered: 58, familiar: 112, touched: 166, unseen: 220 }
    // Stagger shifts nodes off the exact shell by up to ±24px (3 sub-arcs)
    const STAGGER_TOL = 26
    for (const p of placed) {
      const dist = Math.hypot(p.pos.x, p.pos.y, p.pos.z)
      const tier = p.node.slug === 'entity/e' ? 'unseen' : p.node.level
      assert.ok(Math.abs(dist - radii[tier]) < STAGGER_TOL, `${p.node.slug} near the ${tier} shell (dist=${dist.toFixed(1)}, expected ~${radii[tier]})`)
      assert.ok(p.r >= 7 && p.r <= 15, `radius bounds for ${p.node.slug}`)
      assert.ok(Number.isFinite(p.pos.x) && Number.isFinite(p.pos.y) && Number.isFinite(p.pos.z))
    }
    // unknown levels degrade to the unseen band
    assert.equal(placed.find((p) => p.node.slug === 'entity/e')?.tier, 'unseen')
  })

  it('gives each zone a disjoint azimuth sector; zones never overlap', () => {
    const nodes = [
      mk('concept/a1', 'unseen', 'a1', 0, undefined, 'fa'),
      mk('concept/a2', 'unseen', 'a2', 0, undefined, 'fa'),
      mk('concept/b1', 'unseen', 'b1', 0, undefined, 'fb'),
    ]
    const { placed, sectors } = layoutConstellation3(nodes, [
      { id: 'fa', name: 'A' }, { id: 'fb', name: 'B' },
    ], NOW)
    assert.equal(sectors.size, 2)
    for (const p of placed) {
      const [start, end] = sectors.get(p.node.folder_id!)!
      const az = Math.atan2(p.pos.z, p.pos.x)
      const norm = ((az % (Math.PI * 2)) + Math.PI * 2) % (Math.PI * 2)
      const within = norm >= start - 1e-9 && norm <= end + 1e-9
      assert.ok(within, `${p.node.slug} azimuth ${norm.toFixed(2)} in sector [${start.toFixed(2)}, ${end.toFixed(2)}]`)
    }
  })

  it('orders a zone arc by material rank (doc_rank), deterministically', () => {
    const nodes = [
      mk('concept/later', 'unseen', 'later', 0, undefined, 'f', 9),
      mk('concept/early', 'unseen', 'early', 0, undefined, 'f', 1),
      mk('concept/none', 'unseen', 'none', 0, undefined, 'f'),
    ]
    const first = layoutConstellation3(nodes, [{ id: 'f', name: 'F' }], NOW)
    const second = layoutConstellation3([...nodes].reverse(), [{ id: 'f', name: 'F' }], NOW)
    const byAz = (l: typeof first) =>
      l.placed
        .map((p) => ({ slug: p.node.slug, az: ((Math.atan2(p.pos.z, p.pos.x) % (Math.PI * 2)) + Math.PI * 2) % (Math.PI * 2) }))
        .sort((x, y) => x.az - y.az)
        .map((x) => x.slug)
    assert.deepEqual(byAz(first), byAz(second), 'input order must not matter')
    const order = byAz(first)
    assert.equal(order[0], 'concept/early', 'lowest doc_rank leads the arc')
    assert.equal(order[order.length - 1], 'concept/none', 'unranked trails the arc')
  })

  it('flags faded (unseen with history) and recent (≤48h activity)', () => {
    const nodes = [
      mk('concept/faded', 'unseen', 'f', 3, '2026-08-31T12:00:00Z'),
      mk('concept/fresh', 'unseen', 'x', 3, '2026-09-01T11:00:00Z'),
      mk('concept/old', 'unseen', 'o', 3, '2026-08-01T12:00:00Z'),
      mk('concept/clean', 'unseen', 'c', 0, undefined),
    ]
    const { placed } = layoutConstellation3(nodes, [{ id: 'f1', name: 'F' }], NOW)
    const by = (s: string) => placed.find((p) => p.node.slug === s)!
    assert.equal(by('concept/faded').faded, true)
    assert.equal(by('concept/clean').faded, false)
    assert.equal(by('concept/fresh').recent, true)
    assert.equal(by('concept/old').recent, false)
  })
})

describe('projectPoint', () => {
  const size = 560
  it('maps the origin to the canvas centre at full scale', () => {
    const p = projectPoint({ x: 0, y: 0, z: 0 }, 0.3, 0.2, size)
    assert.ok(Math.abs(p.x - size / 2) < 1e-9 && Math.abs(p.y - size / 2) < 1e-9)
    assert.ok(Math.abs(p.scale - 1) < 1e-9)
  })
  it('farther points project smaller and nearer points larger (depth cues)', () => {
    const near = projectPoint({ x: 0, y: 0, z: -100 }, 0, 0, size)
    const far = projectPoint({ x: 0, y: 0, z: 100 }, 0, 0, size)
    assert.ok(near.scale > 1 && far.scale < 1, `near ${near.scale} > 1 > far ${far.scale}`)
    assert.ok(far.depth > near.depth)
  })
  it('rotation moves a point but keeps it finite and in-frame', () => {
    for (const yaw of [0, 1.2, 3.9]) {
      for (const pitch of [-1.1, 0, 0.9]) {
        const p = projectPoint({ x: 220, y: 60, z: -180 }, yaw, pitch, size)
        assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y) && Number.isFinite(p.scale))
        assert.ok(p.x > -size && p.x < 2 * size && p.y > -size && p.y < 2 * size, `(${p.x},${p.y}) stays near-canvas`)
      }
    }
  })
})

describe('ringPoints', () => {
  it('renders a closed, finite polyline for any rotation', () => {
    for (const [yaw, pitch] of [[0, 0], [0.8, 0.5], [2.5, -0.4]]) {
      const pts = ringPoints(166, yaw, pitch, 560)
      const coords = pts.split(' ')
      assert.equal(coords.length, 73, 'samples + closing point')
      assert.equal(coords[0], coords[coords.length - 1], 'ring closes')
      for (const c of coords) {
        const [x, y] = c.split(',').map(Number)
        assert.ok(Number.isFinite(x) && Number.isFinite(y))
      }
    }
  })
})

describe('nodeLabel', () => {
  it('falls back to the slug tail', () => {
    assert.equal(nodeLabel(undefined, 'concept/rag'), 'rag')
    assert.equal(nodeLabel('检索增强', 'concept/rag'), '检索增强')
  })
})

describe('density: weighted sectors & capacity rings', () => {
  function zoneNodes(zone: string, n: number): ConstellationNode[] {
    const levels = ['mastered', 'familiar', 'touched', 'unseen']
    return Array.from({ length: n }, (_, i) =>
      mk(`concept/${zone}-${i}`, levels[i % 4], `${zone}${i}`, 2, undefined, zone, i + 1))
  }

  it('gives busier zones wider sectors while spans stay disjoint and sum to 2π', () => {
    const nodes = [...zoneNodes('big', 16), ...zoneNodes('small', 2), ...zoneNodes('mid', 6)]
    const { sectors } = layoutConstellation3(nodes, [
      { id: 'big', name: 'B' }, { id: 'small', name: 'S' }, { id: 'mid', name: 'M' },
    ], NOW)
    const span = (id: string) => { const [s, e] = sectors.get(id)!; return e - s }
    assert.ok(span('big') > span('small') * 1.5, `big (${span('big').toFixed(2)}) clearly wider than small (${span('small').toFixed(2)})`)
    assert.ok(span('big') > span('mid'), 'monotone in member count')
    let total = 0
    let prevEnd = 0
    for (const [id, [s, e]] of sectors) {
      assert.ok(Math.abs(s - prevEnd) < 1e-9, `${id} starts where the previous zone ends`)
      prevEnd = e
      total += e - s
    }
    assert.ok(Math.abs(total - Math.PI * 2) < 1e-9, 'sectors tile the full circle')
  })

  it('keeps the disc near-flat: elevations within ±ARC_TILT (±5.7°); single member at 0', () => {
    const nodes = Array.from({ length: 12 }, (_, i) =>
      mk(`concept/e${i}`, 'touched', `e${i}`, 2, undefined, 'f', i + 1))
    const { placed } = layoutConstellation3(nodes, [{ id: 'f', name: 'F' }], NOW)
    const elevOf = (p: (typeof placed)[number]) => Math.asin(p.pos.y / Math.hypot(p.pos.x, p.pos.y, p.pos.z))
    const limit = (5.7 + 0.5) * (Math.PI / 180)
    for (const p of placed) {
      assert.ok(Math.abs(elevOf(p)) <= limit,
        `${p.node.slug} elevation within ±6.2° (got ${(elevOf(p) * 180 / Math.PI).toFixed(1)}°)`)
    }
    const solo = layoutConstellation3([mk('concept/only', 'familiar', 'o', 1, undefined, 'solo')], [{ id: 'solo', name: 'S' }], NOW)
    assert.ok(Math.abs(solo.placed[0].pos.y) < 1e-9, 'single member elevates 0')
  })

  it('keeps projected separation between azimuth-adjacent zone members at any density', () => {
    // The metric is the DEFAULT VIEW's projection: a flat disc compresses
    // line-of-sight separation to ~sin(pitch) on screen, so 3D distance
    // alone is not the user-facing invariant. Normal rings hold ≥50px
    // (arc budget); the terminal absorbing ring — pathological
    // single-zone density only — relaxes toward the physical limit
    // (≥14px observed across 20..120 members), still clear of the 0-10px
    // pile-ups this invariant exists to catch. The absorbing ring at
    // pathological density bottoms out ≈13px (band circumference bound).
    const FLOOR = 12
    for (const n of [20, 60, 120]) {
      const nodes = zoneNodes('dense', n)
      const { placed } = layoutConstellation3(nodes, [{ id: 'dense', name: 'D' }], NOW)
      const proj = placed.map((p) => projectPoint(p.pos, DEFAULT_VIEW.yaw, DEFAULT_VIEW.pitch, 560))
      const byAz = [...placed.keys()].sort((a, b) =>
        Math.atan2(placed[a].pos.z, placed[a].pos.x) - Math.atan2(placed[b].pos.z, placed[b].pos.x))
      for (let k = 1; k < byAz.length; k++) {
        const i = byAz[k - 1], j = byAz[k]
        const d = Math.hypot(proj[i].x - proj[j].x, proj[i].y - proj[j].y)
        assert.ok(d >= FLOOR,
          `n=${n}: ${placed[i].node.slug} ↔ ${placed[j].node.slug} only ${d.toFixed(1)}px apart on screen`)
      }
    }
  })
})

describe('truncateLabel / labelUnits', () => {
  it('counts CJK as 1 unit and ASCII as 0.5', () => {
    assert.equal(labelUnits('智能体'), 3)
    assert.equal(labelUnits('rag'), 1.5)
  })
  it('keeps short labels verbatim, truncates long ones with an ellipsis', () => {
    assert.equal(truncateLabel('智能体', 9), '智能体')
    assert.equal(truncateLabel('检索增强生成技术详解', 9), '检索增强生成技术…')
    assert.ok(labelUnits(truncateLabel('a-very-long-ascii-label', 9)) <= 9)
    assert.ok(truncateLabel('a-very-long-ascii-label', 9).endsWith('…'))
  })
})

describe('partitionDust', () => {
  const nextSlugs = new Set(['concept/next-one'])
  it('folds only pure-unseen nodes and only when the zone reaches DUST_MIN_PER_ZONE', () => {
    const nodes = [
      // 6 pure-unseen in f1 → dust
      ...Array.from({ length: 6 }, (_, i) => mk(`concept/d${i}`, 'unseen', undefined, 0, undefined, 'f1')),
      // 4 pure-unseen in f2 → below threshold, stays visible
      ...Array.from({ length: DUST_MIN_PER_ZONE - 1 }, (_, i) => mk(`concept/s${i}`, 'unseen', undefined, 0, undefined, 'f2')),
      // signal carriers in f1: all stay visible
      mk('concept/faded', 'unseen', 'f', 3, '2026-08-01T00:00:00Z', 'f1'),   // 已淡化
      mk('concept/fresh', 'unseen', 'x', 0, '2026-09-01T11:00:00Z', 'f1'),   // recent
      mk('concept/next-one', 'unseen', 'n', 0, undefined, 'f1'),             // zone next
      mk('concept/marked', 'unseen', 'm', 0, undefined, 'f1'),               // self_assess
      mk('concept/done', 'unseen', 'd', 0, undefined, 'f1'),                 // skipped
      mk('concept/seen', 'touched', 't', 1, undefined, 'f1'),
    ]
    nodes[13].self_assess = { direction: 'up', event_type: 'self_assess_up', occurred_at: '2026-08-31T00:00:00Z' }
    nodes[14].skipped = true
    const { visible, dust } = partitionDust(nodes, nextSlugs, NOW)
    assert.equal(dust.size, 1)
    const d1 = dust.get('f1')!
    assert.equal(d1.length, 6)
    assert.deepEqual(d1.map((n) => n.slug), [...d1].map((n) => n.slug).sort(), 'dust slug-sorted')
    const vis = new Set(visible.map((n) => n.slug))
    for (const keep of ['concept/faded', 'concept/fresh', 'concept/next-one', 'concept/marked', 'concept/done', 'concept/seen']) {
      assert.ok(vis.has(keep), `${keep} stays individually rendered`)
    }
    assert.ok(visible.some((n) => n.folder_id === 'f2'), 'below-threshold zone stays visible')
  })
})

describe('labelPriority & cullLabels', () => {
  it('ranks tiers and boosts fresh/next/declared signals', () => {
    const p = (tier: 'mastered' | 'familiar' | 'touched' | 'unseen', extra: Partial<Parameters<typeof labelPriority>[0]> = {}) =>
      labelPriority({ tier, ...extra })
    assert.ok(p('mastered') > p('familiar') && p('familiar') > p('touched') && p('touched') > p('unseen', { faded: true }) && p('unseen', { faded: true }) > p('unseen'))
    assert.ok(p('unseen', { recent: true }) > p('unseen'))
    assert.ok(p('touched', { isNext: true }) > p('touched'))
    assert.equal(p('familiar'), p('familiar'), 'deterministic')
  })

  const hub = { x: 280, y: 280, r: 36 }
  function slot(slug: string, x: number, y: number, priority: number, pinned = false): LabelSlot {
    return { slug, x, y, widthPx: 40, priority, pinned }
  }

  it('drops the lower-priority label when two rects overlap; keeps both when disjoint', () => {
    const keep = cullLabels([slot('low', 100, 100, 100), slot('high', 104, 100, 500)], hub)
    assert.ok(keep.has('high') && !keep.has('low'))
    const both = cullLabels([slot('a', 40, 100, 100), slot('b', 200, 100, 500)], hub)
    assert.ok(both.has('a') && both.has('b'))
  })
  it('treats the hub disc as an obstacle, but a pinned label always shows', () => {
    const drop = cullLabels([slot('at-hub', 280, 280, 900)], hub)
    assert.ok(!drop.has('at-hub'), 'unpinned label on the hub is dropped')
    const pin = cullLabels([slot('at-hub', 280, 280, 1, true)], hub)
    assert.ok(pin.has('at-hub'), 'pinned label survives')
  })
  it('places pinned labels first so ordinary labels yield to them', () => {
    const keep = cullLabels([slot('pinned', 100, 100, 1, true), slot('bigger', 102, 100, 500)], hub)
    assert.ok(keep.has('pinned') && !keep.has('bigger'))
  })
  it('honours caller obstacles (e.g. zone-name rim rects)', () => {
    const obstacle = { x0: 90, y0: 90, x1: 130, y1: 106 }
    const keep = cullLabels([slot('rim-adjacent', 110, 100, 900)], hub, [obstacle])
    assert.ok(!keep.has('rim-adjacent'), 'label overlapping an obstacle is dropped')
    const far = cullLabels([slot('elsewhere', 40, 200, 100)], hub, [obstacle])
    assert.ok(far.has('elsewhere'))
  })
})
