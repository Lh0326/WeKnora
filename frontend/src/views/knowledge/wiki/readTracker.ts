/**
 * Tiered wiki-read tracker for the learning signal.
 *
 * Replaces the fire-on-open recordWikiRead calls with dwell-aware tiers:
 * - normal: the page stayed open ≥ NORMAL_DELAY_MS (5s). Quick flips
 *   (< 5s) record nothing — a glance is not a study action.
 * - deep: uses the supplied content-cost threshold (60s fallback), with a
 *   boundary check on leave. Backend caps learning credit; a revisit after
 *   difficulty can be recorded separately without another full credit.
 *
 * One tracker instance tracks ONE "current page" slot: callers call
 * enter(slug) whenever the active page changes (browser selection, graph
 * drawer, drawer close revert) and leave()/settle on unmount. Tab-hidden
 * settles the current page (deep check) and re-enters on visible, so
 * hidden time never counts as dwell.
 */

export const READ_NORMAL_DELAY_MS = 5_000
export const READ_DEEP_THRESHOLD_MS = 60_000

// A reading-cost heuristic, not a claim that elapsed time proves understanding.
// Short pages can produce visible progress without a fixed one-minute wait.
export function readingThresholdMs(content:string):number {
  const chars=Array.from(content.replace(/\s+/g,'')).length
  return Math.max(8_000,Math.min(180_000,Math.ceil(chars/350*60_000)))
}

export interface ReadRecorder {
  (slug: string, tier: 'normal' | 'deep'): void
}

export function createWikiReadTracker(record: ReadRecorder, thresholdFor: (slug:string)=>number = ()=>READ_DEEP_THRESHOLD_MS) {
  let currentSlug = ''
  let openedAt: number | null = null
  let started = false
  let normalTimer: ReturnType<typeof setTimeout> | null = null
  let deepTimer: ReturnType<typeof setTimeout> | null = null
  let deepRecorded = false
  let deepThreshold = READ_DEEP_THRESHOLD_MS

  function pause() {
    if (normalTimer) {
      clearTimeout(normalTimer)
      normalTimer = null
    }
    if (deepTimer) { clearTimeout(deepTimer); deepTimer = null }
    const dwell = openedAt === null ? 0 : Date.now() - openedAt
    if (currentSlug && !deepRecorded && dwell >= deepThreshold) {
      deepRecorded = true
      record(currentSlug, 'deep')
    }
    openedAt = null
  }

  function settle() {
    pause()
    currentSlug = ''
  }

  function resume() {
    if (!started || !currentSlug || document.hidden || openedAt !== null) return
    const slug = currentSlug
    deepThreshold = Math.max(READ_NORMAL_DELAY_MS,thresholdFor(slug))
    deepRecorded = false
    openedAt = Date.now()
    normalTimer = setTimeout(() => {
      normalTimer = null
      if (!document.hidden && currentSlug === slug) record(slug, 'normal')
    }, READ_NORMAL_DELAY_MS)
    // Give feedback while the user is still reading, rather than waiting
    // for the page to close. pause() retains the boundary safeguard.
    deepTimer = setTimeout(() => {
      deepTimer = null
      if (!document.hidden && currentSlug === slug && !deepRecorded) {
        deepRecorded = true
        record(slug, 'deep')
      }
    }, deepThreshold)
  }

  function enter(slug: string) {
    if (slug === currentSlug) return
    settle()
    if (!slug) return
    currentSlug = slug
    resume()
  }

  function onVisibilityChange() {
    if (document.hidden) {
      pause()
    } else {
      resume()
    }
  }

  function start() {
    if (started) return
    started = true
    document.addEventListener('visibilitychange', onVisibilityChange)
    resume()
  }

  function stop() {
    started = false
    document.removeEventListener('visibilitychange', onVisibilityChange)
    settle()
  }

  function restart() {
    pause()
    resume()
  }

  return { enter, settle, start, stop, restart }
}
