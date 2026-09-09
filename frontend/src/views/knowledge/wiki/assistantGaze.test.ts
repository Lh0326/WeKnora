import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { pupilOffset } from './assistantGaze.ts'

describe('pupilOffset', () => {
  it('points toward the pointer and saturates at maxOffset', () => {
    const center = { x: 100, y: 100 }
    // Far to the right: saturates, direction +x
    const far = pupilOffset(center, { x: 900, y: 100 }, 3, 18)
    assert.ok(Math.abs(far.x - 3) < 1e-9 && Math.abs(far.y) < 1e-9)

    // Far up-left diagonal: unit direction, saturated magnitude
    const diag = pupilOffset(center, { x: center.x - 500, y: center.y - 500 }, 3, 18)
    assert.ok(Math.abs(diag.x - (-3 / Math.SQRT2)) < 1e-9)
    assert.ok(Math.abs(diag.y - (-3 / Math.SQRT2)) < 1e-9)
  })

  it('eases to zero when the pointer is on the ball', () => {
    const center = { x: 50, y: 50 }
    const on = pupilOffset(center, { x: 50.4, y: 50 }, 3, 18)
    assert.ok(Math.abs(on.x) < 0.1 && on.y === 0)

    const exact = pupilOffset(center, center)
    assert.deepEqual(exact, { x: 0, y: 0 })
  })

  it('magnitude grows with distance up to the cap', () => {
    const center = { x: 0, y: 0 }
    const near = pupilOffset(center, { x: 18, y: 0 }, 3, 18)
    const mid = pupilOffset(center, { x: 36, y: 0 }, 3, 18)
    const cap = pupilOffset(center, { x: 360, y: 0 }, 3, 18)
    assert.ok(Math.abs(near.x - 1) < 1e-9)
    assert.ok(Math.abs(mid.x - 2) < 1e-9)
    assert.ok(Math.abs(cap.x - 3) < 1e-9)
  })
})
