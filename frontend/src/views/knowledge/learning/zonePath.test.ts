import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { buildZonePaths, circledNum, type ZonePathSourceNode } from './zonePath.ts'

function mk(slug: string, folder: string, rank?: number, title?: string, level = 'unseen', pEff = 0, extra: Partial<ZonePathSourceNode> = {}): ZonePathSourceNode {
  return { slug, folder_id: folder, doc_rank: rank, title, level, p_eff: pEff, ...extra }
}

describe('buildZonePaths', () => {
  it('lays a module out in material order with 1-based path positions', () => {
    const paths = buildZonePaths([
      mk('concept/c', 'fa', 3, '第三'),
      mk('concept/a', 'fa', 1, '第一'),
      mk('concept/b', 'fa', 2, '第二'),
    ])
    assert.deepEqual(paths.fa.map((e) => e.slug), ['concept/a', 'concept/b', 'concept/c'])
    assert.deepEqual(paths.fa.map((e) => e.order), [1, 2, 3])
  })

  it('trails nodes without a material position after the ordered ones, ties break by title then slug', () => {
    const paths = buildZonePaths([
      mk('concept/zz', 'fa', 0, '乙后'),
      mk('concept/mid', 'fa', 2, '第二'),
      mk('concept/aa', 'fa', 0, '甲前'),
      mk('concept/top', 'fa', 1, '第一'),
    ])
    // Ranked material first (1,2); unranked trail by title in Unicode
    // codepoint order (乙 U+4E59 < 甲 U+7532 — codepoint, not pinyin) —
    // deterministic is the contract, collation is not.
    assert.deepEqual(paths.fa.map((e) => e.slug), ['concept/top', 'concept/mid', 'concept/zz', 'concept/aa'])
  })

  it('is deterministic across shuffled input (no re-shuffle between refreshes)', () => {
    const nodes = [
      mk('concept/e', 'fa', 5, '五'), mk('concept/d', 'fa', 4, '四'), mk('concept/c', 'fa', 3, '三'),
      mk('concept/b', 'fa', 2, '二'), mk('concept/a', 'fa', 1, '一'),
    ]
    const forward = buildZonePaths(nodes)
    const shuffled = buildZonePaths([...nodes].reverse())
    assert.deepEqual(shuffled.fa.map((e) => e.slug), forward.fa.map((e) => e.slug))
    assert.deepEqual(shuffled.fa.map((e) => e.order), forward.fa.map((e) => e.order))
  })

  it('groups by folder, empty folder_id lands under root, and per-entry mastery carries through', () => {
    const paths = buildZonePaths([
      mk('concept/x', 'fa', 1, '甲一', 'touched', 0.42),
      mk('concept/y', '', 1, '根一', 'mastered', 0.9, { skipped: true }),
      mk('concept/z', '', 2, '根二', 'unseen', 0, { faded: true }),
    ])
    assert.deepEqual(paths.fa.map((e) => e.slug), ['concept/x'])
    assert.equal(paths.fa[0].pEff, 0.42)
    assert.deepEqual(paths.root.map((e) => e.slug), ['concept/y', 'concept/z'])
    assert.ok(paths.root[0].skipped)
    assert.ok(paths.root[1].faded)
    assert.equal(paths.root[1].title, '根二') // title falls back to slug only when missing
  })
})

describe('circledNum', () => {
  it('renders 1..20 in the card vocabulary and plain numerals beyond', () => {
    assert.equal(circledNum(1), '①')
    assert.equal(circledNum(10), '⑩')
    assert.equal(circledNum(20), '⑳')
    assert.equal(circledNum(21), '21.')
    assert.equal(circledNum(0), '0.')
  })
})
