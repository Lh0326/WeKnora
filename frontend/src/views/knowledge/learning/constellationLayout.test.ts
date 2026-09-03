import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { layoutConstellation, nodeLabel, type ConstellationNode } from './constellationLayout.ts'

const NOW = new Date('2026-09-01T12:00:00Z')

function mk(slug: string, level: string, title?: string, evidence = 0, lastAt?: string): ConstellationNode {
  return { slug, level, title, evidence_count: evidence, last_evidence_at: lastAt }
}

describe('layoutConstellation', () => {
  it('places every node at a finite coordinate inside the canvas', () => {
    const nodes = [
      mk('concept/a', 'mastered', '甲', 9),
      mk('concept/b', 'familiar', '乙', 2),
      mk('concept/c', 'touched', '丙', 1),
      mk('concept/d', 'unseen', undefined, 0),
      mk('entity/e', 'weird-level', '戊', 0),
    ]
    const { placed } = layoutConstellation(nodes, NOW)
    assert.equal(placed.length, 5)
    for (const p of placed) {
      assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y), `finite coords for ${p.node.slug}`)
      assert.ok(p.x >= 0 && p.x <= 560 && p.y >= 0 && p.y <= 560, `in-canvas ${p.node.slug}`)
      assert.ok(p.r >= 7 && p.r <= 15, `radius bounds for ${p.node.slug}`)
    }
    // unknown levels degrade to the unseen band
    assert.equal(placed.find((p) => p.node.slug === 'entity/e')?.tier, 'unseen')
  })

  it('is deterministic: same input, identical picture', () => {
    const nodes = [
      mk('concept/b', 'touched', '乙'), mk('concept/a', 'touched', '甲'),
      mk('concept/z', 'unseen', '丙'), mk('concept/c', 'mastered', '丁'),
    ]
    const a = layoutConstellation(nodes, NOW)
    const b = layoutConstellation([...nodes].reverse(), NOW)
    assert.deepEqual(a.placed.map((p) => [p.node.slug, p.x, p.y]), b.placed.map((p) => [p.node.slug, p.x, p.y]))
  })

  it('keeps mastered innermost and unseen outermost', () => {
    const { placed, bands } = layoutConstellation([
      mk('a', 'mastered', '甲'), mk('b', 'unseen', '乙'), mk('c', 'familiar', '丙'), mk('d', 'touched', '丁'),
    ], NOW)
    const dist = (p: { x: number; y: number }) => Math.hypot(p.x - 280, p.y - 280)
    const d = Object.fromEntries(placed.map((p) => [p.tier, dist(p)]))
    assert.ok(d.mastered < d.familiar && d.familiar < d.touched && d.touched < d.unseen)
    assert.deepEqual(bands.map((b) => b.tier), ['mastered', 'familiar', 'touched', 'unseen'])
    assert.ok(bands.every((b) => b.count === 1))
  })

  it('spreads a tiers nodes over the full circle without overlap at small counts', () => {
    const many = Array.from({ length: 6 }, (_, i) => mk(`concept/n${i}`, 'unseen', `节${i}`))
    const { placed } = layoutConstellation(many, NOW)
    const seen = new Set(placed.map((p) => `${p.x},${p.y}`))
    assert.equal(seen.size, 6, 'distinct positions')
    // pairwise min distance comfortably above 2×min radius
    for (let i = 0; i < placed.length; i++) {
      for (let j = i + 1; j < placed.length; j++) {
        const d = Math.hypot(placed[i].x - placed[j].x, placed[i].y - placed[j].y)
        assert.ok(d > 14, `nodes ${i}/${j} overlap at ${d.toFixed(1)}px`)
      }
    }
  })

  it('flags faded (unseen with history) and recent (≤48h activity)', () => {
    const { placed } = layoutConstellation([
      mk('a', 'unseen', '甲', 3, '2026-09-01T10:00:00Z'), // recent faded
      mk('b', 'unseen', '乙', 0), // fresh unseen
      mk('c', 'mastered', '丙', 5, '2026-08-01T00:00:00Z'), // old mastered
      mk('d', 'mastered', '丁', 5, '2099-01-01T00:00:00Z'), // future timestamp → not recent
    ], NOW)
    const by = Object.fromEntries(placed.map((p) => [p.node.slug, p]))
    assert.equal(by.a.faded, true)
    assert.equal(by.a.recent, true)
    assert.equal(by.b.faded, false)
    assert.equal(by.c.recent, false)
    assert.equal(by.d.recent, false)
  })

  it('uses last_activity_at (zero-weight touches) over the folded timestamp', () => {
    // 今天答了「不确定」（零权重不折叠）的老节点：last_evidence_at 停在五天前，
    // 但 last_activity_at 是刚刚——recent 必须为真（学了就要闪）。
    const stale = mk('e', 'touched', '戊', 4, '2026-08-29T00:00:00Z')
    stale.last_activity_at = '2026-09-01T09:30:00Z'
    const { placed } = layoutConstellation([stale], NOW)
    assert.equal(placed[0].recent, true)
    // 反向：两者都旧则不闪
    const old = mk('f', 'touched', '己', 4, '2026-08-29T00:00:00Z')
    old.last_activity_at = '2026-08-29T00:00:00Z'
    const r2 = layoutConstellation([old], NOW)
    assert.equal(r2.placed[0].recent, false)
  })

  it('handles the empty universe without NaN', () => {
    const { placed, bands } = layoutConstellation([], NOW)
    assert.equal(placed.length, 0)
    assert.deepEqual(bands.map((b) => b.count), [0, 0, 0, 0])
  })
})

describe('nodeLabel', () => {
  it('prefers the title and truncates long names with an ellipsis', () => {
    assert.equal(nodeLabel('检索增强生成', 'concept/rag'), '检索增强生成')
    assert.equal(nodeLabel('FSRS记忆稳定性思想', 'concept/fsrs'), 'FSRS记忆…')
    assert.equal(nodeLabel(undefined, 'concept/rag'), 'rag')
    assert.equal(nodeLabel('  ', 'concept/rag'), 'rag')
  })
})
