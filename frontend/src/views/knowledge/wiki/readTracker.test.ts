import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createWikiReadTracker } from './readTracker.ts'

test('visible dwell resumes after hiding, ignores background time, and stops cleanly', () => {
  const originalDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const originalNow = Date.now
  const originalSet = globalThis.setTimeout
  const originalClear = globalThis.clearTimeout
  let now = 0
  let hidden = false
  let nextID = 0
  const timers = new Map<number, { at: number; run: () => void }>()
  const listeners = new Set<() => void>()
  Object.defineProperty(globalThis, 'document', { configurable: true, value: {
    get hidden() { return hidden },
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  } })
  Date.now = () => now
  globalThis.setTimeout = ((fn: () => void, delay: number) => {
    timers.set(++nextID, { at: now + delay, run: fn })
    return nextID
  }) as unknown as typeof setTimeout
  globalThis.clearTimeout = ((id: number) => { timers.delete(id) }) as unknown as typeof clearTimeout
  const tick = (ms: number) => {
    const target = now + ms
    while (true) {
      const pending = [...timers].filter(([, timer]) => timer.at <= target)
        .sort((a, b) => a[1].at - b[1].at)[0]
      if (!pending) break
      now = pending[1].at
      timers.delete(pending[0])
      pending[1].run()
    }
    now = target
  }
  const visibility = (value: boolean) => {
    hidden = value
    for (const fn of listeners) fn()
  }
  const recorded: string[] = []
  const tracker = createWikiReadTracker((slug, tier) => recorded.push(`${slug}:${tier}`))
  try {
    tracker.start()
    tracker.start()
    assert.equal(listeners.size, 1)
    tracker.enter('a')
    tick(2_000)
    visibility(true)
    tick(120_000)
    assert.deepEqual(recorded, [])
    visibility(false)
    tick(5_000)
    assert.deepEqual(recorded, ['a:normal'])
    tick(55_000)
    assert.deepEqual(recorded, ['a:normal', 'a:deep'], 'deep feedback must arrive before leaving the visible page')
    visibility(true)
    visibility(true)
    assert.deepEqual(recorded, ['a:normal', 'a:deep'])
    tracker.enter('b') // selecting a page while hidden must not start its clock
    tick(120_000)
    visibility(false)
    tick(4_000)
    tracker.enter('c')
    tick(5_000)
    assert.deepEqual(recorded, ['a:normal', 'a:deep', 'c:normal'])
    tracker.stop()
    visibility(false)
    tick(120_000)
    assert.equal(listeners.size, 0)
    assert.equal(timers.size, 0)
    assert.deepEqual(recorded, ['a:normal', 'a:deep', 'c:normal'])
  } finally {
    tracker.stop()
    Date.now = originalNow
    globalThis.setTimeout = originalSet
    globalThis.clearTimeout = originalClear
    if (originalDocument) Object.defineProperty(globalThis, 'document', originalDocument)
    else Reflect.deleteProperty(globalThis, 'document')
  }
})
